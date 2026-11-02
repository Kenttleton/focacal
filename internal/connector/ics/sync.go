package ics

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gocal "github.com/emersion/go-ical"
	icalmap "github.com/Kenttleton/focacal/internal/ical"
)

func (c *Connector) Sync(ctx context.Context) error {
	feedURL, err := c.cfg.ResolveICSURL()
	if err != nil {
		return err
	}

	now := time.Now()
	from := now.AddDate(0, 0, -7)
	to := now.AddDate(0, 0, 14)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return fmt.Errorf("ics[%s]: fetch failed: %w", c.cfg.ID, err)
	}
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}
	if c.lastMod != "" {
		req.Header.Set("If-Modified-Since", c.lastMod)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return fmt.Errorf("ics[%s]: fetch failed: %w", c.cfg.ID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ics[%s]: fetch failed: status %d", c.cfg.ID, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ics[%s]: fetch failed: %w", c.cfg.ID, err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("ics[%s]: empty body", c.cfg.ID)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(body)), "BEGIN:VCALENDAR") {
		return fmt.Errorf("ics[%s]: invalid body", c.cfg.ID)
	}

	cal, err := gocal.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		return fmt.Errorf("ics[%s]: parse failed: %w", c.cfg.ID, err)
	}

	events, err := icalmap.Expand(cal, c.cfg.ID, "default", from, to)
	if err != nil {
		return fmt.Errorf("ics[%s]: expand failed: %w", c.cfg.ID, err)
	}

	// Upsert.
	newIDs := make(map[string]bool)
	for _, e := range events {
		if err := c.db.UpsertEvent(ctx, e); err != nil {
			return fmt.Errorf("ics[%s]: upsert failed: %w", c.cfg.ID, err)
		}
		newIDs[e.ID] = true
	}

	// Prune rows in window not returned.
	existing, err := c.db.ListEventsByCalendar(ctx, c.cfg.ID, "default", from, to)
	if err != nil {
		return fmt.Errorf("ics[%s]: list failed: %w", c.cfg.ID, err)
	}
	for _, e := range existing {
		if !newIDs[e.ID] {
			if err := c.db.DeleteEvent(ctx, e.ID); err != nil {
				return fmt.Errorf("ics[%s]: delete failed: %w", c.cfg.ID, err)
			}
		}
	}

	// Store conditional headers.
	if et := resp.Header.Get("ETag"); et != "" {
		c.etag = et
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		c.lastMod = lm
	}
	_ = bufio.NewReader
	return nil
}
