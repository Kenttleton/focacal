package destination

import (
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

func cfgWith(cals ...string) *config.Config {
	c := &config.Config{}
	for _, id := range cals {
		c.Calendars = append(c.Calendars, config.CalendarConfig{ID: id, URL: "https://x.test/" + id})
	}
	return c
}

func ev(src, title string) *store.Event {
	return &store.Event{ID: src + ":" + title, SourceID: src, Title: title, Description: "secret body", RawICal: "RAW"}
}

func decodeDest(t *testing.T, y string) config.DestinationConfig {
	t.Helper()
	var d config.DestinationConfig
	if err := yaml.Unmarshal([]byte(y), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestProjectVisibility(t *testing.T) {
	cfg := cfgWith("work", "home", "other")
	d := decodeDest(t, `
id: out
inputs:
  - calendar: work
    event:
      title: {visibility: false}
      body: {visibility: false}
  - calendar: home
    event:
      title: {visibility: false, rename: Away}
      body: {visibility: true}
  - calendar: ghost
target: {served: true}
`)
	events := []*store.Event{ev("work", "Therapy"), ev("home", "Dentist"), ev("other", "Unlisted")}
	got, _ := Project(cfg, d, events, nil)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (unlisted dropped)", len(got))
	}
	if got[0].Title != "Busy" || got[0].Description != "" || got[0].RawICal != "" {
		t.Errorf("hidden title/body: %+v", got[0])
	}
	if got[1].Title != "Away" || got[1].Description != "secret body" {
		t.Errorf("rename wins, body visible: %+v", got[1])
	}
}

func TestProjectVisibleTitleRename(t *testing.T) {
	cfg := cfgWith("work")
	d := decodeDest(t, `
id: out
inputs:
  - calendar: work
    event:
      title: {visibility: true, rename: Focus}
      body: {visibility: true}
target: {served: true}
`)
	got, _ := Project(cfg, d, []*store.Event{ev("work", "Real")}, nil)
	if got[0].Title != "Focus" {
		t.Errorf("title = %q", got[0].Title)
	}
}

// Absent event block means full visibility; an explicit all-false block hides
// everything, even though both decode to something close to the zero value.
func TestProjectVisibilityDecoding(t *testing.T) {
	cfg := cfgWith("work")
	absent := decodeDest(t, "id: o\ninputs:\n  - calendar: work\ntarget: {served: true}\n")
	got, _ := Project(cfg, absent, []*store.Event{ev("work", "Real")}, nil)
	if got[0].Title != "Real" || got[0].Description != "secret body" {
		t.Errorf("absent event block must be full visibility: %+v", got[0])
	}
	explicit := decodeDest(t, `
id: o
inputs:
  - calendar: work
    event:
      title: {visibility: false}
      body: {visibility: false}
target: {served: true}
`)
	got, _ = Project(cfg, explicit, []*store.Event{ev("work", "Real")}, nil)
	if got[0].Title != "Busy" || got[0].Description != "" {
		t.Errorf("explicit false must hide: %+v", got[0])
	}
	// And it survives a save/load round trip.
	out, err := yaml.Marshal(explicit)
	if err != nil {
		t.Fatal(err)
	}
	var again config.DestinationConfig
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	got, _ = Project(cfg, again, []*store.Event{ev("work", "Real")}, nil)
	if got[0].Title != "Busy" || got[0].Description != "" {
		t.Errorf("round trip lost hiding: %+v\n%s", got[0], out)
	}
}

func TestProjectAllExcludesOwnTarget(t *testing.T) {
	cfg := cfgWith("work", "team")
	d := config.DestinationConfig{ID: "o", Inputs: config.InputList{All: true}, Target: config.DestinationTarget{Calendar: "team"}}
	got, _ := Project(cfg, d, []*store.Event{ev("work", "a"), ev("team", "b")}, nil)
	if len(got) != 1 || got[0].SourceID != "work" {
		t.Errorf("got %+v", got)
	}
}

func TestProjectKinds(t *testing.T) {
	cfg := cfgWith("work")
	now := time.Now()
	blocks := []*store.Block{
		{ID: "1", Type: store.BlockFocus, Title: "Focus", Start: now, End: now},
		{ID: "2", Type: store.BlockLunch, Title: "Lunch", Start: now, End: now},
	}
	d := config.DestinationConfig{ID: "o", Inputs: config.InputList{Items: []config.DestinationInput{{Calendar: "focacal", Kinds: []string{"focus"}}}}}
	_, got := Project(cfg, d, nil, blocks)
	if len(got) != 1 || got[0].Type != store.BlockFocus {
		t.Errorf("kinds=[focus]: %+v", got)
	}
	d.Inputs.Items[0].Kinds = nil
	if _, got = Project(cfg, d, nil, blocks); len(got) != 2 {
		t.Errorf("empty kinds keeps all: %d", len(got))
	}
	d.Inputs.Items = []config.DestinationInput{{Calendar: "work"}}
	if _, got = Project(cfg, d, nil, blocks); len(got) != 0 {
		t.Errorf("no focacal input, no blocks: %d", len(got))
	}
}

func TestProjectDoesNotMutateInputs(t *testing.T) {
	cfg := cfgWith("work")
	d := decodeDest(t, "id: o\ninputs:\n  - calendar: work\n    event:\n      title: {visibility: false}\ntarget: {served: true}\n")
	e := ev("work", "Real")
	b := &store.Block{ID: "1", Type: store.BlockFocus, Title: "Focus"}
	d.Inputs.Items = append(d.Inputs.Items, config.DestinationInput{Calendar: "focacal", Event: config.EventVisibility{Title: config.FieldVisibility{Rename: "X"}}})
	got, gb := Project(cfg, d, []*store.Event{e}, []*store.Block{b})
	if e.Title != "Real" || e.Description != "secret body" || b.Title != "Focus" {
		t.Errorf("inputs mutated: %+v %+v", e, b)
	}
	if got[0] == e || gb[0] == b {
		t.Error("returned pointers alias inputs")
	}
}
