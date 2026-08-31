package domain

import (
	"fmt"
	"slices"
	"time"

	acq "github.com/agnos/agnoforge/internal/domain"
)

// Mode is the readiness rule a Composite Dataset is judged by. It changes
// nothing else: strict and research datasets have the same surface, and a
// research one is always visibly a research one.
type Mode string

const (
	// ModeStrict refuses readiness while any open Gap, invalid Transition or
	// incomplete materialization exists.
	ModeStrict Mode = "strict"
	// ModeResearch allows readiness with those imperfections, listed in
	// Quality and flagged on the dataset.
	ModeResearch Mode = "research"
)

// ParseMode reads a Mode. The empty string is strict: a dataset can never
// become a research dataset by omission.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "", ModeStrict:
		return ModeStrict, nil
	case ModeResearch:
		return ModeResearch, nil
	default:
		return "", fmt.Errorf(`%w: mode %q is neither "strict" nor "research"`, ErrInvalidConfig, s)
	}
}

// String returns the Mode as the API spells it.
func (m Mode) String() string { return string(m) }

// State is where a Composite Dataset is in its lifecycle:
// draft → building → ready | failed, plus stale.
type State string

const (
	// StateDraft means the dataset is declared and has never been built.
	StateDraft State = "draft"
	// StateBuilding means a Build is running.
	StateBuilding State = "building"
	// StateReady means the last Build succeeded and the dataset is servable.
	StateReady State = "ready"
	// StateFailed means the last Build failed; the error is preserved.
	StateFailed State = "failed"
	// StateStale means the configuration was edited after a Build, so what is
	// served no longer matches what is declared. Only an edit causes this —
	// source-data drift does not.
	StateStale State = "stale"
)

// BeginBuild is the State a starting Build moves a dataset into. Every resting
// state — draft, stale, ready, failed — may be rebuilt; a dataset that is
// already building is refused with ErrBuildRunning, because two builds of one
// dataset would interleave and corrupt what they both write.
func (s State) BeginBuild() (State, error) {
	if s == StateBuilding {
		return "", fmt.Errorf("%w: a build of this composite dataset is already running", ErrBuildRunning)
	}
	return StateBuilding, nil
}

// Settled is the State a finished Build leaves behind: ready when the dataset
// met its mode's readiness rule, failed otherwise. A failed dataset keeps the
// error that explains it.
func Settled(ready bool) State {
	if ready {
		return StateReady
	}
	return StateFailed
}

// AfterEdit is the State a dataset is in once its configuration was edited. A
// dataset that has never been built is still a draft; anything that has been
// built is stale until the next Build.
func (s State) AfterEdit() State {
	if s == StateDraft {
		return StateDraft
	}
	return StateStale
}

// String returns the State as the API spells it.
func (s State) String() string { return string(s) }

// Source names one source Dataset a Composite Dataset draws on, together with
// the Instrument the user asserts it is. Provider and Symbol are acquisition's
// terms, used here with acquisition's meaning.
type Source struct {
	Instrument Instrument
	Provider   string
	Symbol     acq.Symbol
	Timeframe  Timeframe
}

// String renders a Source the way a log line names one.
func (s Source) String() string {
	return s.Provider + "/" + s.Symbol.String() + "/" + s.Timeframe.String()
}

// CatchUpKind says where the tail between what the base provider can supply
// and the Resolved End comes from.
type CatchUpKind string

const (
	// CatchUpBase is the default: the base provider fills its own tail, and no
	// foreign data can enter the dataset.
	CatchUpBase CatchUpKind = "base"
	// CatchUpNone disables catch-up entirely.
	CatchUpNone CatchUpKind = "none"
	// CatchUpSource names a different provider explicitly. Cross-provider
	// assembly is impossible unless the user configured it this way.
	CatchUpSource CatchUpKind = "source"
)

// CatchUp is the catch-up declaration. Source is meaningful only when Kind is
// CatchUpSource.
type CatchUp struct {
	Kind   CatchUpKind
	Source Source
}

// RequestedEnd is the end of a Composite Dataset's requested range: either a
// fixed instant, or the literal `now`, which every Build resolves afresh to
// the last fully closed 1-minute bar.
type RequestedEnd struct {
	// Now reports whether the end was declared as `now`.
	Now bool
	// At is the fixed instant, meaningful only when Now is false.
	At time.Time
}

// NowLiteral is how a caller asks for an end that tracks the present. It is
// the one spelling of it, shared by every adapter that reads or writes one.
const NowLiteral = "now"

// FixedEnd is a RequestedEnd pinned to an instant.
func FixedEnd(t time.Time) RequestedEnd { return RequestedEnd{At: t.UTC()} }

// NowEnd is a RequestedEnd that each Build resolves.
func NowEnd() RequestedEnd { return RequestedEnd{Now: true} }

// Resolve is the concrete end a Build uses: a fixed end passes through
// untouched, and `now` becomes the close of the last fully closed 1-minute bar
// — which is the open time of the one still forming, because the range is
// half-open. Completeness and Quality are judged against what this returns.
func (e RequestedEnd) Resolve(now time.Time) time.Time {
	if !e.Now {
		return e.At.UTC()
	}
	return TF1m.WindowStart(now)
}

// String renders the end the way it was declared: "now", or the instant.
func (e RequestedEnd) String() string {
	if e.Now {
		return NowLiteral
	}
	return e.At.UTC().Format(time.RFC3339)
}

// Config is everything a researcher declares about a Composite Dataset. It is
// editable; every edit makes a built dataset stale.
type Config struct {
	Instrument Instrument
	// Base is the one existing 1-minute source Dataset the composite is
	// built on.
	Base Source
	// CatchUp says how the tail is filled. The zero value is CatchUpBase.
	CatchUp CatchUp
	// RequestedStart is the inclusive start of the requested half-open range.
	RequestedStart time.Time
	// RequestedEnd is its exclusive end, fixed or `now`.
	RequestedEnd RequestedEnd
	// Timeframes are the higher timeframes to materialize, ascending and
	// without repetition. It may be empty: the 1-minute timeline is served
	// whatever this holds.
	Timeframes []Timeframe
	Mode       Mode
}

// Normalize returns cfg with its Timeframes sorted ascending and deduplicated
// and its instants in UTC, so two declarations of the same thing are the same
// Config. It does not validate.
func (c Config) Normalize() Config {
	out := c
	out.RequestedStart = c.RequestedStart.UTC()
	if !c.RequestedEnd.Now {
		out.RequestedEnd = FixedEnd(c.RequestedEnd.At)
	}
	seen := make(map[Timeframe]bool, len(c.Timeframes))
	frames := make([]Timeframe, 0, len(c.Timeframes))
	for _, tf := range c.Timeframes {
		if seen[tf] {
			continue
		}
		seen[tf] = true
		frames = append(frames, tf)
	}
	slices.SortFunc(frames, func(a, b Timeframe) int { return a.Order() - b.Order() })
	out.Timeframes = frames
	return out
}

// Validate reports why the configuration cannot be declared, as an error
// wrapping ErrInvalidConfig, or nil when it can.
//
// The rules are the ones a researcher can get wrong on paper: the Instrument
// and every Source must be well formed and agree; the base must be a 1-minute
// source; a fixed end must be after the start; every materialized Timeframe
// must be a known one higher than the 1-minute timeline it is derived from.
func (c Config) Validate() error {
	if _, err := ParseInstrument(string(c.Instrument)); err != nil {
		return err
	}
	if err := c.validateSource("base", c.Base); err != nil {
		return err
	}
	switch c.CatchUp.Kind {
	case CatchUpBase, CatchUpNone:
	case CatchUpSource:
		if err := c.validateSource("catch-up", c.CatchUp.Source); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: catch-up %q is none of \"base\", \"none\", \"source\"", ErrInvalidConfig, c.CatchUp.Kind)
	}
	if c.RequestedStart.IsZero() {
		return fmt.Errorf("%w: requested start is required", ErrInvalidConfig)
	}
	if !c.RequestedEnd.Now {
		r := acq.Range{Start: c.RequestedStart, End: c.RequestedEnd.At}
		if r.IsEmpty() {
			return fmt.Errorf("%w: requested end %s is not after requested start %s",
				ErrInvalidConfig, instant(c.RequestedEnd.At), instant(c.RequestedStart))
		}
	}
	for _, tf := range c.Timeframes {
		if !tf.Valid() {
			return fmt.Errorf("%w: unknown timeframe %q", ErrInvalidConfig, tf)
		}
		if tf == TF1m {
			return fmt.Errorf("%w: %q is the composite timeline itself, not a materialized timeframe", ErrInvalidConfig, tf)
		}
	}
	if _, err := ParseMode(string(c.Mode)); err != nil {
		return err
	}
	return nil
}

// validateSource checks one Source: it must name a Provider and a Symbol, be
// a 1-minute source — the only timeframe this context ever asks acquisition
// for — and declare the Composite Dataset's own Instrument.
func (c Config) validateSource(role string, s Source) error {
	if s.Provider == "" {
		return fmt.Errorf("%w: %s source needs a provider", ErrInvalidConfig, role)
	}
	if s.Symbol == "" {
		return fmt.Errorf("%w: %s source needs a symbol", ErrInvalidConfig, role)
	}
	if !s.Timeframe.Valid() {
		return fmt.Errorf("%w: unknown timeframe %q on the %s source", ErrInvalidConfig, s.Timeframe, role)
	}
	if s.Timeframe != TF1m {
		return fmt.Errorf("%w: %s source must be a 1m source, not %q", ErrInvalidConfig, role, s.Timeframe)
	}
	if s.Instrument != c.Instrument {
		return fmt.Errorf("%w: %s source declares instrument %q, but the dataset is %q",
			ErrInvalidConfig, role, s.Instrument, c.Instrument)
	}
	return nil
}

// Dataset is a declared Composite Dataset: its identity, the configuration it
// was declared with, and where it is in its lifecycle.
type Dataset struct {
	Name   Name
	Config Config
	State  State
	// ResolvedEnd is the concrete end the last Build resolved the requested
	// one to. It is zero until a Build has run.
	ResolvedEnd time.Time
	// LastError is why the last Build failed, preserved so a failure is
	// inspectable after the fact. It is empty for any other state.
	LastError string
	// MatVersion is the materialization version the last Build derived this
	// dataset's higher-timeframe bars with. It is zero until a Build has run.
	MatVersion int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Observed is the dataset as a reader must see it. A ready dataset whose bars
// were derived by a materialization version this service no longer produces is
// stale: what is served is not what this service would derive, and only the
// next Build can reconcile them.
//
// The staleness is derived, never written: bumping the version marks every
// dataset built before it at once, and rebuilding one clears it.
func (d Dataset) Observed() Dataset {
	if d.State == StateReady && d.MatVersion != MaterializationVersion {
		d.State = StateStale
	}
	return d
}

// instant is how a bound reaches an error message: RFC3339, in UTC.
func instant(t time.Time) string { return t.UTC().Format(time.RFC3339) }
