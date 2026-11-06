package config

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// decodeDomain decodes the keys d owns from b into into, strictly: an unknown
// key inside an owned key, or a value of the wrong type, is an error. Keys
// owned by other domains (a stray `sources:` in config.yaml) and unknown
// top-level keys are ignored, so one file can never override another. Empty
// input is a no-op. Errors name the field and line but never echo a value,
// since values can be secrets and these errors reach the log.
func decodeDomain(d Domain, b []byte, into *Config) error {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", d.File(), err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil // comments only
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("parse %s: top level must be a mapping", d.File())
	}
	if d == DomainCalendars {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "sources" {
				return fmt.Errorf("%s: the legacy \"sources\" key is no longer supported; restart focacal so it can migrate the file", d.File())
			}
		}
	}
	own := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(root.Content); i += 2 {
		for _, dk := range domainKeys[d] {
			if root.Content[i].Value == dk.name {
				own.Content = append(own.Content, root.Content[i], renameKeys(dk.name, dropObsoleteKeys(dk.name, root.Content[i+1])))
			}
		}
	}
	if len(own.Content) == 0 {
		return nil
	}
	filtered, err := yaml.Marshal(own)
	if err != nil {
		return fmt.Errorf("parse %s: %w", d.File(), err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(filtered))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", d.File(), redactDecodeError(err, filtered))
	}
	return nil
}

// obsoleteItemKeys are per-item keys that older SourceConfig/DestinationConfig
// versions had and later lost (git history: writeback and cross_busy on
// sources, from the pre-directory single config.yaml). MigrateLegacy moves
// sources verbatim, so they must keep loading under strict decoding.
var obsoleteItemKeys = []string{"writeback", "cross_busy"}

// dropObsoleteKeys removes known-obsolete keys from v before the strict
// decode: the credential keys that left config.yaml's auth (they moved to
// credentials.yaml; see legacyCredentialKeys) and obsoleteItemKeys on each
// source and destination. Any other unknown key is still rejected.
func dropObsoleteKeys(key string, v *yaml.Node) *yaml.Node {
	switch key {
	case "auth":
		return withoutKeys(v, legacyCredentialKeys)
	case "sources":
		if v.Kind != yaml.SequenceNode {
			return v
		}
		out := *v
		out.Content = make([]*yaml.Node, len(v.Content))
		for i, item := range v.Content {
			out.Content[i] = withoutKeys(item, obsoleteItemKeys)
		}
		return &out
	}
	return v
}

// renameKeys reads the old per-item key `label` as `name` on accounts,
// calendars and destinations. When an item has both, name wins and label is
// dropped. Saves write name only (marshalDomain encodes the typed values).
func renameKeys(key string, v *yaml.Node) *yaml.Node {
	switch key {
	case "accounts", "calendars", "destinations":
	default:
		return v
	}
	if v.Kind != yaml.SequenceNode {
		return v
	}
	out := *v
	out.Content = make([]*yaml.Node, len(v.Content))
	for i, item := range v.Content {
		out.Content[i] = renameItemKey(item, "label", "name")
	}
	return &out
}

// renameItemKey returns mapping node item with key from renamed to to, or
// with from dropped when to is already present.
func renameItemKey(item *yaml.Node, from, to string) *yaml.Node {
	if item.Kind != yaml.MappingNode {
		return item
	}
	hasTo := false
	for i := 0; i+1 < len(item.Content); i += 2 {
		if item.Content[i].Value == to {
			hasTo = true
		}
	}
	if hasTo {
		return withoutKeys(item, []string{from})
	}
	out := *item
	out.Content = slices.Clone(item.Content)
	for i := 0; i+1 < len(out.Content); i += 2 {
		if out.Content[i].Value == from {
			k := *out.Content[i]
			k.Value = to
			out.Content[i] = &k
		}
	}
	return &out
}

// withoutKeys returns a copy of mapping node v without the named keys; other
// nodes are returned as they are.
func withoutKeys(v *yaml.Node, drop []string) *yaml.Node {
	if v.Kind != yaml.MappingNode {
		return v
	}
	out := *v
	out.Content = nil
	for i := 0; i+1 < len(v.Content); i += 2 {
		if !slices.Contains(drop, v.Content[i].Value) {
			out.Content = append(out.Content, v.Content[i], v.Content[i+1])
		}
	}
	return &out
}

// loadFiles decodes the domain files over Defaults() in Domains order,
// without validating or applying the environment.
func loadFiles(files map[Domain][]byte) (*Config, error) {
	cfg := Defaults()
	for _, d := range Domains {
		if err := decodeDomain(d, files[d], cfg); err != nil {
			return nil, err
		}
	}
	cfg.Resolve()
	cfg.Warnings = cfg.DanglingRefs()
	return cfg, nil
}

var errLine = regexp.MustCompile(`^line (\d+):`)

// redactDecodeError rewrites a yaml decode error to name only the fields
// that failed. yaml messages can quote the offending value ("cannot
// unmarshal !!str `<v>` into int"), and that value can be a secret (an
// inline ics_url or password), while these errors end up in the startup
// output and the server log. src is the document that was decoded; a
// message's line number is mapped back to the key on that line.
func redactDecodeError(err error, src []byte) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return fmt.Errorf("invalid config (details not shown)")
	}
	var doc yaml.Node
	_ = yaml.Unmarshal(src, &doc)
	fields := make([]string, 0, len(te.Errors))
	for _, msg := range te.Errors {
		name := "?"
		if m := errLine.FindStringSubmatch(msg); m != nil {
			line, _ := strconv.Atoi(m[1])
			if p := pathAtLine(&doc, line, ""); p != "" {
				name = p
			} else {
				name = "line " + m[1]
			}
		}
		fields = append(fields, name)
	}
	return fmt.Errorf("invalid value, type or unknown key for %s (value not shown)", strings.Join(fields, ", "))
}

// pathAtLine returns the dotted key path of the deepest mapping key that
// starts on line, or "".
func pathAtLine(n *yaml.Node, line int, prefix string) string {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if p := pathAtLine(c, line, prefix); p != "" {
				return p
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			path := k.Value
			if prefix != "" {
				path = prefix + "." + k.Value
			}
			if p := pathAtLine(v, line, path); p != "" {
				return p
			}
			if k.Line == line {
				return path
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			if p := pathAtLine(c, line, fmt.Sprintf("%s[%d]", prefix, i)); p != "" {
				return p
			}
		}
	}
	return ""
}
