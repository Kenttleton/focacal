package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/you/focacal/internal/connector"
	"github.com/you/focacal/internal/httpclient"
)

// RegisterWebhook registers a Google Calendar push notification channel.
// webhookURL must be publicly reachable (e.g. https://yourdomain.com/webhooks/google).
func (c *Connector) RegisterWebhook(ctx context.Context, webhookURL string) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}

	for _, calID := range c.calendarIDs() {
		if err := c.registerCalendarWebhook(ctx, client, calID, webhookURL); err != nil {
			return fmt.Errorf("register webhook for %q: %w", calID, err)
		}
	}
	return nil
}

func (c *Connector) registerCalendarWebhook(ctx context.Context, client *http.Client, calendarID, webhookURL string) error {
	channelID := uuid.New().String()
	body, _ := json.Marshal(map[string]interface{}{
		"id":      channelID,
		"type":    "web_hook",
		"address": webhookURL,
		"token":   c.cfg.ID, // sent back in X-Goog-Channel-Token header
	})

	escapedCalID := url.PathEscape(calendarID)
	apiURL := fmt.Sprintf("%s/calendars/%s/events/watch", googleCalAPIBase, escapedCalID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	respBody, status, err := httpclient.DoWithRetry(ctx, client, req)
	if err != nil {
		return err
	}

	if status != http.StatusOK {
		return fmt.Errorf("watch API %d: %s", status, respBody)
	}

	var result struct {
		ID         string `json:"id"`
		Expiration int64  `json:"expiration"` // Unix ms
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return err
	}

	expiry := time.Unix(result.Expiration/1000, 0)
	stateKey := c.cfg.ID + ":" + calendarID
	return c.db.SetSyncState(ctx, stateKey, "", result.ID, expiry)
}

// ValidateWebhook verifies an incoming Google push notification.
// Google sends the channel token in X-Goog-Channel-Token.
func (c *Connector) ValidateWebhook(_ context.Context, _ []byte, headers map[string]string) (string, error) {
	token := headers["x-goog-channel-token"]
	if token == "" {
		return "", fmt.Errorf("missing X-Goog-Channel-Token")
	}
	if token != c.cfg.ID {
		return "", fmt.Errorf("channel token mismatch")
	}
	return c.cfg.ID, nil
}

// WriteEvent creates or updates an event on Google Calendar.
func (c *Connector) WriteEvent(ctx context.Context, e *connector.RemoteEvent) (string, error) {
	client, err := c.httpClient()
	if err != nil {
		return "", err
	}

	calID := "primary"
	if len(c.cfg.CalendarIDs) > 0 {
		calID = c.cfg.CalendarIDs[0]
	}

	showAs := "busy"
	if e.ShowAs != "" {
		showAs = e.ShowAs
	}

	payload := map[string]interface{}{
		"summary":     e.Title,
		"description": e.Description,
		"location":    e.Location,
		"start":       map[string]string{"dateTime": fmt.Sprintf("%v", e.Start), "timeZone": "UTC"},
		"end":         map[string]string{"dateTime": fmt.Sprintf("%v", e.End), "timeZone": "UTC"},
		"transparency": func() string {
			if showAs == "free" {
				return "transparent"
			}
			return "opaque"
		}(),
	}
	if e.ManagedTag {
		payload["extendedProperties"] = map[string]interface{}{
			"private": map[string]string{"X-FOCACAL-MANAGED": "true"},
		}
	}

	body, _ := json.Marshal(payload)
	escapedCalID := url.PathEscape(calID)

	var (
		apiURL string
		method string
	)
	if e.RemoteID != "" {
		apiURL = fmt.Sprintf("%s/calendars/%s/events/%s", googleCalAPIBase, escapedCalID,
			url.PathEscape(e.RemoteID))
		method = http.MethodPut
	} else {
		apiURL = fmt.Sprintf("%s/calendars/%s/events", googleCalAPIBase, escapedCalID)
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	respBody, status, err := httpclient.DoWithRetry(ctx, client, req)
	if err != nil {
		return "", err
	}

	if status < 200 || status >= 300 {
		return "", fmt.Errorf("write event API %d: %s", status, respBody)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	return result.ID, nil
}

// DeleteEvent removes an event from Google Calendar by its remote ID.
func (c *Connector) DeleteEvent(ctx context.Context, remoteID string) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}

	calID := "primary"
	if len(c.cfg.CalendarIDs) > 0 {
		calID = c.cfg.CalendarIDs[0]
	}

	escapedCalID := url.PathEscape(calID)
	apiURL := fmt.Sprintf("%s/calendars/%s/events/%s", googleCalAPIBase,
		escapedCalID, url.PathEscape(remoteID))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return err
	}

	_, status, err := httpclient.DoWithRetry(ctx, client, req)
	if err != nil {
		return err
	}

	if status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("delete event API %d", status)
	}
	return nil
}
