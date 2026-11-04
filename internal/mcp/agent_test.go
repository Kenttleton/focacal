package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Kenttleton/focacal/internal/config"
)

func writeAgentCfg(t *testing.T, dir, baseURL, model, key, style string) {
	t.Helper()
	t.Setenv("FOCACAL_AGENT_API_KEY", key) // api_key is env-only
	app := "server:\n  mcp_port: 5234\n"
	planner := fmt.Sprintf("agent:\n  base_url: %q\n  model: %q\nworking_hours:\n  start: \"09:00\"\n  end: \"17:00\"\n  timezone: \"UTC\"\nfocus:\n  style: %s\n  min_block_minutes: 30\n  max_block_minutes: 90\n", baseURL, model, style)
	for name, body := range map[string]string{"config.yaml": app, "planner.yaml": planner, "calendars.yaml": "sources: []\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

type seenReq struct{ path, model, auth, system string }

func chatServer(t *testing.T) (*httptest.Server, func() []seenReq) {
	t.Helper()
	var mu sync.Mutex
	var seen []seenReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role, Content string
			} `json:"messages"`
		}
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		seen = append(seen, seenReq{r.URL.Path, body.Model, r.Header.Get("Authorization"), body.Messages[0].Content})
		mu.Unlock()
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []seenReq { mu.Lock(); defer mu.Unlock(); return append([]seenReq(nil), seen...) }
}

func TestAgentUsesCurrentPlannerConfig(t *testing.T) {
	a, aGot := chatServer(t)
	b, bGot := chatServer(t)
	dir := t.TempDir()
	writeAgentCfg(t, dir, a.URL+"/v1", "model-one", "key-one", "flexible")
	live, err := config.NewLive(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ag := NewAgent(live, 5234, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := ag.Replan(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	writeAgentCfg(t, dir, b.URL+"/v1", "model-two", "key-two", "fixed")
	if _, err := live.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := ag.Replan(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	first, second := aGot(), bGot()
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("requests: first server %d, second server %d, want 1 each", len(first), len(second))
	}
	if first[0].model != "model-one" || first[0].auth != "Bearer key-one" || first[0].path != "/v1/chat/completions" {
		t.Errorf("first replan = %+v", first[0])
	}
	if second[0].model != "model-two" || second[0].auth != "Bearer key-two" {
		t.Errorf("second replan = %+v", second[0])
	}
	if !strings.Contains(second[0].system, "Fixed mode") || strings.Contains(first[0].system, "Fixed mode") {
		t.Errorf("focus style not followed in system prompt")
	}
}
