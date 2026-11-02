package ical

import (
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/Kenttleton/focacal/internal/store"
)

// Expand expands a calendar into one store.Event per VEVENT instance in [from, to).
func Expand(cal *ical.Calendar, sourceID, calendarID string, from, to time.Time) ([]*store.Event, error) {
	if cal == nil {
		return nil, nil
	}

	events := cal.Events()

	// Collect overrides keyed by UID + original instance start.
	overrides := make(map[string]*ical.Event)
	for i := range events {
		ev := &events[i]
		uid, _ := ev.Props.Text(ical.PropUID)
		ridProp := ev.Props.Get(ical.PropRecurrenceID)
		if ridProp == nil {
			continue
		}
		rid, err := ridProp.DateTime(time.UTC)
		if err != nil {
			continue
		}
		key := uid + "|" + strconv.FormatInt(rid.Unix(), 10)
		e := *ev
		overrides[key] = &e
	}

	var out []*store.Event

	for i := range events {
		ev := &events[i]
		ridProp := ev.Props.Get(ical.PropRecurrenceID)
		if ridProp != nil {
			continue // handled as override
		}

		uid, _ := ev.Props.Text(ical.PropUID)
		status, _ := ev.Status()
		if status == ical.EventCancelled {
			continue
		}

		start, err := ev.DateTimeStart(time.UTC)
		if err != nil {
			continue
		}
		end, err := ev.DateTimeEnd(time.UTC)
		if err != nil {
			end = start
		}
		allDay := false
		if sp := ev.Props.Get(ical.PropDateTimeStart); sp != nil && sp.ValueType() == ical.ValueDate {
			allDay = true
		}
		dur := end.Sub(start)

		rrSet, _ := ev.RecurrenceSet(time.UTC)
		if rrSet == nil {
			// Single event.
			if !start.Before(from) && start.Before(to) {
				out = append(out, mapInstance(sourceID, calendarID, ev, uid, start, end, allDay))
			}
			continue
		}

		// Expand occurrences.
		occurrences := rrSet.Between(from, to, false)
		for _, occ := range occurrences {
			// Check override for this instance.
			key := uid + "|" + strconv.FormatInt(occ.Unix(), 10)
			if ov, ok := overrides[key]; ok {
				// skip base; override added below
				_ = ov
				continue
			}
			instStart := occ
			instEnd := occ.Add(dur)
			out = append(out, mapInstance(sourceID, calendarID, ev, uid, instStart, instEnd, allDay))
		}
	}

	// Add overrides.
	for _, ov := range overrides {
		uid, _ := ov.Props.Text(ical.PropUID)
		status, _ := ov.Status()
		if status == ical.EventCancelled {
			continue
		}
		start, err := ov.DateTimeStart(time.UTC)
		if err != nil {
			continue
		}
		if start.Before(from) || !start.Before(to) {
			continue
		}
		end, err := ov.DateTimeEnd(time.UTC)
		if err != nil {
			end = start
		}
		allDay := false
		if sp := ov.Props.Get(ical.PropDateTimeStart); sp != nil && sp.ValueType() == ical.ValueDate {
			allDay = true
		}
		out = append(out, mapInstance(sourceID, calendarID, ov, uid, start, end, allDay))
	}

	return out, nil
}

func mapInstance(sourceID, calendarID string, ev *ical.Event, uid string, start, end time.Time, allDay bool) *store.Event {
	summary, _ := ev.Props.Text(ical.PropSummary)
	description, _ := ev.Props.Text(ical.PropDescription)
	location, _ := ev.Props.Text(ical.PropLocation)

	transp := "busy"
	if t, _ := ev.Props.Text(ical.PropTransparency); strings.EqualFold(t, "TRANSPARENT") {
		transp = "free"
	}

	respStatus := partstatOf(ev)

	e := &store.Event{
		SourceID:       sourceID,
		CalendarID:     calendarID,
		UID:            uid,
		ICalUID:        uid,
		Title:          summary,
		Description:    description,
		Location:       location,
		Start:          start,
		End:            end,
		AllDay:         allDay,
		Status:         icalStatusToStore2(statusOf(ev)),
		ResponseStatus: respStatus,
		ShowAs:         transp,
	}
	// ID
	if rr := ev.Props.Get(ical.PropRecurrenceID); rr != nil {
		// override instance: use its start
	}
	if hasRRule(ev) {
		e.ID = store.EventID(sourceID, calendarID, uid) + "@" + strconv.FormatInt(start.Unix(), 10)
	} else {
		e.ID = store.EventID(sourceID, calendarID, uid)
	}
	if uid == "" {
		// ensure non-empty-ish; leave as is
	}
	return e
}

func hasRRule(ev *ical.Event) bool {
	rr, _ := ev.Props.RecurrenceRule()
	return rr != nil
}

func statusOf(ev *ical.Event) ical.EventStatus {
	s, _ := ev.Status()
	return s
}

func icalStatusToStore2(s ical.EventStatus) string {
	switch s {
	case ical.EventTentative:
		return "tentative"
	case ical.EventCancelled:
		return "cancelled"
	}
	return "confirmed"
}

func partstatOf(ev *ical.Event) string {
	org := ""
	if p := ev.Props.Get(ical.PropOrganizer); p != nil {
		org = p.Value
	}
	attendees := ev.Props[ical.PropAttendee]
	var chosen *ical.Prop
	if len(attendees) > 0 {
		if org != "" {
			for i := range attendees {
				if attendees[i].Value == org {
					chosen = &attendees[i]
					break
				}
			}
		}
		if chosen == nil {
			chosen = &attendees[0]
		}
	}
	if chosen == nil {
		return "accepted"
	}
	ps := chosen.Params.Get("PARTSTAT")
	switch strings.ToUpper(ps) {
	case "ACCEPTED":
		return "accepted"
	case "TENTATIVE":
		return "tentative"
	case "DECLINED":
		return "declined"
	case "NEEDS-ACTION", "":
		if ps == "" {
			return "accepted"
		}
		return "needs_action"
	}
	return "accepted"
}
