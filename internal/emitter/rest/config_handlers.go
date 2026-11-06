package rest

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Kenttleton/focacal/internal/config"
)

// maxConfigBody caps a PATCH /config/{domain} request body.
const maxConfigBody = 1 << 20 // 1 MiB

// handleGetConfigDomain serves GET /config/{domain}: the domain's saved
// values (secrets omitted; sources carry has_secret), collection order and
// pending_restart.
func (s *Server) handleGetConfigDomain(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	d := config.Domain(chi.URLParam(r, "domain"))
	view, err := s.configSvc.Get(d)
	if err != nil {
		s.writeConfigError(w, r, d, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handlePatchConfigDomain serves PATCH /config/{domain}: an RFC 7396 merge
// patch (a JSON object) applied to the domain's file. The file is written only
// if the patched domain validates.
func (s *Server) handlePatchConfigDomain(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	d := config.Domain(chi.URLParam(r, "domain"))
	if !d.Valid() {
		s.writeConfigError(w, r, d, config.ErrUnknownDomain)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
			return
		}
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	var opts config.PatchOptions
	if q := r.URL.Query().Get("dry_run"); q != "" {
		dry, err := strconv.ParseBool(q)
		if err != nil {
			writeError(w, http.StatusBadRequest, "dry_run must be true or false")
			return
		}
		opts.DryRun = dry
	}
	view, err := s.configSvc.Patch(d, body, opts)
	if err != nil {
		s.writeConfigError(w, r, d, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleCreateConfigItem serves POST /config/calendars/{collection}: it adds
// one account, calendar or destination. The body has no id; the server
// derives it from name and returns {"id", "view"} with 201.
func (s *Server) handleCreateConfigItem(w http.ResponseWriter, r *http.Request) {
	if s.configSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "config service unavailable")
		return
	}
	coll := chi.URLParam(r, "collection")
	switch coll {
	case "accounts", "calendars", "destinations":
	default:
		writeError(w, http.StatusNotFound, "unknown collection; use accounts, calendars or destinations")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigBody))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
			return
		}
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	id, view, err := s.configSvc.Create(coll, body)
	if err != nil {
		s.writeConfigError(w, r, config.DomainCalendars, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		ID   string      `json:"id"`
		View config.View `json:"view"`
	}{id, view})
}

// writeConfigError maps Service errors to status codes. Bad-request and
// validation messages are built from field paths, keys and non-secret values
// by the Service and are returned as is. Anything else comes from reading or
// parsing the files on disk: it is logged (config redacts decoded values from
// such errors) and the client gets a generic message without paths or
// contents.
func (s *Server) writeConfigError(w http.ResponseWriter, r *http.Request, d config.Domain, err error) {
	switch {
	case errors.Is(err, config.ErrUnknownDomain):
		writeError(w, http.StatusNotFound, "unknown config domain; use app, planner or calendars")
	case errors.Is(err, config.ErrBadRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, config.ErrValidation):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.log.Error("config domain request failed",
			"domain", string(d), "method", r.Method, "request_id", middleware.GetReqID(r.Context()), "err", err)
		writeError(w, http.StatusInternalServerError,
			"could not read or write the "+string(d)+" config; see the server log")
	}
}
