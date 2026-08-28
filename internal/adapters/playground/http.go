package playground

import (
	"encoding/json"
	"net/http"
	"time"
)

// contentTypeJSON is the only content type this adapter writes, the same one
// the domain routes answer with.
const contentTypeJSON = "application/json"

// traceJSON is one trace on the wire: its id and its spans, ordered by start
// time.
type traceJSON struct {
	TraceID string     `json:"trace_id"`
	Spans   []spanJSON `json:"spans"`
}

// spanJSON is one span on the wire. ParentID is null for a root — a root has
// no parent, and "" would be a span id that does not exist. End is null while
// the span is still running, which is the whole reason OnStart records at all.
type spanJSON struct {
	SpanID        string         `json:"span_id"`
	ParentID      *string        `json:"parent_id"`
	Name          string         `json:"name"`
	Layer         string         `json:"layer"`
	Start         string         `json:"start"`
	End           *string        `json:"end"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"status_message"`
	Attributes    map[string]any `json:"attributes"`
	Events        []eventJSON    `json:"events"`
	Links         []linkJSON     `json:"links"`

	// start is the sort key. It is unexported, so it never reaches the wire.
	start time.Time
}

// eventJSON is one thing that happened during a span: a retry, a rate-limit
// wait, a recorded error.
type eventJSON struct {
	Name       string         `json:"name"`
	Time       string         `json:"time"`
	Attributes map[string]any `json:"attributes"`
}

// linkJSON is one span this span points at, in whichever trace it lives.
type linkJSON struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

// errorJSON is the failure body, the same {"error": …} shape the domain
// routes use.
type errorJSON struct {
	Error string `json:"error"`
}

// Register adds the trace endpoints to mux. It registers rather than
// returning a handler because the command owns the mux: the playground lives
// on the same mux, the same server and the same port as the domain routes,
// and a stdlib pattern mux gives them precedence over the API's catch-all
// without any prefix stripping.
func (s *Store) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /playground/traces/{id}", s.showTrace)
	mux.HandleFunc("GET /playground/traces", s.listTraces)
}

// showTrace answers GET /playground/traces/{id} with the trace, or 404 when
// the id is unknown — never recorded, or evicted since.
func (s *Store) showTrace(w http.ResponseWriter, r *http.Request) {
	trace, ok := s.lookup(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, errorJSON{Error: "not found"})
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

// listTraces answers GET /playground/traces?backfill_id=… with every trace
// carrying that Backfill id, which is an empty array when there are none. The
// query parameter is the whole of the query: without it there is no question
// to answer, so it is a 400 rather than a list of everything.
func (s *Store) listTraces(w http.ResponseWriter, r *http.Request) {
	backfillID := r.URL.Query().Get("backfill_id")
	if backfillID == "" {
		writeJSON(w, http.StatusBadRequest, errorJSON{Error: "backfill_id is required"})
		return
	}
	writeJSON(w, http.StatusOK, s.byBackfill(backfillID))
}

// writeJSON sends v as the whole body at the given status.
func writeJSON(w http.ResponseWriter, code int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		body, code = []byte(`{"error":"encoding response"}`), http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(code)
	w.Write(body)
}
