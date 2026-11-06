package config

import "os"

// UnresolvedRef is an env-var reference whose variable is not set (or empty)
// and that has no inline fallback.
type UnresolvedRef struct {
	Kind string // "source"
	ID   string // source (calendar) id
	Env  string // the variable name (never its value)
}

// UnresolvedEnvRefs lists the ics_url_env references of ics sources whose variable is unset and that have no inline ics_url. Per
// R14 these are warnings, not validation errors: the config loads, the
// affected source fails at sync time with a clear error, and the rest keeps
// working. Callers log them at startup.
func (c *Config) UnresolvedEnvRefs() []UnresolvedRef {
	var out []UnresolvedRef
	unresolved := func(env, inline string) bool {
		return env != "" && inline == "" && os.Getenv(env) == ""
	}
	for _, s := range c.Sources {
		if s.Type == "ics" && unresolved(s.ICSURLEnv, s.ICSURL) {
			out = append(out, UnresolvedRef{Kind: "source", ID: s.ID, Env: s.ICSURLEnv})
		}
	}
	return out
}
