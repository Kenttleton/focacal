package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// WriteSections parses the YAML file at path, replaces or appends only the
// named top-level keys, and writes the file in place.
func WriteSections(path string, sections map[string]any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
		if doc.Content == nil {
			doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
	}
	if len(doc.Content) == 0 {
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.MappingNode})
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config %s: root is not a mapping", path)
	}

	for key, val := range sections {
		// marshal val to a node
		b, err := yaml.Marshal(val)
		if err != nil {
			return fmt.Errorf("marshal section %s: %w", key, err)
		}
		var node yaml.Node
		if err := yaml.Unmarshal(b, &node); err != nil {
			return fmt.Errorf("unmarshal section %s: %w", key, err)
		}
		// node is document node; take content[0]
		var sectionNode *yaml.Node
		if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
			sectionNode = node.Content[0]
		} else {
			sectionNode = &node
		}

		// find existing key
		found := -1
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				found = i
				break
			}
		}
		if found >= 0 {
			root.Content[found+1] = sectionNode
		} else {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
			root.Content = append(root.Content, keyNode, sectionNode)
		}
	}

	// write in place (truncate)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open config for writing %s: %w", path, err)
	}
	defer f.Close()
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode config %s: %w", path, err)
	}
	return enc.Close()
}
