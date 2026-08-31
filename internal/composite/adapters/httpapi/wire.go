package httpapi

import (
	"fmt"
	"time"

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

// compositeJSON is everything observable about one Composite Dataset: its
// identity, the configuration it was declared with, and its lifecycle state.
type compositeJSON struct {
	Name string `json:"name"`
	configJSON
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// asTime renders an instant the way every field of this API spells one.
func asTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// asComposite renders one declaration.
func asComposite(d domain.Dataset) compositeJSON {
	return compositeJSON{
		Name:       d.Name.String(),
		configJSON: asConfig(d.Config),
		State:      d.State.String(),
		CreatedAt:  asTime(d.CreatedAt),
		UpdatedAt:  asTime(d.UpdatedAt),
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
