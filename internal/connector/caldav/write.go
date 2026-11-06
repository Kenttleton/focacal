package caldav

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/Kenttleton/focacal/internal/connector"
)

// RegisterWebhook is a no-op for CalDAV sources — push notifications are not
// universally supported. The polling loop handles change detection.
func (c *Connector) RegisterWebhook(_ context.Context, _ string) error { return nil }

// ValidateWebhook is a no-op for CalDAV sources.
func (c *Connector) ValidateWebhook(_ context.Context, _ []byte, _ map[string]string) (string, error) {
	return c.cfg.ID, nil
}

// WriteEvent creates or updates an event on the CalDAV server.
//
// The remote id is the event's iCalendar UID, which is exactly what Sync
// stores as store.Event.UID, so the write ledger can recognize the event when
// it is read back. e.RemoteID is the UID returned by a previous WriteEvent
// (empty creates a new event with a generated UID); the object path is derived
// from the calendar and the UID.
func (c *Connector) WriteEvent(ctx context.Context, e *connector.RemoteEvent) (string, error) {
	client, err := c.caldavClient()
	if err != nil {
		return "", err
	}

	start, _ := e.Start.(time.Time)
	end, _ := e.End.(time.Time)

	// Build a new VCALENDAR with the event.
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//focacal//focacal//EN")

	event := ical.NewEvent()
	uid := e.RemoteID
	if uid == "" {
		uid = fmt.Sprintf("focacal-%d@focacal", time.Now().UnixNano())
	}

	event.Props.SetText(ical.PropUID, uid)
	event.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	event.Props.SetText(ical.PropSummary, e.Title)
	event.Props.SetText(ical.PropDescription, e.Description)
	event.Props.SetText(ical.PropLocation, e.Location)
	event.Props.SetDateTime(ical.PropDateTimeStart, start.UTC())
	event.Props.SetDateTime(ical.PropDateTimeEnd, end.UTC())

	if e.ManagedTag {
		event.Props.SetText("X-FOCACAL-MANAGED", "TRUE")
	}

	// Map ShowAs to TRANSP.
	transp := "OPAQUE"
	if e.ShowAs == "free" {
		transp = "TRANSPARENT"
	}
	event.Props.SetText(ical.PropTransparency, transp)

	cal.Children = append(cal.Children, event.Component)

	objectPath, err := c.objectPath(ctx, uid)
	if err != nil {
		return "", err
	}
	if _, err := client.PutCalendarObject(ctx, objectPath, cal); err != nil {
		return "", fmt.Errorf("put calendar object: %w", err)
	}
	return uid, nil
}

// objectPath is the object path of the event with the given UID in the
// source's calendar.
func (c *Connector) objectPath(ctx context.Context, uid string) (string, error) {
	calPath, err := c.defaultCalendarPath(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(calPath, "/") + "/" + url.PathEscape(uid) + ".ics", nil
}

// DeleteEvent removes an event from the CalDAV server. remoteID is the UID
// returned by WriteEvent.
func (c *Connector) DeleteEvent(ctx context.Context, remoteID string) error {
	client, err := c.caldavClient()
	if err != nil {
		return err
	}
	objectPath, err := c.objectPath(ctx, remoteID)
	if err != nil {
		return err
	}
	// caldav.Client embeds webdav.Client which has RemoveAll.
	if err := client.RemoveAll(ctx, objectPath); err != nil {
		// go-webdav's HTTP error type is internal; its text starts with the
		// status ("404 Not Found"). A missing object is already deleted.
		if strings.HasPrefix(err.Error(), "404 ") {
			return nil
		}
		return fmt.Errorf("delete calendar object %q: %w", remoteID, err)
	}
	return nil
}

// defaultCalendarPath returns the path of the first configured calendar,
// or discovers the default calendar if no CalendarIDs are set.
func (c *Connector) defaultCalendarPath(ctx context.Context) (string, error) {
	client, err := c.caldavClient()
	if err != nil {
		return "", err
	}

	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return "", fmt.Errorf("find principal: %w", err)
	}
	homeSet, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return "", fmt.Errorf("find home set: %w", err)
	}

	if len(c.cfg.CalendarIDs) > 0 {
		// CalendarIDs can be a path fragment, a full path or an absolute URL;
		// remotePath (shared with the sync filter) resolves all three.
		return remotePath(homeSet, c.cfg.CalendarIDs[0]), nil
	}

	// Default: use the home set path directly (works for single-calendar servers).
	return homeSet, nil
}

