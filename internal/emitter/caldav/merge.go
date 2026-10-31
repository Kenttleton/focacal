package caldav

import (
	"fmt"
	"strings"
	"time"

	"github.com/Kenttleton/focacal/internal/store"
)

const icsTimeFormat = "20060102T150405Z"
const icsDateFormat = "20060102"

// BuildICS produces a VCALENDAR string merging real events and planned blocks.
func BuildICS(events []*store.Event, blocks []*store.Block) string {
	var b strings.Builder

	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//focacal//focacal//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("METHOD:PUBLISH\r\n")
	b.WriteString("X-WR-CALNAME:Focacal\r\n")
	b.WriteString("X-WR-TIMEZONE:UTC\r\n")

	for _, e := range events {
		writeEventVEVENT(&b, e)
	}

	for _, bl := range blocks {
		writeBlockVEVENT(&b, bl)
	}

	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func writeEventVEVENT(b *strings.Builder, e *store.Event) {
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString(fmt.Sprintf("UID:%s\r\n", icsEscape(e.ID)))
	b.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", icsEscape(e.Title)))
	b.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", time.Now().UTC().Format(icsTimeFormat)))
	b.WriteString(fmt.Sprintf("LAST-MODIFIED:%s\r\n", e.SyncedAt.UTC().Format(icsTimeFormat)))
	b.WriteString(fmt.Sprintf("X-FOCACAL-SOURCE:%s\r\n", icsEscape(e.SourceID)))
	b.WriteString(fmt.Sprintf("X-FOCACAL-TYPE:real\r\n"))

	if e.AllDay {
		b.WriteString(fmt.Sprintf("DTSTART;VALUE=DATE:%s\r\n", e.Start.Format(icsDateFormat)))
		b.WriteString(fmt.Sprintf("DTEND;VALUE=DATE:%s\r\n", e.End.Format(icsDateFormat)))
	} else {
		b.WriteString(fmt.Sprintf("DTSTART:%s\r\n", e.Start.UTC().Format(icsTimeFormat)))
		b.WriteString(fmt.Sprintf("DTEND:%s\r\n", e.End.UTC().Format(icsTimeFormat)))
	}

	if e.Description != "" {
		b.WriteString(fmt.Sprintf("DESCRIPTION:%s\r\n", icsEscape(foldLong(e.Description))))
	}
	if e.Location != "" {
		b.WriteString(fmt.Sprintf("LOCATION:%s\r\n", icsEscape(e.Location)))
	}
	if e.ConferenceURL != "" {
		b.WriteString(fmt.Sprintf("URL:%s\r\n", e.ConferenceURL))
	}

	statusMap := map[string]string{
		"confirmed": "CONFIRMED",
		"tentative": "TENTATIVE",
		"cancelled": "CANCELLED",
	}
	if s, ok := statusMap[e.Status]; ok {
		b.WriteString(fmt.Sprintf("STATUS:%s\r\n", s))
	}

	b.WriteString("END:VEVENT\r\n")
}

func writeBlockVEVENT(b *strings.Builder, bl *store.Block) {
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString(fmt.Sprintf("UID:focacal-block-%s\r\n", bl.ID))
	b.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", icsEscape(bl.Title)))
	b.WriteString(fmt.Sprintf("DTSTART:%s\r\n", bl.Start.UTC().Format(icsTimeFormat)))
	b.WriteString(fmt.Sprintf("DTEND:%s\r\n", bl.End.UTC().Format(icsTimeFormat)))
	b.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", bl.UpdatedAt.UTC().Format(icsTimeFormat)))
	b.WriteString(fmt.Sprintf("LAST-MODIFIED:%s\r\n", bl.UpdatedAt.UTC().Format(icsTimeFormat)))
	b.WriteString(fmt.Sprintf("X-FOCACAL-TYPE:%s\r\n", string(bl.Type)))
	b.WriteString("X-FOCACAL-MANAGED:true\r\n")
	b.WriteString("STATUS:CONFIRMED\r\n")

	// Color hint for calendar clients that support it (Apple Calendar, etc.).
	colorMap := map[store.BlockType]string{
		store.BlockFocus: "#4A90D9", // blue
		store.BlockLunch: "#7ED321", // green
		store.BlockBreak: "#F5A623", // amber
	}
	if color, ok := colorMap[bl.Type]; ok {
		b.WriteString(fmt.Sprintf("COLOR:%s\r\n", color))
	}

	b.WriteString("END:VEVENT\r\n")
}

// icsEscape escapes special characters for iCalendar text values.
func icsEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// foldLong truncates long descriptions to keep ICS reasonable.
func foldLong(s string) string {
	const max = 500
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
