package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// api holds what every handler needs: the composite use cases and a logger.
// It knows nothing about which Store is behind them.
type api struct {
	svc *app.Service
	log *slog.Logger
}

// New builds the REST handler over the composite use cases. Routing is the
// standard library's own pattern mux — method and wildcards in the pattern,
// nothing else — exactly as the acquisition adapter routes.
//
// Every pattern is absolute, so this handler can be mounted at "/composites"
// and "/composites/" on the service's own mux and still see the paths it
// registered.
//
// A nil logger falls back to slog.Default().
func New(svc *app.Service, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	a := &api{svc: svc, log: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /composites", a.createComposite)
	mux.HandleFunc("GET /composites", a.listComposites)
	mux.HandleFunc("GET /composites/{name}", a.showComposite)
	mux.HandleFunc("PUT /composites/{name}", a.editComposite)
	mux.HandleFunc("DELETE /composites/{name}", a.deleteComposite)
	mux.HandleFunc("POST /composites/{name}/build", a.buildComposite)

	return a.observe(mux)
}

// observe logs one line per request and makes sure the mux's own plain-text
// 404 and 405 come back as the JSON {error} every other response uses.
func (a *api) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		a.log.Info("request",
			"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(started))
	})
}

// recorder remembers the status for the log line, and rewrites the two
// responses this adapter does not write itself — the mux's 404 for an unknown
// route and its 405 for a known route at the wrong method — into the JSON
// error body. A handler's own 404 already carries a JSON content type and is
// passed through untouched.
type recorder struct {
	http.ResponseWriter
	status  int
	wrote   bool
	replace bool
}

func (rec *recorder) WriteHeader(code int) {
	if rec.wrote {
		return
	}
	rec.wrote, rec.status = true, code
	fromMux := (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) &&
		rec.Header().Get("Content-Type") != contentTypeJSON
	if !fromMux {
		rec.ResponseWriter.WriteHeader(code)
		return
	}
	rec.replace = true
	message := "not found"
	if code == http.StatusMethodNotAllowed {
		message = "method not allowed"
	}
	body, _ := json.Marshal(errorJSON{Error: message})
	rec.Header().Set("Content-Type", contentTypeJSON)
	rec.Header().Del("Content-Length")
	rec.ResponseWriter.WriteHeader(code)
	rec.ResponseWriter.Write(append(body, '\n'))
}

func (rec *recorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	if rec.replace {
		// The mux's plain-text body has already been replaced.
		return len(b), nil
	}
	return rec.ResponseWriter.Write(b)
}

const contentTypeJSON = "application/json"

// writeJSON sends v as the whole body at the given status.
func (a *api) writeJSON(w http.ResponseWriter, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		a.log.Error("encoding response", "err", err)
		a.writeError(w, http.StatusInternalServerError, errors.New("encoding response"))
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(code)
	w.Write(append(body, '\n'))
}

// writeError sends {"error": …} at the given status.
func (a *api) writeError(w http.ResponseWriter, code int, err error) {
	body, _ := json.Marshal(errorJSON{Error: err.Error()})
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(code)
	w.Write(append(body, '\n'))
}

// fail maps a use-case failure onto its status and reports it. Anything the
// use cases do not name is the service's own fault, so it is a 500 and it is
// logged.
func (a *api) fail(w http.ResponseWriter, err error) {
	code := statusFor(err)
	if code == http.StatusInternalServerError {
		a.log.Error("request failed", "err", err)
	}
	a.writeError(w, code, err)
}

// statusFor is the whole error contract of this resource: which sentinel
// means which status.
func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrDuplicateName),
		errors.Is(err, domain.ErrBuildRunning),
		errors.Is(err, domain.ErrNotReady):
		return http.StatusConflict
	case errors.Is(err, domain.ErrInvalidConfig):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// decodeJSON reads the request body into v, reporting a bad-request error.
func decodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("%w: invalid JSON body: %s", domain.ErrInvalidConfig, err)
	}
	return nil
}

// nameFromPath reads the {name} a route addresses. A name that is not a slug
// names no Composite Dataset that could exist, so it is a bad request.
func nameFromPath(r *http.Request) (domain.Name, error) {
	return domain.ParseName(r.PathValue("name"))
}
