package rest

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Kenttleton/focacal/internal/auth/simple"
	"github.com/Kenttleton/focacal/internal/config"
)

const testKey = "static-test-key"

const cfgTestApp = `# app
server:
  rest_port: 5233
`

const cfgTestPlanner = `# planner settings
working_hours:
  start: "09:00"
  end: "17:00" # leave on time
  timezone: "America/New_York"
colors:
  focus: "#00aa00"
`

// The inline secrets here are legal in a hand-written file (spec 9.5) and
// must never come back out of the API.
const cfgTestCalendars = `# calendars
sources:
  - id: feed
    type: ics
    ics_url: https://secret.example/feed-SECRETTOKEN.ics
  - id: dav
    type: caldav
    caldav_url: https://dav.example
    username: me
    password: hunter2-SECRETPW
`

// newConfigTestServer builds a Server over a temp config dir holding the
// three domain files, with a config Service and the auth middleware applied.
func newConfigTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	return newConfigTestServerLog(t, io.Discard)
}

// newConfigTestServerLog is newConfigTestServer with the server log written
// to logOut.
func newConfigTestServerLog(t *testing.T, logOut io.Writer) (*Server, string) {
	t.Helper()
	t.Cleanup(simple.RevokeAll)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"config.yaml":    cfgTestApp,
		"planner.yaml":   cfgTestPlanner,
		"calendars.yaml": cfgTestCalendars,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	started, err := config.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	svc, err := config.NewService(dir, started)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(logOut, nil))
	s := New(nil, NewHealthState(), "127.0.0.1:0", log)
	s.SetConfigDir(dir)
	s.SetConfigService(svc)
	s.WrapHandler(simple.Middleware(config.AuthConfig{Enabled: true, APIKey: testKey}, nil, log))
	return s, dir
}

func doReq(t *testing.T, s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+testKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	return rec
}

type viewResp struct {
	Values         map[string]any      `json:"values"`
	Order          map[string][]string `json:"order"`
	PendingRestart bool                `json:"pending_restart"`
}

func decodeView(t *testing.T, rec *httptest.ResponseRecorder) viewResp {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var v viewResp
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode view: %v; body %s", err, rec.Body)
	}
	return v
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var e map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e["error"] == "" {
		t.Fatalf("want {\"error\": ...}, got %s (%v)", rec.Body, err)
	}
	return e["error"]
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGetConfigDomains(t *testing.T) {
	s, _ := newConfigTestServer(t)
	want := map[string][]string{
		"app":       {"server", "auth"},
		"planner":   {"working_hours", "lunch", "focus", "colors", "agent"},
		"calendars": {"sources", "destinations", "oauth"},
	}
	for domain, keys := range want {
		t.Run(domain, func(t *testing.T) {
			rec := doReq(t, s, http.MethodGet, "/config/"+domain, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /config/%s = %d %s", domain, rec.Code, rec.Body)
			}
			v := decodeView(t, rec)
			for _, k := range keys {
				if _, ok := v.Values[k]; !ok {
					t.Errorf("values missing %q: %v", k, v.Values)
				}
			}
			if v.PendingRestart {
				t.Errorf("pending_restart = true on an unchanged dir")
			}
		})
	}

	rec := doReq(t, s, http.MethodGet, "/config/planner", nil)
	wh := decodeView(t, rec).Values["working_hours"].(map[string]any)
	if wh["start"] != "09:00" || wh["timezone"] != "America/New_York" {
		t.Errorf("working_hours = %v", wh)
	}
}

func TestGetCalendarsHasNoSecrets(t *testing.T) {
	s, _ := newConfigTestServer(t)
	rec := doReq(t, s, http.MethodGet, "/config/calendars", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, secret := range []string{"SECRETTOKEN", "SECRETPW", `"password"`, `"ics_url"`} {
		if strings.Contains(body, secret) {
			t.Errorf("response contains %s: %s", secret, body)
		}
	}
	v := decodeView(t, rec)
	if got := v.Order["sources"]; len(got) != 2 || got[0] != "feed" || got[1] != "dav" {
		t.Errorf("order = %v", got)
	}
	for _, id := range []string{"feed", "dav"} {
		src := v.Values["sources"].(map[string]any)[id].(map[string]any)
		if src["has_secret"] != true {
			t.Errorf("%s has_secret = %v, want true", id, src["has_secret"])
		}
	}
}

func TestPatchPlannerValid(t *testing.T) {
	s, dir := newConfigTestServer(t)
	rec := doReq(t, s, http.MethodPatch, "/config/planner", []byte(`{"working_hours":{"start":"08:30"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body)
	}
	v := decodeView(t, rec)
	if !v.PendingRestart {
		t.Errorf("pending_restart = false, want true")
	}
	if wh := v.Values["working_hours"].(map[string]any); wh["start"] != "08:30" || wh["end"] != "17:00" {
		t.Errorf("working_hours = %v", wh)
	}
	got := readFile(t, filepath.Join(dir, "planner.yaml"))
	if !strings.Contains(got, `"08:30"`) && !strings.Contains(got, "08:30") {
		t.Errorf("planner.yaml not updated:\n%s", got)
	}
	if !strings.Contains(got, "# leave on time") {
		t.Errorf("comment lost:\n%s", got)
	}

	// GET now reflects the saved value.
	rec = doReq(t, s, http.MethodGet, "/config/planner", nil)
	if v := decodeView(t, rec); !v.PendingRestart {
		t.Errorf("GET after PATCH: pending_restart = false")
	}
}

func TestPatchPlannerInvalidReturns422AndFileUnchanged(t *testing.T) {
	s, dir := newConfigTestServer(t)
	before := readFile(t, filepath.Join(dir, "planner.yaml"))
	rec := doReq(t, s, http.MethodPatch, "/config/planner", []byte(`{"working_hours":{"start":"25:00"}}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH = %d %s, want 422", rec.Code, rec.Body)
	}
	if msg := errorBody(t, rec); !strings.Contains(msg, "working_hours.start") {
		t.Errorf("error %q should name the field", msg)
	}
	if after := readFile(t, filepath.Join(dir, "planner.yaml")); after != before {
		t.Errorf("planner.yaml changed:\n%s", after)
	}
}

func TestPatchInlineSecretReturns422WithoutEcho(t *testing.T) {
	s, dir := newConfigTestServer(t)
	before := readFile(t, filepath.Join(dir, "calendars.yaml"))
	rec := doReq(t, s, http.MethodPatch, "/config/calendars",
		[]byte(`{"sources":{"dav":{"password":"NEWSECRET-xyz"}}}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH = %d %s, want 422", rec.Code, rec.Body)
	}
	msg := errorBody(t, rec)
	for _, secret := range []string{"NEWSECRET", "SECRETPW", "SECRETTOKEN"} {
		if strings.Contains(msg, secret) {
			t.Errorf("error echoes a secret: %q", msg)
		}
	}
	if after := readFile(t, filepath.Join(dir, "calendars.yaml")); after != before {
		t.Errorf("calendars.yaml changed")
	}
}

func TestPatchMalformedJSONReturns400(t *testing.T) {
	s, dir := newConfigTestServer(t)
	before := readFile(t, filepath.Join(dir, "planner.yaml"))
	for name, body := range map[string]string{
		"malformed":    `{`,
		"array":        `[]`,
		"string":       `"x"`,
		"empty":        ``,
		"unknown key":  `{"bogus":1}`,
		"other domain": `{"sources":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := doReq(t, s, http.MethodPatch, "/config/planner", []byte(body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PATCH %s = %d %s, want 400", body, rec.Code, rec.Body)
			}
			errorBody(t, rec)
		})
	}
	if after := readFile(t, filepath.Join(dir, "planner.yaml")); after != before {
		t.Errorf("planner.yaml changed")
	}
}

func TestPatchOverLimitReturns413(t *testing.T) {
	s, dir := newConfigTestServer(t)
	before := readFile(t, filepath.Join(dir, "planner.yaml"))
	// A syntactically valid object just over 1 MiB.
	body := []byte(`{"colors":{"focus":"` + strings.Repeat("a", 1<<20) + `"}}`)
	rec := doReq(t, s, http.MethodPatch, "/config/planner", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PATCH = %d, want 413", rec.Code)
	}
	errorBody(t, rec)
	if after := readFile(t, filepath.Join(dir, "planner.yaml")); after != before {
		t.Errorf("planner.yaml changed")
	}
}

func TestPatchAtLimitIsRead(t *testing.T) {
	s, _ := newConfigTestServer(t)
	// Exactly 1 MiB, padded with whitespace: read fully, then rejected only
	// for its content (unknown key), not its size.
	prefix := `{"bogus":1}`
	body := []byte(prefix + strings.Repeat(" ", (1<<20)-len(prefix)))
	rec := doReq(t, s, http.MethodPatch, "/config/planner", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH = %d %s, want 400", rec.Code, rec.Body)
	}
}

func TestUnknownDomainReturns404(t *testing.T) {
	s, _ := newConfigTestServer(t)
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		var body []byte
		if m == http.MethodPatch {
			body = []byte(`{}`)
		}
		rec := doReq(t, s, m, "/config/bogus", body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s /config/bogus = %d, want 404", m, rec.Code)
			continue
		}
		errorBody(t, rec)
	}
}

func TestConfigRouteShapes(t *testing.T) {
	s, _ := newConfigTestServer(t)
	cases := []struct {
		method, path string
		want         int
	}{
		// No StripSlashes on this router: a trailing slash is a different path.
		{http.MethodGet, "/config/planner/", http.StatusNotFound},
		{http.MethodGet, "/config/planner/extra", http.StatusNotFound},
		{http.MethodGet, "/config/PLANNER", http.StatusNotFound},
		{http.MethodHead, "/config/planner", http.StatusMethodNotAllowed},
		{http.MethodPut, "/config/planner", http.StatusMethodNotAllowed},
		{http.MethodPost, "/config/planner", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/config/planner", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		var body []byte
		if tc.method == http.MethodPut || tc.method == http.MethodPost {
			body = []byte(`{}`)
		}
		rec := doReq(t, s, tc.method, tc.path, body)
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestOldConfigRoutesRemoved(t *testing.T) {
	s, _ := newConfigTestServer(t)
	cases := []struct{ method, path string }{
		{http.MethodGet, "/config"},
		{http.MethodPatch, "/config"},
		{http.MethodPut, "/config/sources"},
		{http.MethodPut, "/config/destinations"},
		{http.MethodGet, "/config/sources"},
		{http.MethodGet, "/sources"},
		{http.MethodGet, "/destinations"},
	}
	for _, tc := range cases {
		var body []byte
		if tc.method != http.MethodGet {
			body = []byte(`[]`)
		}
		rec := doReq(t, s, tc.method, tc.path, body)
		// /config/sources now matches /config/{domain} (an unknown domain);
		// PUT there is 405. Every other old route is gone entirely.
		want := http.StatusNotFound
		if tc.method == http.MethodPut && strings.HasPrefix(tc.path, "/config/") {
			want = http.StatusMethodNotAllowed
		}
		if rec.Code != want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, want)
		}
	}
}

func TestConfigRequiresAuth(t *testing.T) {
	s, dir := newConfigTestServer(t)
	before := readFile(t, filepath.Join(dir, "planner.yaml"))
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		req := httptest.NewRequest(m, "/config/planner", strings.NewReader(`{"working_hours":{"start":"08:00"}}`))
		rec := httptest.NewRecorder()
		s.srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token = %d, want 401", m, rec.Code)
		}
		req = httptest.NewRequest(m, "/config/planner", strings.NewReader(`{"working_hours":{"start":"08:00"}}`))
		req.Header.Set("Authorization", "Bearer wrong")
		rec = httptest.NewRecorder()
		s.srv.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with bad token = %d, want 401", m, rec.Code)
		}
	}
	if after := readFile(t, filepath.Join(dir, "planner.yaml")); after != before {
		t.Errorf("planner.yaml changed by an unauthenticated PATCH")
	}
}

func TestConfigWithoutServiceReturns503(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(nil, NewHealthState(), "127.0.0.1:0", log)
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		rec := doReq(t, s, m, "/config/planner", []byte(`{}`))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503", m, rec.Code)
			continue
		}
		errorBody(t, rec)
	}
}

func TestConfigFileErrorReturns500WithoutContents(t *testing.T) {
	// Hand-edited calendars.yaml files holding a secret that no longer load:
	// one does not parse as YAML, one parses but does not decode (a decoder
	// error may quote the offending value).
	for name, broken := range map[string]string{
		"parse":  "sources:\n  - id: feed\n    ics_url: https://x/LEAKME.ics\n   bad: [\n",
		"decode": "sources:\n  - id: feed\n    type: ics\n    ics_url: [https://x/LEAKME.ics]\n",
	} {
		t.Run(name, func(t *testing.T) {
			var logBuf syncBuffer
			s, dir := newConfigTestServerLog(t, &logBuf)
			if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(broken), 0o600); err != nil {
				t.Fatal(err)
			}
			before := readFile(t, filepath.Join(dir, "planner.yaml"))
			for _, tc := range []struct {
				method, path string
				body         []byte
			}{
				{http.MethodGet, "/config/calendars", nil},
				{http.MethodGet, "/config/planner", nil},
				{http.MethodPatch, "/config/planner", []byte(`{"working_hours":{"start":"08:00"}}`)},
				{http.MethodPatch, "/config/calendars", []byte(`{"sources":{"feed":{"label":"x"}}}`)},
			} {
				rec := doReq(t, s, tc.method, tc.path, tc.body)
				if rec.Code != http.StatusInternalServerError {
					t.Errorf("%s %s = %d %s, want 500", tc.method, tc.path, rec.Code, rec.Body)
					continue
				}
				msg := errorBody(t, rec)
				for _, leak := range []string{"LEAKME", "calendars.yaml", dir, "yaml:"} {
					if strings.Contains(msg, leak) {
						t.Errorf("%s %s error leaks %q: %q", tc.method, tc.path, leak, msg)
					}
				}
			}
			if after := readFile(t, filepath.Join(dir, "planner.yaml")); after != before {
				t.Errorf("planner.yaml changed")
			}
			logged := logBuf.String()
			if !strings.Contains(logged, "config domain request failed") {
				t.Errorf("failure not logged:\n%s", logged)
			}
			if strings.Contains(logged, "LEAKME") {
				t.Errorf("server log quotes the secret:\n%s", logged)
			}
		})
	}
}

func TestDisplayTimezoneRemoved(t *testing.T) {
	s, _ := newConfigTestServer(t)
	rec := doReq(t, s, http.MethodPatch, "/config/planner", []byte(`{"display_timezone":"UTC"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH display_timezone = %d, want 400", rec.Code)
	}
	if bytes.Contains(uiHTML, []byte("display_timezone")) {
		t.Errorf("ui.html still references display_timezone")
	}
}

func TestUIUsesPlannerDomain(t *testing.T) {
	for _, old := range []string{"fetch('/config')", "fetch('/config',", "'/sources'", "'/destinations'"} {
		if bytes.Contains(uiHTML, []byte(old)) {
			t.Errorf("ui.html still calls %s", old)
		}
	}
	if !bytes.Contains(uiHTML, []byte("/config/planner")) {
		t.Errorf("ui.html does not call /config/planner")
	}
}

func TestOpenAPIDocumentsConfigDomain(t *testing.T) {
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openapiJSON, &spec); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	p, ok := spec.Paths["/config/{domain}"]
	if !ok {
		t.Fatal("openapi.json lacks /config/{domain}")
	}
	for _, m := range []string{"get", "patch"} {
		if _, ok := p[m]; !ok {
			t.Errorf("/config/{domain} lacks %s", m)
		}
	}
	for _, old := range []string{"/config", "/sources", "/destinations", "/config/sources", "/config/destinations"} {
		if _, ok := spec.Paths[old]; ok {
			t.Errorf("openapi.json still documents %s", old)
		}
	}
	if _, ok := spec.Components.Schemas["ConfigView"]; !ok {
		t.Error("openapi.json lacks the ConfigView schema")
	}
	var patch struct {
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(p["patch"], &patch); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"200", "400", "404", "413", "422", "500"} {
		if _, ok := patch.Responses[code]; !ok {
			t.Errorf("PATCH /config/{domain} lacks response %s", code)
		}
	}
}

func TestPatchAcceptsMergePatchContentType(t *testing.T) {
	s, _ := newConfigTestServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/config/planner", strings.NewReader(`{"focus":{"style":"pomodoro"}}`))
	req.Header.Set("Authorization", "Bearer "+testKey)
	req.Header.Set("Content-Type", "application/merge-patch+json")
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body)
	}
	if f := decodeView(t, rec).Values["focus"].(map[string]any); f["style"] != "pomodoro" {
		t.Errorf("focus = %v", f)
	}
}

// syncBuffer is a bytes.Buffer safe for the logger and the test to share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestPatchEmptyMapKeyReturns400(t *testing.T) {
	s, dir := newConfigTestServer(t)
	before := readFile(t, filepath.Join(dir, "planner.yaml"))
	rec := doReq(t, s, http.MethodPatch, "/config/planner", []byte(`{"colors":{"":"#ffffff"}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH = %d %s, want 400", rec.Code, rec.Body)
	}
	errorBody(t, rec)
	if after := readFile(t, filepath.Join(dir, "planner.yaml")); after != before {
		t.Errorf("planner.yaml changed")
	}
}

func TestPatchChunkedOverLimitReturns413(t *testing.T) {
	s, dir := newConfigTestServer(t)
	before := readFile(t, filepath.Join(dir, "planner.yaml"))
	body := `{"colors":{"focus":"` + strings.Repeat("a", 1<<20) + `"}}`
	// An io.Reader that is not a *bytes.Reader/*strings.Reader leaves
	// ContentLength unknown (-1), as with Transfer-Encoding: chunked.
	req := httptest.NewRequest(http.MethodPatch, "/config/planner", io.MultiReader(strings.NewReader(body)))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+testKey)
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PATCH = %d, want 413", rec.Code)
	}
	errorBody(t, rec)
	if after := readFile(t, filepath.Join(dir, "planner.yaml")); after != before {
		t.Errorf("planner.yaml changed")
	}
}

func TestOpenAPIDocuments503(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openapiJSON, &spec); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"get", "patch"} {
		var op struct {
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(spec.Paths["/config/{domain}"][m], &op); err != nil {
			t.Fatal(err)
		}
		if _, ok := op.Responses["503"]; !ok {
			t.Errorf("%s /config/{domain} lacks response 503", m)
		}
	}
}
