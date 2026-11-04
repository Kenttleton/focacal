package config

import (
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestApplyEnv(t *testing.T) {
	c := Defaults()
	if err := ApplyEnv(c, envMap(map[string]string{
		"FOCACAL_AGENT_BASE_URL":          "http://x/v1",
		"FOCACAL_AGENT_MODEL":             "m",
		"FOCACAL_AGENT_API_KEY":           "k",
		"FOCACAL_AGENT_TIMEOUT_SECS":      "7",
		"FOCACAL_SERVER_REST_PORT":        "7000",
		"FOCACAL_SERVER_BIND_ADDRESS":     "127.0.0.1",
		"FOCACAL_SERVER_WEBHOOKS_ENABLED": "true",
		"FOCACAL_AUTH_ENABLED":            "false",
		"OAUTH_REDIRECT_BASE_URL":         "https://cal.example",
	})); err != nil {
		t.Fatal(err)
	}
	if c.Agent.BaseURL != "http://x/v1" || c.Agent.Model != "m" || c.Agent.APIKey != "k" || c.Agent.TimeoutSecs != 7 {
		t.Errorf("agent: %+v", c.Agent)
	}
	if c.Server.RESTPort != 7000 || c.Server.BindAddress != "127.0.0.1" || !c.Server.WebhooksEnabled {
		t.Errorf("server: %+v", c.Server)
	}
	if c.Auth.Enabled || c.OAuth.RedirectBaseURL != "https://cal.example" {
		t.Errorf("auth/oauth: %+v %+v", c.Auth, c.OAuth)
	}
}

func TestApplyEnv_BadValueErrorsWithoutEchoing(t *testing.T) {
	c := Defaults()
	err := ApplyEnv(c, envMap(map[string]string{"FOCACAL_SERVER_REST_PORT": "SECRETINT", "FOCACAL_AUTH_ENABLED": "SECRETBOOL"}))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "FOCACAL_SERVER_REST_PORT") || !strings.Contains(msg, "FOCACAL_AUTH_ENABLED") {
		t.Errorf("error should name both variables: %v", err)
	}
	if strings.Contains(msg, "SECRET") {
		t.Errorf("error echoes a value: %v", err)
	}
	if c.Server.RESTPort != 5233 || !c.Auth.Enabled {
		t.Errorf("file values not kept: %+v %+v", c.Server, c.Auth)
	}
}

func TestLoadStartFailsOnBadEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FOCACAL_SERVER_REST_PORT", "SECRETINT")
	_, _, err := LoadStart(dir)
	if err == nil || strings.Contains(err.Error(), "SECRETINT") || !strings.Contains(err.Error(), "FOCACAL_SERVER_REST_PORT") {
		t.Errorf("err = %v", err)
	}
}

func TestApplyEnv_UnsetChangesNothing(t *testing.T) {
	c := Defaults()
	if err := ApplyEnv(c, envMap(nil)); err != nil {
		t.Fatal(err)
	}
	if c.Server.RESTPort != 5233 || c.Agent.Model != "qwen2.5:14b" {
		t.Errorf("%+v", c)
	}
}
