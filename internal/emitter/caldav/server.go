package caldav

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/you/focacal/internal/store"
)

// Server serves a CalDAV endpoint that merges real events and AI-planned blocks
// into a single subscribable calendar feed.
type Server struct {
	addr string
	srv  *http.Server
	log  *slog.Logger
}

// New creates a CalDAV server bound to addr (e.g. "0.0.0.0:5232").
func New(addr string, log *slog.Logger) *Server {
	s := &Server{addr: addr, log: log}
	mux := http.NewServeMux()

	// CalDAV discovery and principal endpoints.
	mux.HandleFunc("/.well-known/caldav", s.handleRedirect)
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

// handleRedirect sends CalDAV clients to the principal URL.
func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/focacal/", http.StatusMovedPermanently)
}

// handleCalDAV handles all CalDAV PROPFIND / REPORT / GET requests.
func (s *Server) handleCalDAV(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "PROPFIND":
		s.handlePropfind(w, r)
	case "REPORT":
		s.handleReport(w, r)
	case http.MethodGet:
		s.handleGet(w, r)
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

// handlePropfind responds to CalDAV PROPFIND discovery requests.
func (s *Server) handlePropfind(w http.ResponseWriter, r *http.Request) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "1"
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus) // 207

	// Minimal PROPFIND multistatus response for calendar collection discovery.
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<multistatus xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <response>
    <href>/focacal/calendar/</href>
    <propstat>
      <prop>
        <displayname>Focacal</displayname>
        <resourcetype><collection/><C:calendar/></resourcetype>
        <C:supported-calendar-component-set>
          <C:comp name="VEVENT"/>
        </C:supported-calendar-component-set>
        <getcontenttype>text/calendar; charset=utf-8</getcontenttype>
      </prop>
      <status>HTTP/1.1 200 OK</status>
    </propstat>
  </response>
</multistatus>`)
}

// handleReport responds to CalDAV REPORT (calendar-query, calendar-multiget).
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	// For the initial scaffold we serve the ICS feed on any REPORT.
	// A production implementation would parse the XML request body
	// and honour time-range filters from the client.
	s.serveICS(w, r)
}

// handleGet serves the merged ICS feed for clients that poll via HTTP GET
// (e.g. Google Calendar's "From URL" subscription).
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	s.serveICS(w, r)
}

// serveICS builds and serves the merged ICS calendar.
func (s *Server) serveICS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	from := now.AddDate(0, 0, -7)
	to := now.AddDate(0, 0, 14)

	db := store.FromContext(ctx)
	events, err := db.ListEvents(ctx, from, to)
	if err != nil {
		s.log.Error("list events for ICS", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	blocks, err := db.ListBlocks(ctx, from, to)
	if err != nil {
		s.log.Error("list blocks for ICS", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ics := BuildICS(events, blocks)
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="focacal.ics"`)
	fmt.Fprint(w, ics)
}
