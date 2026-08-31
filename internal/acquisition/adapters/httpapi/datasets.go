package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// showCoverage answers GET /datasets/{p}/{s}/{tf}/coverage with the ranges
// that have been asked for, sorted and coalesced. A Dataset nothing was ever
// acquired for has an empty Coverage, which is [] and not an error.
func (a *api) showCoverage(w http.ResponseWriter, r *http.Request) {
	id, err := datasetFromPath(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	coverage, err := a.svc.Coverage(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asRanges(coverage))
}

// showCompleteness answers GET /datasets/{p}/{s}/{tf}/complete?start&end: is
// the range inside Coverage with no open Gap intersecting it, and if not,
// which Gaps stand in the way.
func (a *api) showCompleteness(w http.ResponseWriter, r *http.Request) {
	id, err := datasetFromPath(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	rng, err := rangeFromQuery(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	completeness, err := a.svc.IsComplete(r.Context(), id, rng)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, completenessJSON{
		Complete: completeness.Complete,
		Gaps:     asGaps(completeness.Gaps),
	})
}

// showBars answers GET /datasets/{p}/{s}/{tf}/bars?start&end[&format=json]:
// the Bars themselves, as a Parquet stream by default or as JSON on request.
//
// It never refuses an incomplete range — it flags it. Completeness is decided
// before a single byte of the body is written, so X-Complete and X-Gaps are
// on the response whatever happens to the stream afterwards.
func (a *api) showBars(w http.ResponseWriter, r *http.Request) {
	id, err := datasetFromPath(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	rng, err := rangeFromQuery(r)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err)
		return
	}
	format := r.URL.Query().Get("format")
	if format != "" && format != "json" && format != "parquet" {
		a.writeError(w, http.StatusBadRequest, fmt.Errorf("format %q is neither \"parquet\" nor \"json\"", format))
		return
	}

	completeness, err := a.svc.IsComplete(r.Context(), id, rng)
	if err != nil {
		a.fail(w, err)
		return
	}
	gaps, err := json.Marshal(asGaps(completeness.Gaps))
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("X-Complete", strconv.FormatBool(completeness.Complete))
	w.Header().Set("X-Gaps", string(gaps))

	if format == "json" {
		bars, err := a.svc.Bars(r.Context(), id, rng)
		if err != nil {
			a.fail(w, err)
			return
		}
		a.writeJSON(w, http.StatusOK, asBars(bars))
		return
	}

	w.Header().Set("Content-Type", contentTypeParquet)
	counted := &countingWriter{w: w}
	if err := a.svc.ExportParquet(r.Context(), id, rng, counted); err != nil {
		if counted.n == 0 {
			// Nothing was written yet, so the failure can still be reported
			// as one: the status line is unsent.
			a.fail(w, err)
			return
		}
		// The body is already on the wire and the status says 200. Truncating
		// it is all the honesty left, and the log is where this is answered.
		a.log.Error("export parquet", "dataset", id.String(), "range", rng.String(), "err", err)
	}
}

// countingWriter reports whether anything reached the client yet, which is
// what decides if a failure mid-stream can still become an error response.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}
