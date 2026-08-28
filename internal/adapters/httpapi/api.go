package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// api holds what every handler needs: the use cases and a logger. It knows
// nothing about which Store or Provider is behind them.
type api struct {
	svc *app.Service
	log *slog.Logger
}

// New builds the REST handler over the use cases. Routing is the standard
// library's own pattern mux — method and wildcards in the pattern, nothing
// else — so there is no router to configure and no framework to learn.
//
// A nil logger falls back to slog.Default().
func New(svc *app.Service, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	a := &api{svc: svc, log: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /providers", a.listProviders)
	mux.HandleFunc("POST /backfills", a.startBackfill)
	mux.HandleFunc("GET /backfills/{id}", a.showBackfill)
	mux.HandleFunc("DELETE /backfills/{id}", a.cancelBackfill)
	mux.HandleFunc("GET /datasets/{provider}/{symbol}/{timeframe}/coverage", a.showCoverage)
	mux.HandleFunc("GET /datasets/{provider}/{symbol}/{timeframe}/complete", a.showCompleteness)
	mux.HandleFunc("GET /datasets/{provider}/{symbol}/{timeframe}/gaps", a.listGaps)
	mux.HandleFunc("GET /datasets/{provider}/{symbol}/{timeframe}/bars", a.showBars)
	mux.HandleFunc("PATCH /gaps/{id}", a.patchGap)
	mux.HandleFunc("POST /gaps/{id}/repair", a.repairGap)

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

const (
	contentTypeJSON    = "application/json"
	contentTypeParquet = "application/vnd.apache.parquet"
)

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

// statusFor is the whole error contract of this API: which sentinel means
// which status.
func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrBackfillRunning):
		return http.StatusConflict
	case errors.Is(err, domain.ErrUnknownSymbol),
		errors.Is(err, domain.ErrUnsupportedTimeframe),
		errors.Is(err, app.ErrUnknownProvider),
		errors.Is(err, app.ErrGapStatusNotSettable):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// decodeJSON reads the request body into v, reporting a plain error the
// caller turns into a 400.
func decodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

// datasetFromPath reads the Dataset the URL names. Only the Timeframe can be
// wrong here: a Provider or Symbol nothing was ever acquired for is an empty
// Dataset, not a bad request.
func datasetFromPath(r *http.Request) (domain.DatasetID, error) {
	tf, err := domain.ParseTimeframe(r.PathValue("timeframe"))
	if err != nil {
		return domain.DatasetID{}, err
	}
	return domain.DatasetID{
		Provider:  r.PathValue("provider"),
		Symbol:    domain.Symbol(r.PathValue("symbol")),
		Timeframe: tf,
	}, nil
}

// parseTime reads one bound: an RFC3339 instant, or a YYYY-MM-DD date read as
// midnight UTC.
func parseTime(field, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%s %q is neither an RFC3339 instant nor a YYYY-MM-DD date", field, v)
}

// parseRange reads a half-open range from its two bounds. An empty range is
// refused: every caller of this asks a question about instants, and there are
// none in [t, t).
func parseRange(start, end string) (domain.Range, error) {
	from, err := parseTime("start", start)
	if err != nil {
		return domain.Range{}, err
	}
	to, err := parseTime("end", end)
	if err != nil {
		return domain.Range{}, err
	}
	r := domain.Range{Start: from, End: to}
	if r.IsEmpty() {
		return domain.Range{}, fmt.Errorf("start %s must be before end %s", asTime(from), asTime(to))
	}
	return r, nil
}

// rangeFromQuery reads the ?start&end pair.
func rangeFromQuery(r *http.Request) (domain.Range, error) {
	q := r.URL.Query()
	return parseRange(q.Get("start"), q.Get("end"))
}

// gapIDFromPath reads the {id} of a Gap route.
func gapIDFromPath(r *http.Request) (int64, error) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("gap id %q is not a number", raw)
	}
	return id, nil
}
