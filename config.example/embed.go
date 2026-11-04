// Package configexample embeds the sample config files so the binary can write
// any that are missing on first start (config.SeedDefaults). The same files
// are the repo's documented examples, so there is one copy to keep current.
package configexample

import "embed"

// FS holds config.yaml, planner.yaml and calendars.yaml.
//
//go:embed config.yaml planner.yaml calendars.yaml
var FS embed.FS
