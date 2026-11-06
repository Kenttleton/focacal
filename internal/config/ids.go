package config

import (
	"regexp"
	"strconv"
	"strings"
)

const maxIDLen = 63

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidID reports whether id is a valid account, calendar or destination id.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Slug turns a display name into an id candidate: lowercase ASCII letters and
// digits, runs of anything else collapsed to one "-", no leading or trailing
// "-", at most 63 characters. A name with no ASCII letter or digit gives
// "calendar".
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	s := b.String()
	if len(s) > maxIDLen {
		s = strings.TrimRight(s[:maxIDLen], "-")
	}
	if s == "" {
		return "calendar"
	}
	return s
}

// UniqueID returns Slug(name), with "-2", "-3"... appended until taken
// reports the candidate free. Candidates are lowercase; taken must compare
// case-insensitively (ids are unique ignoring case). The base is shortened
// when a suffix would push the id past 63 characters.
func UniqueID(name string, taken func(string) bool) string {
	base := Slug(name)
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		b := base
		if len(b)+len(suffix) > maxIDLen {
			b = strings.TrimRight(b[:maxIDLen-len(suffix)], "-")
		}
		if c := b + suffix; !taken(c) {
			return c
		}
	}
}
