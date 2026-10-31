package caldav

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/you/focacal/internal/connector"
)

// RegisterWebhook is a no-op for CalDAV sources — push notifications are not
// universally supported. The polling loop handles change detection.
func (c *Connector) RegisterWebhook(_ context.Context, _ string) error { return nil }

// ValidateWebhook is a no-op for CalDAV sources.
func (c *Connector) ValidateWebhook(_ context.Context, _ []byte, _ map[string]string) (string, error) {
	return c.cfg.ID, nil
}

// WriteEvent creates or updates an event on the CalDAV server.
// e.RemoteID should be the full object path on the server (returned by a
// previous WriteEvent call). If empty, a new path is constructed from the
// first configured calendar and the event UID.
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

	// Determine the object path.
	objectPath := e.RemoteID
	if objectPath == "" || !strings.HasSuffix(objectPath, ".ics") {
		// Construct path from calendar home + uid.
		calPath, pathErr := c.defaultCalendarPath(ctx)
		if pathErr != nil {
			return "", pathErr
		}
		objectPath = calPath + url.PathEscape(uid) + ".ics"
	}

	obj, err := client.PutCalendarObject(ctx, objectPath, cal)
	if err != nil {
		return "", fmt.Errorf("put calendar object: %w", err)
	}
	return obj.Path, nil
}

// DeleteEvent removes an event from the CalDAV server.
// remoteID must be the full object path returned by WriteEvent.
func (c *Connector) DeleteEvent(ctx context.Context, remoteID string) error {
	client, err := c.caldavClient()
	if err != nil {
		return err
	}
	// caldav.Client embeds webdav.Client which has RemoveAll.
	if err := client.RemoveAll(ctx, remoteID); err != nil {
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
		// CalendarIDs can be either a path fragment or a full path.
		id := c.cfg.CalendarIDs[0]
		if strings.HasPrefix(id, "/") {
			return id, nil
		}
		return homeSet + id + "/", nil
	}

	// Default: use the home set path directly (works for single-calendar servers).
	return homeSet, nil
}

