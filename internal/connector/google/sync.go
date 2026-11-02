package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Kenttleton/focacal/internal/httpclient"
	"github.com/Kenttleton/focacal/internal/store"
)

// Sync performs a full or incremental sync of Google Calendar events into the store.
func (c *Connector) Sync(ctx context.Context) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}

	for _, calID := range c.calendarIDs() {
		if err := c.syncCalendar(ctx, client, calID); err != nil {
			return fmt.Errorf("sync calendar %q: %w", calID, err)
		}
	}
	return nil
}

func (c *Connector) syncCalendar(ctx context.Context, client *http.Client, calendarID string) error {
	syncToken, _, _, err := c.db.GetSyncState(ctx, c.cfg.ID+":"+calendarID)
	if err != nil {
		return err
	}

	// Build the request URL.
	params := url.Values{}
	params.Set("maxResults", "2500")
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")

	if syncToken != "" {
		params.Set("syncToken", syncToken)
	} else {
		// Full sync: fetch 7 days back and 14 days forward.
		now := time.Now()
		params.Set("timeMin", timeToRFC3339(now.AddDate(0, 0, -7)))
		params.Set("timeMax", timeToRFC3339(now.AddDate(0, 0, 14)))
	}

	escapedCalID := url.PathEscape(calendarID)
	apiURL := fmt.Sprintf("%s/calendars/%s/events?%s", googleCalAPIBase, escapedCalID, params.Encode())

	for apiURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return err
		}

		body, status, err := httpclient.DoWithRetry(ctx, client, req)
		if err != nil {
			return fmt.Errorf("list events: %w", err)
		}

		if status == http.StatusGone {
			// Sync token expired — fall back to full sync.
			if err := c.db.SetSyncState(ctx, c.cfg.ID+":"+calendarID, "", "", time.Time{}); err != nil {
				return err
			}
			return c.syncCalendar(ctx, client, calendarID)
		}
		if status != http.StatusOK {
			return fmt.Errorf("google API %d: %s", status, body)
		}

		var result struct {
			Items         []googleEvent `json:"items"`
			NextPageToken string        `json:"nextPageToken"`
			NextSyncToken string        `json:"nextSyncToken"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}

		for _, item := range result.Items {
			if err := c.upsertEvent(ctx, calendarID, item); err != nil {
				return fmt.Errorf("upsert event %q: %w", item.ID, err)
			}
		}

		if result.NextSyncToken != "" {
			if err := c.db.SetSyncState(ctx, c.cfg.ID+":"+calendarID, result.NextSyncToken, "", time.Time{}); err != nil {
				return err
			}
		}

		if result.NextPageToken != "" {
			params.Set("pageToken", result.NextPageToken)
			params.Del("syncToken")
			apiURL = fmt.Sprintf("%s/calendars/%s/events?%s", googleCalAPIBase, escapedCalID, params.Encode())
		} else {
			apiURL = ""
		}
	}
	return nil
}

func (c *Connector) upsertEvent(ctx context.Context, calendarID string, item googleEvent) error {
	if item.Status == "cancelled" {
		return c.db.DeleteEvent(ctx, store.EventID(c.cfg.ID, calendarID, item.ID))
	}

	e := toStoreEvent(c.cfg.ID, calendarID, item)
	return c.db.UpsertEvent(ctx, e)
}

// toStoreEvent maps a Google Calendar event to a store.Event.
func toStoreEvent(sourceID, calendarID string, item googleEvent) *store.Event {
	e := &store.Event{
		ID:             store.EventID(sourceID, calendarID, item.ID),
		SourceID:       sourceID,
		CalendarID:     calendarID,
		UID:            item.ID,
		ICalUID:        item.ICalUID,
		Title:          item.Summary,
		Description:    item.Description,
		Location:       item.Location,
		Status:         item.Status,
		ResponseStatus: googleResponseStatus(item),
		ConferenceURL:  extractConferenceURL(item),
	}
	if item.Transparency == "transparent" {
		e.ShowAs = "free"
	} else {
		e.ShowAs = "busy"
	}

	// Parse start/end — can be date-time or all-day date.
	if item.Start.DateTime != "" {
		e.Start, _ = time.Parse(time.RFC3339, item.Start.DateTime)
		e.End, _ = time.Parse(time.RFC3339, item.End.DateTime)
	} else {
		e.AllDay = true
		e.Start, _ = time.Parse("2006-01-02", item.Start.Date)
		e.End, _ = time.Parse("2006-01-02", item.End.Date)
	}

	return e
}

func googleResponseStatus(item googleEvent) string {
	for _, a := range item.Attendees {
		if a.Self {
			switch a.ResponseStatus {
			case "declined":
				return "declined"
			case "tentative":
				return "tentative"
			case "needsAction":
				return "needs_action"
			case "accepted":
				return "accepted"
			}
		}
	}
	if len(item.Attendees) == 0 {
		return "accepted"
	}
	// no self attendee: organizer? default accepted
	return "accepted"
}

// googleEvent matches the Google Calendar API event resource.
type googleEvent struct {
	ID           string `json:"id"`
	ICalUID      string `json:"iCalUID"`
	Summary      string `json:"summary"`
	Description  string `json:"description"`
	Location     string `json:"location"`
	Status       string `json:"status"`
	Transparency string `json:"transparency"`
	Attendees    []struct {
		Self           bool   `json:"self"`
		ResponseStatus string `json:"responseStatus"`
	} `json:"attendees"`
	Start struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"end"`
	ConferenceData struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType"`
			URI            string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData"`
	HangoutLink string `json:"hangoutLink"`
}

// videoURLPatterns matches Zoom, Meet, Teams, and Webex URLs.
var videoURLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`https?://[^\s]*zoom\.us/[^\s]*`),
	regexp.MustCompile(`https?://meet\.google\.com/[^\s]*`),
	regexp.MustCompile(`https?://teams\.microsoft\.com/[^\s]*`),
	regexp.MustCompile(`https?://[^\s]*webex\.com/[^\s]*`),
}

func extractConferenceURL(item googleEvent) string {
	// Prefer structured conferenceData.
	for _, ep := range item.ConferenceData.EntryPoints {
		if ep.EntryPointType == "video" && ep.URI != "" {
			return ep.URI
		}
	}
	if item.HangoutLink != "" {
		return item.HangoutLink
	}
	// Fall back to scanning description/location for known video URLs.
	for _, src := range []string{item.Description, item.Location} {
		for _, re := range videoURLPatterns {
			if m := re.FindString(src); m != "" {
				return m
			}
		}
	}
	return ""
}

// StripVideoLinks removes all known video conference URLs from a string.
// Used by the write-back sanitizer.
func StripVideoLinks(s string) string {
	for _, re := range videoURLPatterns {
		s = re.ReplaceAllString(s, "")
	}
	// Clean up leftover blank lines.
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
