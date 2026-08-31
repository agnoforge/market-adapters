package httpapi

import (
	"fmt"
	"time"

	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// This file is the whole wire format of the composites resource: every JSON
// document it reads or writes, and the translation to and from the composite
// domain. Nothing else in the package spells a field name.
//
// Instants are RFC3339 in UTC, the way the acquisition resource spells one.
// requested_end is the same, or the literal "now".

// errorJSON is the body of every failed request, whatever the status.
type errorJSON struct {
	Error string `json:"error"`
}

// sourceJSON names one source Dataset a Composite Dataset draws on.
// instrument may be omitted, in which case it is the dataset's own — a source
// that spells a different one is refused.
type sourceJSON struct {
	Instrument string `json:"instrument"`
	Provider   string `json:"provider"`
	Symbol     string `json:"symbol"`
	Timeframe  string `json:"timeframe"`
}

// catchUpJSON declares how the tail is filled. An omitted catch_up is
// {"kind":"base"}: the base provider fills its own tail, so no foreign data
// can enter a dataset that did not ask for it.
type catchUpJSON struct {
	Kind       string `json:"kind"`
	Instrument string `json:"instrument,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Symbol     string `json:"symbol,omitempty"`
	Timeframe  string `json:"timeframe,omitempty"`
}

// configJSON is the declaration itself, the body of a create and of an edit
// alike. An omitted mode is "strict": a dataset never becomes a research
// dataset by omission.
type configJSON struct {
	Instrument     string       `json:"instrument"`
	Base           sourceJSON   `json:"base"`
	CatchUp        *catchUpJSON `json:"catch_up"`
	RequestedStart string       `json:"requested_start"`
	RequestedEnd   string       `json:"requested_end"`
	Timeframes     []string     `json:"timeframes"`
	Mode           string       `json:"mode"`
}

// createRequestJSON is the body of POST /composites: a name and the
// configuration to declare under it.
type createRequestJSON struct {
	Name string `json:"name"`
	configJSON
}

// compositeJSON is everything observable about one Composite Dataset's
// declaration: its identity, the configuration it was declared with, its
// lifecycle state, and what the last Build left on the row. resolved_end is
// omitted until a Build has resolved one, and last_error until one has failed.
type compositeJSON struct {
	Name string `json:"name"`
	configJSON
	State       string `json:"state"`
	ResolvedEnd string `json:"resolved_end,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// compositeDetailJSON is what Get and Build answer: the declaration plus the
// ordered Segments the last Build assembled and the Quality it computed. The
// Segment list is the provenance API; quality is null until a Build has run.
type compositeDetailJSON struct {
	compositeJSON
	Segments []segmentJSON `json:"segments"`
	Quality  *qualityJSON  `json:"quality"`
}

// segmentJSON is one contiguous, provider-attributed slice of the composite
// timeline. The range is half-open, the way every range in this service is.
type segmentJSON struct {
	Kind   string     `json:"kind"`
	Source sourceJSON `json:"source"`
	Start  string     `json:"start"`
	End    string     `json:"end"`
}

// gapJSON is one open Gap Quality lists. The id is acquisition's, so an
// operator can act on the same Gap acquisition knows.
type gapJSON struct {
	ID    int64  `json:"id"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// transitionJSON is one provider boundary the timeline crosses, with the price
// movement across it. The three prices are decimal strings, exactly as the
// bars hold them, and empty when one of the two bars was not there to price the
// transition with. No threshold is applied to a delta: it is recorded so a
// researcher can see the seam, never enforced.
type transitionJSON struct {
	At    string     `json:"at"`
	From  sourceJSON `json:"from"`
	To    sourceJSON `json:"to"`
	Close string     `json:"close"`
	Open  string     `json:"open"`
	Delta string     `json:"price_delta"`
}

// qualityJSON is what a Build computed about the dataset, judged against the
// resolved end. `strict` is the one boolean that separates a strict dataset
// from a research one, so an imperfect dataset can never read as a complete
// one.
type qualityJSON struct {
	RequestedStart     string           `json:"requested_start"`
	RequestedEnd       string           `json:"requested_end"`
	ResolvedEnd        string           `json:"resolved_end"`
	AvailableStart     string           `json:"available_start,omitempty"`
	AvailableEnd       string           `json:"available_end,omitempty"`
	ExpectedBars       int64            `json:"expected_bars"`
	ActualBars         int64            `json:"actual_bars"`
	CoveragePercentage float64          `json:"coverage_percentage"`
	OpenGapCount       int              `json:"open_gap_count"`
	OpenGaps           []gapJSON        `json:"open_gaps"`
	TransitionCount    int              `json:"transition_count"`
	Transitions        []transitionJSON `json:"transitions"`
	Mode               string           `json:"mode"`
	Strict             bool             `json:"strict"`
	LastBuildAt        string           `json:"last_build_at"`
}

// asTime renders an instant the way every field of this API spells one.
func asTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// asTimeOrEmpty renders an instant, or nothing at all when there is none.
func asTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return asTime(t)
}

// asComposite renders one declaration.
func asComposite(d domain.Dataset) compositeJSON {
	return compositeJSON{
		Name:        d.Name.String(),
		configJSON:  asConfig(d.Config),
		State:       d.State.String(),
		ResolvedEnd: asTimeOrEmpty(d.ResolvedEnd),
		LastError:   d.LastError,
		CreatedAt:   asTime(d.CreatedAt),
		UpdatedAt:   asTime(d.UpdatedAt),
	}
}

// asDetail renders a declaration with its provenance: the ordered Segments and
// the Quality. No segments is [], never null; no Build yet is a null quality.
func asDetail(v app.View) compositeDetailJSON {
	out := compositeDetailJSON{
		compositeJSON: asComposite(v.Dataset),
		Segments:      make([]segmentJSON, 0, len(v.Segments)),
	}
	for _, seg := range v.Segments {
		out.Segments = append(out.Segments, segmentJSON{
			Kind:   seg.Kind.String(),
			Source: asSource(seg.Source),
			Start:  asTime(seg.Range.Start),
			End:    asTime(seg.Range.End),
		})
	}
	if v.Quality != nil {
		out.Quality = asQuality(*v.Quality)
	}
	return out
}

// asQuality renders what a Build computed.
func asQuality(q domain.Quality) *qualityJSON {
	gaps := make([]gapJSON, 0, len(q.OpenGaps))
	for _, g := range q.OpenGaps {
		gaps = append(gaps, gapJSON{ID: g.ID, Start: asTime(g.Range.Start), End: asTime(g.Range.End)})
	}
	transitions := make([]transitionJSON, 0, len(q.Transitions))
	for _, t := range q.Transitions {
		transitions = append(transitions, transitionJSON{
			At:    asTime(t.At),
			From:  asSource(t.From),
			To:    asSource(t.To),
			Close: t.Close,
			Open:  t.Open,
			Delta: t.Delta,
		})
	}
	return &qualityJSON{
		RequestedStart:     asTime(q.RequestedStart),
		RequestedEnd:       q.RequestedEnd.String(),
		ResolvedEnd:        asTime(q.ResolvedEnd),
		AvailableStart:     asTimeOrEmpty(q.AvailableStart),
		AvailableEnd:       asTimeOrEmpty(q.AvailableEnd),
		ExpectedBars:       q.ExpectedBars,
		ActualBars:         q.ActualBars,
		CoveragePercentage: q.Coverage(),
		OpenGapCount:       q.OpenGapCount(),
		OpenGaps:           gaps,
		TransitionCount:    q.TransitionCount(),
		Transitions:        transitions,
		Mode:               q.Mode.String(),
		Strict:             q.Strict(),
		LastBuildAt:        asTime(q.LastBuildAt),
	}
}

// asComposites renders a list. No datasets is [], never null.
func asComposites(datasets []domain.Dataset) []compositeJSON {
	out := make([]compositeJSON, 0, len(datasets))
	for _, d := range datasets {
		out = append(out, asComposite(d))
	}
	return out
}

func asConfig(c domain.Config) configJSON {
	frames := make([]string, 0, len(c.Timeframes))
	for _, tf := range c.Timeframes {
		frames = append(frames, tf.String())
	}
	return configJSON{
		Instrument:     c.Instrument.String(),
		Base:           asSource(c.Base),
		CatchUp:        asCatchUp(c.CatchUp),
		RequestedStart: asTime(c.RequestedStart),
		RequestedEnd:   c.RequestedEnd.String(),
		Timeframes:     frames,
		Mode:           c.Mode.String(),
	}
}

func asSource(s domain.Source) sourceJSON {
	return sourceJSON{
		Instrument: s.Instrument.String(),
		Provider:   s.Provider,
		Symbol:     s.Symbol.String(),
		Timeframe:  s.Timeframe.String(),
	}
}

// asCatchUp renders the catch-up declaration. Only an explicitly configured
// one names a provider, so what comes back is what was declared.
func asCatchUp(c domain.CatchUp) *catchUpJSON {
	out := &catchUpJSON{Kind: string(c.Kind)}
	if c.Kind == domain.CatchUpSource {
		out.Instrument = c.Source.Instrument.String()
		out.Provider = c.Source.Provider
		out.Symbol = c.Source.Symbol.String()
		out.Timeframe = c.Source.Timeframe.String()
	}
	return out
}

// toConfig reads a declaration off the wire. Everything it can decide on its
// own — an unspellable instant, an unknown timeframe — is decided here; every
// rule about the configuration as a whole belongs to the domain, which the use
// case runs next.
func (c configJSON) toConfig() (domain.Config, error) {
	instrument, err := domain.ParseInstrument(c.Instrument)
	if err != nil {
		return domain.Config{}, err
	}
	base, err := c.Base.toSource("base", instrument)
	if err != nil {
		return domain.Config{}, err
	}
	catchUp, err := c.CatchUp.toCatchUp(instrument)
	if err != nil {
		return domain.Config{}, err
	}
	start, err := parseTime("requested_start", c.RequestedStart)
	if err != nil {
		return domain.Config{}, err
	}
	end, err := parseRequestedEnd(c.RequestedEnd)
	if err != nil {
		return domain.Config{}, err
	}
	frames := make([]domain.Timeframe, 0, len(c.Timeframes))
	for _, raw := range c.Timeframes {
		tf, err := domain.ParseTimeframe(raw)
		if err != nil {
			return domain.Config{}, err
		}
		frames = append(frames, tf)
	}
	mode, err := domain.ParseMode(c.Mode)
	if err != nil {
		return domain.Config{}, err
	}
	return domain.Config{
		Instrument:     instrument,
		Base:           base,
		CatchUp:        catchUp,
		RequestedStart: start,
		RequestedEnd:   end,
		Timeframes:     frames,
		Mode:           mode,
	}, nil
}

// toSource reads one Source. An omitted instrument is the dataset's own; a
// different one is left for the domain to refuse, so the message a caller
// reads is the domain's.
func (s sourceJSON) toSource(role string, instrument domain.Instrument) (domain.Source, error) {
	declared := instrument
	if s.Instrument != "" {
		declared = domain.Instrument(s.Instrument)
	}
	timeframe := domain.TF1m
	if s.Timeframe != "" {
		tf, err := domain.ParseTimeframe(s.Timeframe)
		if err != nil {
			return domain.Source{}, fmt.Errorf("%w on the %s source", err, role)
		}
		timeframe = tf
	}
	return domain.Source{
		Instrument: declared,
		Provider:   s.Provider,
		Symbol:     acq.Symbol(s.Symbol),
		Timeframe:  timeframe,
	}, nil
}

// toCatchUp reads the catch-up declaration. Omitting it means the base
// provider fills its own tail.
func (c *catchUpJSON) toCatchUp(instrument domain.Instrument) (domain.CatchUp, error) {
	if c == nil {
		return domain.CatchUp{Kind: domain.CatchUpBase}, nil
	}
	kind := domain.CatchUpKind(c.Kind)
	if c.Kind == "" {
		kind = domain.CatchUpBase
	}
	if kind != domain.CatchUpSource {
		return domain.CatchUp{Kind: kind}, nil
	}
	source, err := sourceJSON{
		Instrument: c.Instrument, Provider: c.Provider,
		Symbol: c.Symbol, Timeframe: c.Timeframe,
	}.toSource("catch-up", instrument)
	if err != nil {
		return domain.CatchUp{}, err
	}
	return domain.CatchUp{Kind: domain.CatchUpSource, Source: source}, nil
}

// parseTime reads one bound: an RFC3339 instant, or a YYYY-MM-DD date read as
// midnight UTC.
func parseTime(field, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, fmt.Errorf("%w: %s is required", domain.ErrInvalidConfig, field)
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%w: %s %q is neither an RFC3339 instant nor a YYYY-MM-DD date",
		domain.ErrInvalidConfig, field, v)
}

// parseRequestedEnd reads the requested end: the literal "now", or an instant.
func parseRequestedEnd(v string) (domain.RequestedEnd, error) {
	if v == domain.NowLiteral {
		return domain.NowEnd(), nil
	}
	t, err := parseTime("requested_end", v)
	if err != nil {
		return domain.RequestedEnd{}, err
	}
	return domain.FixedEnd(t), nil
}
