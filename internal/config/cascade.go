package config

import "slices"

// pruneCascade removes what the deletes of one patch leave dangling, working
// on the merged document before it is decoded and validated, so the delete
// and its cascade reach disk in one write. deleted maps a collection name
// (accounts, calendars) to the ids this patch removed.
//
// Order: calendars of a deleted account are deleted (and join the deleted
// calendars), then destinations whose target is a deleted calendar are
// removed, then inputs naming a deleted calendar are dropped from the
// remaining destinations. A destination left with no inputs stays. Nothing
// else is touched: a dangling reference someone wrote by hand is not a
// reference this patch deleted, so it is left alone. Destinations are
// compared by id, so only the ones that change are modified.
func pruneCascade(doc map[string]any, deleted map[string][]string) []Pruned {
	var out []Pruned
	gone := slices.Clone(deleted["calendars"])

	if accts := deleted["accounts"]; len(accts) > 0 {
		list, _ := doc["calendars"].([]any)
		kept := make([]any, 0, len(list))
		for _, it := range list {
			m, _ := it.(map[string]any)
			if m != nil && slices.Contains(accts, str(m["account"])) {
				id := str(m["id"])
				gone = append(gone, id)
				out = append(out, Pruned{Kind: "calendar", ID: id})
				continue
			}
			kept = append(kept, it)
		}
		if len(kept) != len(list) {
			doc["calendars"] = kept
		}
	}
	if len(gone) == 0 {
		return out
	}

	list, _ := doc["destinations"].([]any)
	if len(list) == 0 {
		return out
	}
	var inputs, dests []Pruned
	kept := make([]any, 0, len(list))
	for _, it := range list {
		m, _ := it.(map[string]any)
		if m == nil {
			kept = append(kept, it)
			continue
		}
		id := str(m["id"])
		if tgt, _ := m["target"].(map[string]any); tgt != nil && slices.Contains(gone, str(tgt["calendar"])) && str(tgt["calendar"]) != "" {
			dests = append(dests, Pruned{Kind: "destination", ID: id})
			continue
		}
		if items, ok := m["inputs"].([]any); ok {
			left := make([]any, 0, len(items))
			for _, in := range items {
				im, _ := in.(map[string]any)
				if cal := str(im["calendar"]); im != nil && cal != "" && slices.Contains(gone, cal) {
					inputs = append(inputs, Pruned{Kind: "destination_input", ID: id + "/" + cal})
					continue
				}
				left = append(left, in)
			}
			if len(left) != len(items) {
				m["inputs"] = left
			}
		}
		kept = append(kept, it)
	}
	doc["destinations"] = kept
	out = append(out, inputs...)
	return append(out, dests...)
}
