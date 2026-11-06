package caldav

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	gocaldav "github.com/emersion/go-webdav/caldav"
	"github.com/Kenttleton/focacal/internal/store"
)

// Sync performs a full sync of CalDAV events into the store.
// CalDAV lacks a universal incremental-sync token, so a time-range query
// is issued on every call. The window matches the Google/Outlook connectors:
// 7 days back, 14 days forward.
func (c *Connector) Sync(ctx context.Context) error {
	client, err := c.caldavClient()
	if err != nil {
		return err
	}

	// Discover the calendar home set via the principal URL.
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return fmt.Errorf("caldav[%s]: find principal: %w", c.cfg.ID, err)
	}
	homeSet, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return fmt.Errorf("caldav[%s]: find home set: %w", c.cfg.ID, err)
	}

	calendars, err := client.FindCalendars(ctx, homeSet)
	if err != nil {
		return fmt.Errorf("caldav[%s]: find calendars: %w", c.cfg.ID, err)
	}

	matched := 0
	for _, cal := range calendars {
		if !c.wantCalendar(homeSet, cal.Path) {
			continue
		}
		matched++
		if err := c.syncCalendar(ctx, client, cal.Path); err != nil {
			return fmt.Errorf("caldav[%s]: sync %q: %w", c.cfg.ID, cal.Path, err)
		}
	}
	if len(c.cfg.CalendarIDs) > 0 && matched == 0 {
		// Name the calendar only: the remote id may be a URL with credentials.
		slog.Warn("caldav calendar matches no collection on the server; nothing synced", "calendar", c.cfg.ID)
	}
	return nil
}

// remotePath resolves a configured remote id to a collection path the way the
// write path does: an absolute URL is reduced to its path, a path starting
// with "/" is used as is, and anything else is a fragment under the home set.
func remotePath(homeSet, id string) string {
	if u, err := url.Parse(id); err == nil && u.Scheme != "" && u.Host != "" {
		return u.EscapedPath()
	}
	if strings.HasPrefix(id, "/") {
		return id
	}
	return strings.TrimSuffix(homeSet, "/") + "/" + id + "/"
}

// normPath is the comparison key for a collection path: percent-decoded,
// lower-cased, trailing slash dropped.
func normPath(p string) string {
	if d, err := url.PathUnescape(p); err == nil {
		p = d
	}
	return strings.ToLower(strings.TrimSuffix(p, "/"))
}

// wantCalendar reports whether the collection at path should be synced.
//
// With no CalendarIDs every collection of the home set is synced (the
// behavior before the calendar model). A resolved calendar carries its remote
// id in CalendarIDs; it is resolved with remotePath and must EQUAL the
// collection path (as returned by FindCalendars) after normPath: trailing
// slash, letter case and percent-encoding are not significant. A substring
// match is wrong: "/home/a/" would also select "/home/a/archive/".
func (c *Connector) wantCalendar(homeSet, path string) bool {
	if len(c.cfg.CalendarIDs) == 0 {
		return true
	}
	p := normPath(path)
	for _, id := range c.cfg.CalendarIDs {
		if normPath(remotePath(homeSet, id)) == p {
			return true
		}
	}
	return false
}

func (c *Connector) syncCalendar(ctx context.Context, client *gocaldav.Client, calPath string) error {
	now := time.Now()
	from := now.AddDate(0, 0, -7)
	to := now.AddDate(0, 0, 14)

	query := &gocaldav.CalendarQuery{
		CompRequest: gocaldav.CalendarCompRequest{
			Name:     ical.CompCalendar,
			AllProps: false,
			Comps: []gocaldav.CalendarCompRequest{{
				Name:     ical.CompEvent,
				AllProps: true,
			}},
		},
		CompFilter: gocaldav.CompFilter{
			Name: ical.CompCalendar,
			Comps: []gocaldav.CompFilter{{
				Name:  ical.CompEvent,
				Start: from.UTC(),
				End:   to.UTC(),
			}},
		},
	}

	objects, err := client.QueryCalendar(ctx, calPath, query)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}

	// Collect the paths returned by this sync to detect deletions.
	seen := make(map[string]bool, len(objects))
	for _, obj := range objects {
		seen[obj.Path] = true
		if err := c.upsertObject(ctx, calPath, obj); err != nil {
			return err
		}
	}

	// Remove store events whose path was not returned (deleted or moved outside window).
	if err := c.pruneDeleted(ctx, calPath, seen); err != nil {
		return err
	}

	return nil
}

func (c *Connector) upsertObject(ctx context.Context, calPath string, obj gocaldav.CalendarObject) error {
	if obj.Data == nil {
		return nil
	}
	for _, event := range obj.Data.Events() {
		if err := c.upsertEvent(ctx, obj.Path, event); err != nil {
			return err
		}
	}
	return nil
}

func (c *Connector) upsertEvent(ctx context.Context, path string, event ical.Event) error {
	uid, _ := event.Props.Text(ical.PropUID)
	if uid == "" {
		uid = path // fall back to path as unique key
	}

	status, _ := event.Status()
	if status == ical.EventCancelled {
		return c.db.DeleteEvent(ctx, c.cfg.ID+":"+uid)
	}

	summary, _ := event.Props.Text(ical.PropSummary)
	description, _ := event.Props.Text(ical.PropDescription)
	location, _ := event.Props.Text(ical.PropLocation)

	// Extract conference URL from CONFERENCE property or scan description.
	conferenceURL := ""
	if confProp := event.Props.Get(ical.PropConference); confProp != nil {
		conferenceURL = confProp.Value
	}

	e := &store.Event{
		ID:            c.cfg.ID + ":" + uid,
		SourceID:      c.cfg.ID,
		UID:           uid,
		Title:         summary,
		Description:   description,
		Location:      location,
		ConferenceURL: conferenceURL,
		Status:        icalStatusToStore(status),
	}

	// Encode the raw iCal for the event so it can be round-tripped on write-back.
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//focacal//focacal//EN")
	cal.Children = append(cal.Children, event.Component)
	var buf bytes.Buffer
	if encErr := ical.NewEncoder(&buf).Encode(cal); encErr == nil {
		e.RawICal = buf.String()
	}

	// Parse start/end times.
	start, err := event.DateTimeStart(time.UTC)
	if err == nil {
		e.Start = start
	}
	end, err := event.DateTimeEnd(time.UTC)
	if err == nil {
		e.End = end
	}

	// Detect all-day events (DATE value type rather than DATE-TIME).
	if dtstart := event.Props.Get(ical.PropDateTimeStart); dtstart != nil {
		if dtstart.ValueType() == ical.ValueDate {
			e.AllDay = true
		}
	}

	return c.db.UpsertEvent(ctx, e)
}

// pruneDeleted removes store events for this source/calendar whose object path
// was not returned in the latest sync, indicating deletion on the server.
func (c *Connector) pruneDeleted(ctx context.Context, calPath string, seen map[string]bool) error {
	// List all events for this source.
	now := time.Now()
	events, err := c.db.ListEventsByCalendar(ctx, c.cfg.ID, "", now.AddDate(0, 0, -7), now.AddDate(0, 0, 14))
	if err != nil {
		return err
	}
	for _, e := range events {
		// Reconstruct the expected object path: calPath + uid + ".ics"
		expectedPath := calPath + e.UID + ".ics"
		if !seen[expectedPath] {
			if delErr := c.db.DeleteEvent(ctx, e.ID); delErr != nil {
				return delErr
			}
		}
	}
	return nil
}

func icalStatusToStore(s ical.EventStatus) string {
	switch s {
	case ical.EventTentative:
		return "tentative"
	case ical.EventCancelled:
		return "cancelled"
	default:
		return "confirmed"
	}
}
