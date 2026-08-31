package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The backtester contract (decision 32): one route serves every resolution of
// a Composite Dataset, and one route answers what the dataset is worth.
//
// A consumer names a dataset, a Timeframe and a range and gets bars. Whether
// they were read from the referenced source bars of the 1-minute timeline or
// from the bars a Build materialized is not on the wire anywhere, because it is
// not the consumer's question (user stories 26–28).

// showBars answers GET /composites/{name}/bars?timeframe&start&end[&format]:
// the bars themselves, as a Parquet stream by default or as JSON on request.
//
// Both bounds are optional and default to the dataset's own resolved range, so
// "give me this dataset" is a request a backtester can make without knowing
// what `now` resolved to. An omitted timeframe is the 1-minute composite
// timeline, which every dataset has.
//
// What the dataset is — its lifecycle state and its readiness mode — is
// decided before a single byte of the body is written, so the headers carry it
// whatever happens to the stream afterwards. A `stale` or research dataset is
// served, and it is served visibly as one.
func (a *api) showBars(w http.ResponseWriter, r *http.Request) {
	name, err := nameFromPath(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	req, err := barsRequestFromQuery(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	format, err := formatFromQuery(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	query, err := a.svc.PlanBars(r.Context(), name, req)
	if err != nil {
		a.fail(w, err)
		return
	}

	w.Header().Set("X-Composite-State", query.Dataset.State.String())
	w.Header().Set("X-Composite-Mode", query.Dataset.Config.Mode.String())
	w.Header().Set("X-Timeframe", query.Timeframe.String())
	w.Header().Set("X-Range-Start", asTime(query.Range.Start))
	w.Header().Set("X-Range-End", asTime(query.Range.End))

	if format == formatJSON {
		bars, err := a.svc.Bars(r.Context(), query)
		if err != nil {
			a.fail(w, err)
			return
		}
		a.writeJSON(w, http.StatusOK, asBars(bars))
		return
	}

	w.Header().Set("Content-Type", contentTypeParquet)
	counted := &countingWriter{w: w}
	if err := a.svc.ExportBars(r.Context(), query, counted); err != nil {
		if counted.n == 0 {
			// Nothing was written yet, so the failure can still be reported as
			// one: the status line is unsent.
			a.fail(w, err)
			return
		}
		// The body is already on the wire and the status says 200. Truncating it
		// is all the honesty left, and the log is where this is answered.
		a.log.Error("export composite parquet", "dataset", name.String(),
			"timeframe", query.Timeframe.String(), "range", query.Range.String(), "err", err)
	}
}

// showQuality answers GET /composites/{name}/quality with the Quality the last
// Build persisted, and the state and mode it belongs to.
//
// It is the fitness answer a researcher reads before running anything (user
// story 19), and it is deliberately not an error for a dataset no Build has run
// for: the quality is null and the state says draft, which is the honest answer
// to "how good is this dataset?".
func (a *api) showQuality(w http.ResponseWriter, r *http.Request) {
	name, err := nameFromPath(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	view, err := a.svc.View(r.Context(), name)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, asQualityDocument(view))
}

// The two encodings a bars query can be answered in. Parquet is the default,
// because the payload a backtester actually asks for is a large one.
const (
	formatParquet = "parquet"
	formatJSON    = "json"
)

// formatFromQuery reads ?format. Anything but the two encodings is refused
// rather than silently answered in one of them.
func formatFromQuery(r *http.Request) (string, error) {
	switch format := r.URL.Query().Get("format"); format {
	case "":
		return formatParquet, nil
	case formatParquet, formatJSON:
		return format, nil
	default:
		return "", fmt.Errorf("%w: format %q is neither %q nor %q",
			domain.ErrInvalidConfig, format, formatParquet, formatJSON)
	}
}

// barsRequestFromQuery reads what a consumer asked for: the Timeframe, and the
// two bounds it may have left to the dataset.
func barsRequestFromQuery(r *http.Request) (app.BarsRequest, error) {
	q := r.URL.Query()
	// The 1-minute timeline is the dataset's own frame and every dataset has
	// it, so it is what an unstated timeframe means.
	timeframe := domain.TF1m
	if raw := q.Get("timeframe"); raw != "" {
		tf, err := domain.ParseTimeframe(raw)
		if err != nil {
			return app.BarsRequest{}, err
		}
		timeframe = tf
	}
	start, err := optionalTime("start", q.Get("start"))
	if err != nil {
		return app.BarsRequest{}, err
	}
	end, err := optionalTime("end", q.Get("end"))
	if err != nil {
		return app.BarsRequest{}, err
	}
	return app.BarsRequest{Timeframe: timeframe, Start: start, End: end}, nil
}

// optionalTime reads one bound a caller may have omitted. The zero instant is
// "not stated", which the dataset's own range answers.
func optionalTime(field, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return parseTime(field, v)
}

// countingWriter reports whether anything reached the client yet, which is what
// decides if a failure mid-stream can still become an error response.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}
