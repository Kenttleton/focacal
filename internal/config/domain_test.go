package config

import "testing"

func TestDomainForKey_AgentAndOAuth(t *testing.T) {
	want := map[string]Domain{
		"agent":  DomainPlanner,
		"oauth":  DomainCalendars,
		"server": DomainApp,
		"auth":   DomainApp,
	}
	for k, d := range want {
		got, ok := DomainForKey(k)
		if !ok || got != d {
			t.Errorf("DomainForKey(%q) = %v, %v; want %v, true", k, got, ok, d)
		}
	}
	var appKeys []string
	for _, dk := range domainKeys[DomainApp] {
		appKeys = append(appKeys, dk.name)
	}
	if len(appKeys) != 2 || appKeys[0] != "server" || appKeys[1] != "auth" {
		t.Errorf("config.yaml keys = %v, want [server auth]", appKeys)
	}
}
