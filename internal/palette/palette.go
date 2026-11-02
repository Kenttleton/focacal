package palette

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Kind is an event kind.
type Kind string

const (
	Meeting    Kind = "meeting"
	Focus      Kind = "focus"
	Routine    Kind = "routine"
	Transition Kind = "transition"
	Conflict   Kind = "conflict"
)

func Defaults() map[Kind]string {
	return map[Kind]string{
		Meeting:    "#4A90D9",
		Focus:      "#2E9E5B",
		Routine:    "#D9534F",
		Transition: "#9B9B9B",
		Conflict:   "#F5A623",
	}
}

func Hex(r, g, b uint8) string {
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

func Parse(s string) (uint8, uint8, uint8, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) == 3 {
			r, _ := strconv.ParseUint(string(h[0])+string(h[0]), 16, 8)
			g, _ := strconv.ParseUint(string(h[1])+string(h[1]), 16, 8)
			b, _ := strconv.ParseUint(string(h[2])+string(h[2]), 16, 8)
			return uint8(r), uint8(g), uint8(b), nil
		}
		if len(h) == 6 {
			r, err := strconv.ParseUint(h[0:2], 16, 8)
			if err != nil {
				return 0, 0, 0, err
			}
			g, err := strconv.ParseUint(h[2:4], 16, 8)
			if err != nil {
				return 0, 0, 0, err
			}
			b, err := strconv.ParseUint(h[4:6], 16, 8)
			if err != nil {
				return 0, 0, 0, err
			}
			return uint8(r), uint8(g), uint8(b), nil
		}
		return 0, 0, 0, fmt.Errorf("bad hex color %q", s)
	}
	re := regexp.MustCompile(`rgb\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)`)
	m := re.FindStringSubmatch(s)
	if m != nil {
		r, _ := strconv.Atoi(m[1])
		g, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		return uint8(r), uint8(g), uint8(b), nil
	}
	return 0, 0, 0, fmt.Errorf("unparseable color %q", s)
}

func Resolve(overrides map[string]string) (map[Kind]string, error) {
	out := Defaults()
	for k, v := range overrides {
		kind := Kind(k)
		if _, ok := out[kind]; !ok {
			return nil, fmt.Errorf("unknown color kind %q", k)
		}
		r, g, b, err := Parse(v)
		if err != nil {
			return nil, fmt.Errorf("bad color for %q: %w", k, err)
		}
		out[kind] = Hex(r, g, b)
	}
	return out, nil
}

func KindForBlock(t string) Kind {
	switch t {
	case "focus":
		return Focus
	case "lunch":
		return Routine
	case "break":
		return Transition
	}
	return Meeting
}
