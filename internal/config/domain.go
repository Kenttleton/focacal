package config

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"

	"go.yaml.in/yaml/v3"
)

// Domain names one of the three portable configuration domains. Each domain
// lives in its own file inside the config directory.
type Domain string

const (
	DomainApp       Domain = "app"
	DomainPlanner   Domain = "planner"
	DomainCalendars Domain = "calendars"
)

// Domains lists every domain in load order (later files win on overlap).
var Domains = []Domain{DomainApp, DomainPlanner, DomainCalendars}

// ErrUnknownDomain is returned for a Domain that is not one of Domains.
var ErrUnknownDomain = errors.New("unknown config domain")

var domainFiles = map[Domain]string{
	DomainApp:       "config.yaml",
	DomainPlanner:   "planner.yaml",
	DomainCalendars: "calendars.yaml",
}

// File returns the file name (relative to the config directory) holding d.
func (d Domain) File() string { return domainFiles[d] }

// Valid reports whether d is a known domain.
func (d Domain) Valid() bool { _, ok := domainFiles[d]; return ok }

// domainKey describes one top-level key owned by a domain.
type domainKey struct {
	name string
	// typ is the shape a patch value for this key must have. For collections
	// it is the item type.
	typ reflect.Type
	// collection is set for id-keyed list keys.
	collection *CollectionSpec
	// get returns the typed value of this key from c.
	get func(c *Config) any
	// write, when set, returns the value marshalDomain saves. It is wider
	// than get where a hand-written secret (inline api_key) must survive a
	// save although the API view never shows it.
	write func(c *Config) any
}

// appAuth is the part of auth that belongs to the App domain. Login
// credentials (username, password_hash) live in credentials.yaml.
type appAuth struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// plannerAgent is AgentConfig without its api_key, which belongs in .env.
type plannerAgent struct {
	BaseURL     string `json:"base_url" yaml:"base_url"`
	Model       string `json:"model" yaml:"model"`
	TimeoutSecs int    `json:"timeout_secs" yaml:"timeout_secs"`
	MaxTokens   int    `json:"max_tokens" yaml:"max_tokens"`
}

var domainKeys = map[Domain][]domainKey{
	DomainApp: {
		{name: "server", typ: reflect.TypeFor[ServerConfig](), get: func(c *Config) any { return c.Server }},
		{name: "auth", typ: reflect.TypeFor[appAuth](), get: func(c *Config) any { return appAuth{Enabled: c.GlobalConfig.Auth.Enabled} },
			write: func(c *Config) any { return c.GlobalConfig.Auth }},
	},
	DomainPlanner: {
		{name: "working_hours", typ: reflect.TypeFor[WorkingHours](), get: func(c *Config) any { return c.WorkingHours }},
		{name: "lunch", typ: reflect.TypeFor[LunchConfig](), get: func(c *Config) any { return c.Lunch }},
		{name: "focus", typ: reflect.TypeFor[FocusConfig](), get: func(c *Config) any { return c.Focus }},
		// colors is the generated-event palette keyed by event kind (spec 9.6).
		{name: "colors", typ: reflect.TypeFor[map[string]string](), get: func(c *Config) any { return c.Colors }},
		// agent is the scheduler endpoint; its api_key stays in .env.
		{name: "agent", typ: reflect.TypeFor[plannerAgent](), get: func(c *Config) any {
			a := c.Agent
			return plannerAgent{BaseURL: a.BaseURL, Model: a.Model, TimeoutSecs: a.TimeoutSecs, MaxTokens: a.MaxTokens}
		}, write: func(c *Config) any { return c.Agent }},
	},
	DomainCalendars: {
		{name: "accounts", typ: reflect.TypeFor[AccountConfig](), collection: &IDSpec, get: func(c *Config) any { return c.Accounts }},
		{name: "calendars", typ: reflect.TypeFor[CalendarConfig](), collection: &IDSpec, get: func(c *Config) any { return c.Calendars }},
		{name: "destinations", typ: reflect.TypeFor[DestinationConfig](), collection: &IDSpec, get: func(c *Config) any { return c.Destinations }},
		{name: "oauth", typ: reflect.TypeFor[OAuthConfig](), get: func(c *Config) any { return c.OAuth }},
	},
}

// DomainForKey returns the domain that owns top-level key k.
func DomainForKey(k string) (Domain, bool) {
	for _, d := range Domains {
		for _, dk := range domainKeys[d] {
			if dk.name == k {
				return d, true
			}
		}
	}
	return "", false
}

// collections returns the collection specs of d, for ApplyMergePatch.
func (d Domain) collections() map[string]CollectionSpec {
	out := map[string]CollectionSpec{}
	for _, dk := range domainKeys[d] {
		if dk.collection != nil {
			out[dk.name] = *dk.collection
		}
	}
	return out
}

// validate runs d's validator on c.
func (d Domain) validate(c *Config) error {
	switch d {
	case DomainApp:
		return c.ValidateApp()
	case DomainPlanner:
		return c.ValidatePlanner()
	case DomainCalendars:
		return c.ValidateCalendars()
	}
	return ErrUnknownDomain
}

// marshalDomain renders exactly d's keys of c as YAML, in domainKeys order,
// collections as lists in their existing order. The output is deterministic
// (map-typed values such as colors are sorted by yaml) and carries no
// comments. Inline api_key values already in the file are carried through
// (the API view and patches never expose or accept them).
func marshalDomain(d Domain, c *Config) ([]byte, error) {
	if !d.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDomain, d)
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, dk := range domainKeys[d] {
		var v yaml.Node
		val := dk.get
		if dk.write != nil {
			val = dk.write
		}
		if err := v.Encode(val(c)); err != nil {
			return nil, fmt.Errorf("encode %s: %w", dk.name, err)
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: dk.name}, &v)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("encode %s: %w", d.File(), err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode %s: %w", d.File(), err)
	}
	return buf.Bytes(), nil
}
