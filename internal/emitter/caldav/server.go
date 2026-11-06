package caldav

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/destination"
	"github.com/Kenttleton/focacal/internal/store"
)

// Server serves a CalDAV endpoint: one subscribable feed per served
// destination, each the projection of the store through that destination's
// inputs and visibility.
type Server struct {
	addr string
	srv  *http.Server
	live *config.Live
	log  *slog.Logger
}

// New creates a CalDAV server bound to addr (e.g. "0.0.0.0:5232"). The served
// destinations are read from live on every request, so edits apply without a
// restart; only addr needs one.
func New(addr string, live *config.Live, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Server{addr: addr, live: live, log: log}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/caldav", s.handleRedirect)
	mux.HandleFunc("/calendar/", s.handleCalDAV)
	// Aliases for the default destination, kept for existing subscriptions.
	mux.HandleFunc("/focacal/", s.handleCalDAV)
	mux.HandleFunc("/focacal/calendar/", s.handleCalDAV)

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return s
}

// WrapHandler wraps the server's HTTP handler with the given middleware.
// Call before Start() — for example to apply auth middleware.
func (s *Server) WrapHandler(mw func(http.Handler) http.Handler) {
	s.srv.Handler = mw(s.srv.Handler)
}

// Name implements emitter.Emitter.
func (s *Server) Name() string { return "caldav" }

// Start begins listening. Blocks until the context is cancelled.
// ctx must carry the injected Store so that all HTTP request contexts inherit
// it via BaseContext.
func (s *Server) Start(ctx context.Context) error {
	s.srv.BaseContext = func(net.Listener) context.Context {
		return context.WithoutCancel(ctx)
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("caldav listen %s: %w", s.addr, err)
	}
	s.log.Info("CalDAV server listening", "addr", s.addr)

	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// Notify implements emitter.Emitter — for CalDAV, the calendar is always
// served live from the store, so no explicit push is needed.
func (s *Server) Notify(_ context.Context) error { return nil }

// handleRedirect sends CalDAV clients to the calendar home.
func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/calendar/", http.StatusMovedPermanently)
}

// served returns the destinations that publish a feed, in file order.
func (s *Server) served() []config.DestinationConfig {
	if s.live == nil {
		return nil
	}
	var out []config.DestinationConfig
	for _, d := range s.live.Current().Destinations {
		if d.Target.Served {
			out = append(out, d)
		}
	}
	return out
}

// feedFor resolves a request path to a served destination. listing is true
// for the /calendar/ home, which lists them all.
func (s *Server) feedFor(path string) (d config.DestinationConfig, listing, ok bool) {
	var id string
	switch path {
	case "/calendar", "/calendar/":
		return d, true, true
	case "/focacal", "/focacal/", "/focacal/calendar", "/focacal/calendar/":
		id = config.FocacalSource
	default:
		rest, found := strings.CutPrefix(path, "/calendar/")
		if !found {
			return d, false, false
		}
		id = strings.TrimSuffix(rest, "/")
		if id == "" || strings.Contains(id, "/") {
			return d, false, false
		}
	}
	for _, c := range s.served() {
		if c.ID == id {
			return c, false, true
		}
	}
	return d, false, false
}

func feedName(d config.DestinationConfig) string {
	if d.Name != "" {
		return d.Name
	}
	return d.ID
}

// handleCalDAV handles all CalDAV PROPFIND / REPORT / GET requests.
func (s *Server) handleCalDAV(w http.ResponseWriter, r *http.Request) {
	d, listing, ok := s.feedFor(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case "PROPFIND":
		if listing {
			s.propfindList(w)
			return
		}
		s.propfindFeed(w, r, d)
	case "REPORT", http.MethodGet:
		if listing {
			s.getList(w)
			return
		}
		s.serveICS(w, r, d)
	case http.MethodOptions:
		s.handleOptions(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleOptions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "OPTIONS, GET, PROPFIND, REPORT")
	w.Header().Set("DAV", "1, 2, calendar-access")
	w.WriteHeader(http.StatusOK)
}

func feedHref(d config.DestinationConfig) string { return "/calendar/" + d.ID + "/" }

func writeFeedResponse(b *strings.Builder, d config.DestinationConfig, ctag string) {
	name := html.EscapeString(feedName(d))
	fmt.Fprintf(b, `  <response>
    <href>%s</href>
    <propstat>
      <prop>
        <displayname>%s</displayname>
        <resourcetype><collection/><C:calendar/></resourcetype>
        <C:supported-calendar-component-set>
          <C:comp name="VEVENT"/>
        </C:supported-calendar-component-set>
        <getcontenttype>text/calendar; charset=utf-8</getcontenttype>
`, html.EscapeString(feedHref(d)), name)
	if ctag != "" {
		fmt.Fprintf(b, "        <CS:getctag>%s</CS:getctag>\n", ctag)
	}
	b.WriteString(`      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
`)
}

const multistatusOpen = `<?xml version="1.0" encoding="UTF-8"?>
<multistatus xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:CS="http://calendarserver.org/ns/">
`

// propfindList lists every served destination as a calendar collection.
func (s *Server) propfindList(w http.ResponseWriter) {
	var b strings.Builder
	b.WriteString(multistatusOpen)
	for _, d := range s.served() {
		writeFeedResponse(&b, d, "")
	}
	b.WriteString("</multistatus>")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, b.String())
}

// getList is the browsable form of the calendar home.
func (s *Server) getList(w http.ResponseWriter) {
	var b strings.Builder
	for _, d := range s.served() {
		fmt.Fprintf(&b, "%s\t%s\n", feedName(d), feedHref(d))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, b.String())
}

// propfindFeed answers discovery for one feed, with a ctag that changes when
// the projected output does.
func (s *Server) propfindFeed(w http.ResponseWriter, r *http.Request, d config.DestinationConfig) {
	events, blocks, ok := s.project(w, r, d)
	if !ok {
		return
	}
	var b strings.Builder
	b.WriteString(multistatusOpen)
	writeFeedResponse(&b, d, ctag(events, blocks))
	b.WriteString("</multistatus>")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	io.WriteString(w, b.String())
}

// ctag hashes everything a feed publishes.
func ctag(events []*store.Event, blocks []*store.Block) string {
	h := sha256.New()
	for _, e := range events {
		fmt.Fprintf(h, "e|%s|%s|%s|%s|%d|%d|%t|%s|%s|%d\n", e.ID, e.SourceID, e.Title, e.Description,
			e.Start.UnixNano(), e.End.UnixNano(), e.AllDay, e.Status, e.Location, e.SyncedAt.UnixNano())
	}
	for _, b := range blocks {
		fmt.Fprintf(h, "b|%s|%s|%s|%d|%d|%d\n", b.ID, b.Type, b.Title, b.Start.UnixNano(), b.End.UnixNano(), b.UpdatedAt.UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// project reads the feed window from the store and projects it through d. On
// failure it has already answered the request.
func (s *Server) project(w http.ResponseWriter, r *http.Request, d config.DestinationConfig) ([]*store.Event, []*store.Block, bool) {
	ctx := r.Context()
	now := time.Now()
	from := now.AddDate(0, 0, -7)
	to := now.AddDate(0, 0, 14)

	db := store.FromContext(ctx)
	events, err := db.ListEvents(ctx, from, to)
	if err != nil {
		s.log.Error("list events for ICS", "destination", d.ID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, nil, false
	}
	blocks, err := db.ListBlocks(ctx, from, to)
	if err != nil {
		s.log.Error("list blocks for ICS", "destination", d.ID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, nil, false
	}
	events, blocks = destination.Project(s.live.Current(), d, events, blocks)
	return events, blocks, true
}

// serveICS builds and serves one destination's calendar.
func (s *Server) serveICS(w http.ResponseWriter, r *http.Request, d config.DestinationConfig) {
	events, blocks, ok := s.project(w, r, d)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.ID+".ics"))
	fmt.Fprint(w, BuildICSNamed(feedName(d), events, blocks))
}
