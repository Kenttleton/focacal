// Package destination turns the store's events and planned blocks into what a
// destination publishes: its inputs, filtered and trimmed by visibility.
package destination

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// HiddenTitle replaces an event title that is hidden without a rename.
const HiddenTitle = "Busy"

// Project returns the events and blocks destination d publishes. Events
// belong to an input when their SourceID equals the input's calendar id; the
// focacal input admits planner blocks, limited to its kinds when set. The
// inputs are never modified: the result holds copies.
//
// An input with no event block (nil) shows everything unchanged. Otherwise a hidden title becomes its rename, or "Busy"; a
// visible title with a rename is replaced by the rename; a hidden body is
// emptied. An input that hides either also loses location, meeting link,
// source marker and original uid (see the loop below).
func Project(cfg *config.Config, d config.DestinationConfig, events []*store.Event, blocks []*store.Block) ([]*store.Event, []*store.Block) {
	byCal := map[string]config.DestinationInput{}
	for _, in := range cfg.ExpandInputs(d) {
		if _, dup := byCal[in.Calendar]; !dup {
			byCal[in.Calendar] = in
		}
	}

	var outEvents []*store.Event
	for _, e := range events {
		in, ok := byCal[e.SourceID]
		if !ok || e.SourceID == config.FocacalSource {
			continue
		}
		c := *e
		if in.Event != nil && (!in.Event.Title.Visibility || !in.Event.Body.Visibility) {
			// A trimmed event must not leak through its other fields: the
			// location and meeting link follow the body, and the id is
			// replaced by an opaque one with no source marker.
			c.Location = ""
			c.ConferenceURL = ""
			c.CalendarID = ""
			c.UID = ""
			c.ICalUID = ""
			c.SourceID = ""
			c.ID = opaqueID(d.ID, e.ID)
		}
		c.Title = visibleTitle(in.Event, e.Title)
		if in.Event != nil && !in.Event.Body.Visibility {
			c.Description = ""
			c.RawICal = ""
		}
		outEvents = append(outEvents, &c)
	}

	var outBlocks []*store.Block
	if in, ok := byCal[config.FocacalSource]; ok {
		for _, b := range blocks {
			if !kindAllowed(in.Kinds, b.Type) {
				continue
			}
			c := *b
			c.Title = visibleTitle(in.Event, b.Title)
			if b.WrittenBack != nil {
				c.WrittenBack = make(map[string]string, len(b.WrittenBack))
				for k, v := range b.WrittenBack {
					c.WrittenBack[k] = v
				}
			}
			outBlocks = append(outBlocks, &c)
		}
	}
	return outEvents, outBlocks
}

// opaqueID is a stable id that reveals neither the calendar nor the upstream uid.
func opaqueID(destID, eventID string) string {
	sum := sha256.Sum256([]byte(destID + "\x00" + eventID))
	return hex.EncodeToString(sum[:16])
}

func visibleTitle(v *config.EventVisibility, title string) string {
	if v == nil {
		return title
	}
	if v.Title.Rename != "" {
		return v.Title.Rename
	}
	if !v.Title.Visibility {
		return HiddenTitle
	}
	return title
}

func kindAllowed(kinds []string, t store.BlockType) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, k := range kinds {
		if k == string(t) {
			return true
		}
	}
	return false
}
