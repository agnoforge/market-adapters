package app

import (
	"context"
	"errors"
	"time"

	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// The two answers a backfill request can come back with that are not failures.
// Both are stated in this context's words: the adapter translates whatever
// acquisition spells them as (decision 15).
var (
	// ErrBackfillBusy reports that acquisition already has a backfill running
	// for the source Dataset. It is transient and it is not a build failure:
	// the Build waits for the running one and asks again.
	ErrBackfillBusy = errors.New("a backfill of the source dataset is already running")

	// ErrNothingToAcquire reports that the provider has nothing to acquire in
	// the requested range — its earliest-available floor lies past the range,
	// or the range holds no closed bar yet. It is the provider's answer about
	// what exists, not a failure: a Build that hears it carries on and lets the
	// mode judge the shortfall.
	ErrNothingToAcquire = errors.New("the provider has nothing to acquire in the range")
)

// AcquisitionPort is everything this context asks of Market Data Acquisition,
// stated in this context's own words. It is consumer-defined here — the
// acquisition service knows nothing about it — and its production adapter
// wraps that service in-process.
//
// It is a control plane and nothing else (decision 33, ADR-0005): coverage,
// completeness, backfill start and wait, gap detection and repair. There is
// deliberately **no method that returns bars**. Source 1-minute bars are the
// data plane, read read-only in SQL from acquisition's own table through the
// Store port, because staging the largest data in the system through Go values
// would buy nothing. A future service split replaces one store read, not this
// port.
type AcquisitionPort interface {
	// Coverage returns the ranges the source Dataset has been acquired over,
	// sorted by start and coalesced.
	Coverage(ctx context.Context, src domain.Source) ([]acq.Range, error)

	// Completeness reports whether r can be trusted for the source Dataset —
	// covered, with no open Gap in it — and which open Gaps stand in the way.
	Completeness(ctx context.Context, src domain.Source, r acq.Range) (Completeness, error)

	// StartBackfill asks acquisition to acquire r for the source Dataset and
	// returns the handle to wait on. It reports ErrBackfillBusy when the source
	// Dataset already has a backfill running, and ErrNothingToAcquire when the
	// provider has nothing in r; every other error is a failure to ask.
	StartBackfill(ctx context.Context, src domain.Source, r acq.Range) (BackfillHandle, error)

	// WaitBackfill blocks until the backfill finishes, reporting nil when it
	// completed and the acquisition failure otherwise.
	WaitBackfill(ctx context.Context, h BackfillHandle) error

	// DetectGaps records where bars are expected inside r and absent, and
	// returns the open Gaps the source Dataset now has there.
	DetectGaps(ctx context.Context, src domain.Source, r acq.Range) ([]domain.Gap, error)

	// RepairGap asks acquisition to re-acquire one Gap's range and returns the
	// handle to wait on. It is a backfill like any other, so it reports
	// ErrBackfillBusy and ErrNothingToAcquire the same way StartBackfill does.
	RepairGap(ctx context.Context, g domain.Gap) (BackfillHandle, error)
}

// Completeness is the port's answer about one source range: whether it can be
// trusted, and the open Gaps that say why not.
type Completeness struct {
	// Complete is true when the range lies entirely inside the source
	// Dataset's coverage and no open Gap intersects it.
	Complete bool
	// Gaps are the open Gaps intersecting the range, ascending.
	Gaps []domain.Gap
}

// BackfillHandle names one running acquisition backfill for as long as it
// runs. It is opaque: only the port's adapter knows how acquisition spells one.
type BackfillHandle string

// SourceBars is what acquisition's bars table holds for one source Dataset
// inside a range: how many bars, and the open time of the first and last of
// them. First and Last are zero when Count is zero.
type SourceBars struct {
	Count       int64
	First, Last time.Time
}

// Store persists Composite Dataset declarations: the configuration a
// researcher declared and the lifecycle state it is in. It never touches
// source data — source Datasets belong to acquisition and are read-only here.
//
// Implementations live under internal/composite/adapters; nothing in this
// package knows which database is behind the port.
type Store interface {
	// CreateDataset writes a new declaration. A Name that is already taken is
	// an error wrapping domain.ErrDuplicateName: Name is the identity.
	CreateDataset(ctx context.Context, d domain.Dataset) error

	// Dataset returns one declaration by Name, or an error wrapping
	// domain.ErrNotFound.
	Dataset(ctx context.Context, name domain.Name) (domain.Dataset, error)

	// Datasets returns every declaration, ordered by Name.
	Datasets(ctx context.Context) ([]domain.Dataset, error)

	// UpdateDataset replaces the configuration and state of an existing
	// declaration, or returns an error wrapping domain.ErrNotFound.
	UpdateDataset(ctx context.Context, d domain.Dataset) error

	// DeleteDataset removes a declaration and everything derived from it. It
	// never removes source data. A Name that does not exist is an error
	// wrapping domain.ErrNotFound.
	DeleteDataset(ctx context.Context, name domain.Name) error

	// SaveBuild records the outcome of one Build in one go: the dataset row
	// with its new state, resolved end and error, the ordered Segments, and
	// the Quality. Segments and Quality are replaced wholesale, so what is
	// stored always describes the build the row is in the state of — a build
	// that could not be ready therefore leaves no Segments behind.
	SaveBuild(ctx context.Context, d domain.Dataset, segments []domain.Segment, q domain.Quality) error

	// Segments returns the ordered Segments of a Composite Dataset, empty when
	// no Build has assembled any.
	Segments(ctx context.Context, name domain.Name) ([]domain.Segment, error)

	// Quality returns what the last Build computed and whether there was one.
	Quality(ctx context.Context, name domain.Name) (domain.Quality, bool, error)

	// SourceBars reports what acquisition's bars table holds for one source
	// Dataset inside r. It is a read-only read of another context's table —
	// the accepted data plane (ADR-0005) — and the only place composite code
	// touches source bars at all.
	SourceBars(ctx context.Context, src domain.Source, r acq.Range) (SourceBars, error)

	// TransitionDelta prices one Transition: the close of the last bar `from`
	// has before at, the open of the bar `to` has at at, and the exact
	// difference between them.
	//
	// It is the same read-only read of acquisition's bars, for the same
	// reason: a price is a fact in that table, and it is a decimal there. It
	// stays a decimal all the way here — the difference is computed over the
	// stored decimals, never over a float — so what Quality records is exactly
	// the movement across the seam.
	//
	// A Transition one of the two bars is missing for is not an error: it
	// comes back with Priced false and no prices.
	TransitionDelta(ctx context.Context, from, to domain.Source, at time.Time) (PriceDelta, error)
}

// PriceDelta is the price movement across one Transition, as the exact
// decimal strings the bars hold. Priced is false when one of the two bars was
// not there, and then the three strings are empty.
type PriceDelta struct {
	Close  string
	Open   string
	Delta  string
	Priced bool
}
