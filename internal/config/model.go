package config

import (
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// AccountConfig is one connection to a calendar provider (Google, Outlook or
// CalDAV). Calendars reference an account by id.
type AccountConfig struct {
	ID              string `json:"id" yaml:"id"`
	Name            string `json:"name" yaml:"name,omitempty"`
	Type            string `json:"type" yaml:"type"` // google | outlook | caldav
	CredentialsFile string `json:"credentials_file" yaml:"credentials_file,omitempty"`
	TokenFile       string `json:"token_file" yaml:"token_file,omitempty"`
	CalDAVURL       string `json:"caldav_url" yaml:"caldav_url,omitempty"`
	Username        string `json:"username" yaml:"username,omitempty"`
	Password        string `json:"password" yaml:"password,omitempty"`
	AuthType        string `json:"auth_type" yaml:"auth_type,omitempty"`
}

// CalendarConfig is one calendar Focacal reads: either a calendar of an
// account (account and remote_id) or an ICS feed (url or url_env).
type CalendarConfig struct {
	ID        string         `json:"id" yaml:"id"`
	Name      string         `json:"name" yaml:"name,omitempty"`
	Account   string         `json:"account" yaml:"account,omitempty"`
	RemoteID  string         `json:"remote_id" yaml:"remote_id,omitempty"` // empty = the connector default
	URL       string         `json:"url" yaml:"url,omitempty"`             // secret feed URL
	URLEnv    string         `json:"url_env" yaml:"url_env,omitempty"`     // env var holding the feed URL
	Plan      *bool          `json:"plan" yaml:"plan,omitempty"`           // nil means true
	Conflicts ConflictConfig `json:"conflicts" yaml:"conflicts,omitempty"`
}

// Plans reports whether the planner schedules around this calendar. The
// default, when plan is unset, is true.
func (c CalendarConfig) Plans() bool { return c.Plan == nil || *c.Plan }

// DestinationConfig is somewhere Focacal publishes: a calendar it writes to
// or a feed it serves.
type DestinationConfig struct {
	ID     string            `json:"id" yaml:"id"`
	Name   string            `json:"name" yaml:"name,omitempty"`
	Inputs InputList         `json:"inputs" yaml:"inputs,omitempty"`
	Target DestinationTarget `json:"target" yaml:"target"`
}

// DestinationInput is one calendar (or focacal) feeding a destination.
type DestinationInput struct {
	Calendar string          `json:"calendar" yaml:"calendar"`
	Event    EventVisibility `json:"event" yaml:"event,omitempty"`
	Kinds    []string        `json:"kinds,omitempty" yaml:"kinds,omitempty"` // focus | lunch | break, focacal input only
}

// DestinationTarget names where a destination publishes: exactly one of a
// writable calendar or the served feed.
type DestinationTarget struct {
	Calendar string `json:"calendar,omitempty" yaml:"calendar,omitempty"`
	Served   bool   `json:"served,omitempty" yaml:"served,omitempty"`
	Auth     string `json:"auth,omitempty" yaml:"auth,omitempty"` // private | public; empty means private
}

// InputList is a destination's inputs: the scalar "all" or an explicit list.
type InputList struct {
	All   bool
	Items []DestinationInput
}

// IsZero lets omitempty drop an unset list.
func (l InputList) IsZero() bool { return !l.All && len(l.Items) == 0 }

func (l InputList) MarshalYAML() (any, error) {
	if l.All {
		return "all", nil
	}
	if l.Items == nil {
		return []DestinationInput{}, nil
	}
	return l.Items, nil
}

func (l *InputList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			*l = InputList{}
			return nil
		}
		if n.Value != "all" {
			return fmt.Errorf("inputs must be \"all\" or a list")
		}
		*l = InputList{All: true}
		return nil
	case yaml.SequenceNode:
		var items []DestinationInput
		if err := n.Decode(&items); err != nil {
			return err
		}
		*l = InputList{Items: items}
		return nil
	}
	return fmt.Errorf("inputs must be \"all\" or a list")
}

func (l InputList) MarshalJSON() ([]byte, error) {
	if l.All {
		return []byte(`"all"`), nil
	}
	return json.Marshal(nonNilInputs(l.Items))
}

func (l *InputList) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case nil:
		*l = InputList{}
		return nil
	case string:
		if t != "all" {
			return fmt.Errorf("inputs must be \"all\" or a list")
		}
		*l = InputList{All: true}
		return nil
	case []any:
		var items []DestinationInput
		if err := json.Unmarshal(b, &items); err != nil {
			return err
		}
		*l = InputList{Items: items}
		return nil
	}
	return fmt.Errorf("inputs must be \"all\" or a list")
}

func nonNilInputs(in []DestinationInput) []DestinationInput {
	if in == nil {
		return []DestinationInput{}
	}
	return in
}
