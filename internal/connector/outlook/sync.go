package outlook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Kenttleton/focacal/internal/connector"
	"github.com/Kenttleton/focacal/internal/httpclient"
	"github.com/Kenttleton/focacal/internal/store"
)

// Sync performs a full or incremental sync of Outlook calendar events.
func (c *Connector) Sync(ctx context.Context) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}
	for _, calID := range c.cfg.EffectiveCalendarIDs() {
		if err := c.syncCalendar(ctx, client, calID); err != nil {
			return err
		}
	}
	return nil
}

func (c *Connector) syncCalendar(ctx context.Context, client *http.Client, calendarID string) error {
	// Outlook uses delta queries for incremental sync.
	// On first run we use the full event list; subsequent runs use the delta link.
	syncToken, _, _, err := c.db.GetSyncState(ctx, syncKey(c.cfg.ID, calendarID))
	if err != nil {
		return err
	}

	var apiURL string
	if syncToken != "" {
		apiURL = syncToken // Graph delta link is a full URL.
	} else {
		now := time.Now()
		start := now.AddDate(0, 0, -7).Format(time.RFC3339)
		end := now.AddDate(0, 0, 14).Format(time.RFC3339)

		calPath := "/me/calendar/calendarView"
		if calendarID != "" && calendarID != "primary" {
			calPath = fmt.Sprintf("/me/calendars/%s/calendarView", calendarID)
		}
		apiURL = fmt.Sprintf("%s%s/delta?startDateTime=%s&endDateTime=%s&$top=100",
			graphAPIBase, calPath, start, end)
	}

	for apiURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Prefer", `outlook.timezone="UTC"`)

		body, status, err := httpclient.DoWithRetry(ctx, client, req)
		if err != nil {
			return err
		}

		if status != http.StatusOK {
			if isExpiredRefreshToken(status, body) {
				return fmt.Errorf("outlook[%s]: refresh token expired — run 'focacal auth outlook %s'",
					c.cfg.ID, c.cfg.TokenKey())
			}
			return fmt.Errorf("graph API %d: %s", status, body)
		}

		var result struct {
			Value     []outlookEvent `json:"value"`
			NextLink  string         `json:"@odata.nextLink"`
			DeltaLink string         `json:"@odata.deltaLink"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return err
		}

		for _, item := range result.Value {
			if err := c.upsertEvent(ctx, calendarID, item); err != nil {
				return fmt.Errorf("upsert event %q: %w", item.ID, err)
			}
		}

		if result.DeltaLink != "" {
			if err := c.db.SetSyncState(ctx, syncKey(c.cfg.ID, calendarID), result.DeltaLink, "", time.Time{}); err != nil {
				return err
			}
		}

		apiURL = result.NextLink
	}
	return nil
}

func (c *Connector) upsertEvent(ctx context.Context, calendarID string, item outlookEvent) error {
	cal := calendarID
	if cal == "" || cal == "primary" {
		cal = "primary"
	}
	if item.IsRemoved() {
		return c.db.DeleteEvent(ctx, store.EventID(c.cfg.ID, cal, item.ID))
	}

	e := toStoreEvent(c.cfg.ID, cal, item)
	return c.db.UpsertEvent(ctx, e)
}

// toStoreEvent maps an Outlook event to a store.Event.
func toStoreEvent(sourceID, calendarID string, item outlookEvent) *store.Event {
	e := &store.Event{
		ID:             store.EventID(sourceID, calendarID, item.ID),
		SourceID:       sourceID,
		CalendarID:     calendarID,
		UID:            item.ID,
		ICalUID:        item.ICalUID,
		Title:          item.Subject,
		Description:    item.BodyPreview,
		Location:       item.Location.DisplayName,
		ConferenceURL:  item.OnlineMeetingURL,
		ResponseStatus: outlookResponseStatus(item),
		ShowAs:         outlookShowAs(item.ShowAs),
	}
	if item.IsCancelled {
		e.Status = "cancelled"
	} else {
		e.Status = "confirmed"
	}

	if item.IsAllDay {
		e.AllDay = true
		e.Start, _ = time.Parse("2006-01-02", item.Start.DateTime[:10])
		e.End, _ = time.Parse("2006-01-02", item.End.DateTime[:10])
	} else {
		e.Start, _ = time.Parse("2006-01-02T15:04:05.9999999", item.Start.DateTime)
		e.End, _ = time.Parse("2006-01-02T15:04:05.9999999", item.End.DateTime)
	}
	return e
}

func outlookResponseStatus(item outlookEvent) string {
	switch item.ResponseStatus.Response {
	case "tentativelyAccepted":
		return "tentative"
	case "declined":
		return "declined"
	case "accepted", "organizer":
		return "accepted"
	case "none", "notResponded":
		return "needs_action"
	}
	if item.ResponseStatus.Response == "" {
		return "accepted"
	}
	return "needs_action"
}

func outlookShowAs(s string) string {
	switch s {
	case "free":
		return "free"
	case "oof":
		return "oof"
	default:
		return "busy"
	}
}

// RegisterWebhook registers or renews a Graph change notification subscription.
// It is idempotent: no-op if the subscription has more than 24 hours remaining,
// renews if expiring soon, and creates a new one if none exists.
func (c *Connector) RegisterWebhook(ctx context.Context, webhookURL string) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}

	syncToken, webhookID, webhookExpiry, err := c.db.GetSyncState(ctx, c.cfg.ID)
	if err != nil {
		return err
	}

	// More than 24 hours remaining — no action needed.
	if webhookID != "" && time.Until(webhookExpiry) > 24*time.Hour {
		return nil
	}

	// Try to renew an existing subscription before creating a new one.
	if webhookID != "" {
		newExpiry, err := c.renewSubscription(ctx, client, webhookID)
		if err == nil {
			return c.db.SetSyncState(ctx, c.cfg.ID, syncToken, webhookID, newExpiry)
		}
		// Renewal failed (subscription may have already expired); fall through.
	}

	subID, expiry, err := c.createSubscription(ctx, client, webhookURL)
	if err != nil {
		return err
	}
	return c.db.SetSyncState(ctx, c.cfg.ID, syncToken, subID, expiry)
}

// createSubscription POSTs a new Graph subscription and returns its ID and expiry.
func (c *Connector) createSubscription(ctx context.Context, client *http.Client, notificationURL string) (string, time.Time, error) {
	// Graph allows at most 4320 minutes (3 days) for calendar subscriptions.
	expiresAt := time.Now().UTC().Add(4319 * time.Minute)

	payload, _ := json.Marshal(map[string]any{
		"changeType":         "created,updated,deleted",
		"notificationUrl":    notificationURL,
		"resource":           "/me/events",
		"expirationDateTime": expiresAt.Format(time.RFC3339),
		"clientState":        c.cfg.ID,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		graphAPIBase+"/subscriptions", bytes.NewReader(payload))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	body, status, err := httpclient.DoWithRetry(ctx, client, req)
	if err != nil {
		return "", time.Time{}, err
	}

	if status != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("create subscription %d: %s", status, body)
	}

	var result struct {
		ID                 string `json:"id"`
		ExpirationDateTime string `json:"expirationDateTime"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parse subscription response: %w", err)
	}
	expiry, _ := time.Parse(time.RFC3339, result.ExpirationDateTime)
	return result.ID, expiry, nil
}

// renewSubscription PATCHes an existing subscription with a new expiry.
func (c *Connector) renewSubscription(ctx context.Context, client *http.Client, subscriptionID string) (time.Time, error) {
	expiresAt := time.Now().UTC().Add(4319 * time.Minute)

	payload, _ := json.Marshal(map[string]any{
		"expirationDateTime": expiresAt.Format(time.RFC3339),
	})

	apiURL := fmt.Sprintf("%s/subscriptions/%s", graphAPIBase, subscriptionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, apiURL, bytes.NewReader(payload))
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	body, status, err := httpclient.DoWithRetry(ctx, client, req)
	if err != nil {
		return time.Time{}, err
	}

	if status != http.StatusOK {
		return time.Time{}, fmt.Errorf("renew subscription %d: %s", status, body)
	}

	var result struct {
		ExpirationDateTime string `json:"expirationDateTime"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return time.Time{}, fmt.Errorf("parse renewal response: %w", err)
	}
	expiry, _ := time.Parse(time.RFC3339, result.ExpirationDateTime)
	return expiry, nil
}

// ValidateWebhook verifies an incoming Graph change notification.
// Graph sends the source ID as clientState on each notification POST.
// The initial subscription validation handshake (GET ?validationToken=…) is
// handled at the HTTP layer by the REST server before this method is called.
func (c *Connector) ValidateWebhook(_ context.Context, payload []byte, _ map[string]string) (string, error) {
	var notification struct {
		Value []struct {
			ClientState string `json:"clientState"`
		} `json:"value"`
	}
	if err := json.Unmarshal(payload, &notification); err != nil {
		return "", fmt.Errorf("parse notification: %w", err)
	}
	if len(notification.Value) == 0 {
		return "", fmt.Errorf("empty notification payload")
	}
	if notification.Value[0].ClientState != c.cfg.ID {
		return "", fmt.Errorf("clientState mismatch: got %q, want %q",
			notification.Value[0].ClientState, c.cfg.ID)
	}
	return c.cfg.ID, nil
}

// WriteEvent creates or updates an event on the Outlook calendar.
// A new event is created when e.RemoteID is empty; otherwise the existing event is patched.
func (c *Connector) WriteEvent(ctx context.Context, e *connector.RemoteEvent) (string, error) {
	client, err := c.httpClient()
	if err != nil {
		return "", err
	}

	start, _ := e.Start.(time.Time)
	end, _ := e.End.(time.Time)

	showAs := "busy"
	switch e.ShowAs {
	case "free", "tentative", "oof":
		showAs = e.ShowAs
	}

	eventPayload := map[string]any{
		"subject": e.Title,
		"body":    map[string]string{"contentType": "text", "content": e.Description},
		"start":   map[string]string{"dateTime": start.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
		"end":     map[string]string{"dateTime": end.UTC().Format("2006-01-02T15:04:05"), "timeZone": "UTC"},
		"showAs":  showAs,
	}
	if e.ManagedTag {
		// singleValueExtendedProperties is the Graph equivalent of Google's extendedProperties.
		eventPayload["singleValueExtendedProperties"] = []map[string]string{{
			"id":    "String {66f5a359-4659-4830-9070-00047ec6ac6e} Name X-FOCACAL-MANAGED",
			"value": "true",
		}}
	}

	calID := ""
	if len(c.cfg.CalendarIDs) > 0 {
		calID = c.cfg.CalendarIDs[0]
	}

	var apiURL, method string
	if e.RemoteID != "" {
		if calID != "" {
			apiURL = fmt.Sprintf("%s/me/calendars/%s/events/%s", graphAPIBase, calID, e.RemoteID)
		} else {
			apiURL = fmt.Sprintf("%s/me/events/%s", graphAPIBase, e.RemoteID)
		}
		method = http.MethodPatch
	} else {
		if calID != "" {
			apiURL = fmt.Sprintf("%s/me/calendars/%s/events", graphAPIBase, calID)
		} else {
			apiURL = fmt.Sprintf("%s/me/events", graphAPIBase)
		}
		method = http.MethodPost
	}

	payload, _ := json.Marshal(eventPayload)
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	respBody, status, err := httpclient.DoWithRetry(ctx, client, req)
	if err != nil {
		return "", err
	}

	if status < 200 || status >= 300 {
		if isExpiredRefreshToken(status, respBody) {
			return "", fmt.Errorf("outlook[%s]: refresh token expired — run 'focacal auth outlook %s'",
				c.cfg.ID, c.cfg.TokenKey())
		}
		return "", fmt.Errorf("write event %s %d: %s", method, status, respBody)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse write event response: %w", err)
	}
	return result.ID, nil
}

// DeleteEvent removes an event from the Outlook calendar by its remote ID.
func (c *Connector) DeleteEvent(ctx context.Context, remoteID string) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}

	calID := ""
	if len(c.cfg.CalendarIDs) > 0 {
		calID = c.cfg.CalendarIDs[0]
	}

	var apiURL string
	if calID != "" {
		apiURL = fmt.Sprintf("%s/me/calendars/%s/events/%s", graphAPIBase, calID, remoteID)
	} else {
		apiURL = fmt.Sprintf("%s/me/events/%s", graphAPIBase, remoteID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return err
	}

	_, status, err := httpclient.DoWithRetry(ctx, client, req)
	if err != nil {
		return err
	}

	if status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("delete event %d", status)
	}
	return nil
}

// outlookEvent represents a Microsoft Graph calendar event.
type outlookEvent struct {
	ID               string `json:"id"`
	ICalUID          string `json:"iCalUId"`
	Subject          string `json:"subject"`
	BodyPreview      string `json:"bodyPreview"`
	IsAllDay         bool   `json:"isAllDay"`
	IsCancelled      bool   `json:"isCancelled"`
	ShowAs           string `json:"showAs"` // free | tentative | busy | oof | workingElsewhere | unknown
	OnlineMeetingURL string `json:"onlineMeetingUrl"`
	ResponseStatus   struct {
		Response string `json:"response"`
	} `json:"responseStatus"`
	Removed          json.RawMessage `json:"@removed"`
	Start            struct {
		DateTime string `json:"dateTime"`
		TimeZone string `json:"timeZone"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		TimeZone string `json:"timeZone"`
	} `json:"end"`
	Location struct {
		DisplayName string `json:"displayName"`
	} `json:"location"`
}

// IsRemoved reports whether this is a delta-removed event.
func (e outlookEvent) IsRemoved() bool {
	return len(e.Removed) > 0
}

// syncKey returns the sync-state key for a source/calendar pair.
func syncKey(sourceID, calendarID string) string {
	if calendarID == "" {
		calendarID = "primary"
	}
	return sourceID + ":" + calendarID
}
