package domain

import "errors"

// Sentinel errors that cross the port boundaries. A Provider or Store adapter
// wraps these so use cases can branch on them with errors.Is.
var (
	// ErrUnknownSymbol reports a Symbol the Provider does not know. It is
	// permanent: retrying will not help.
	ErrUnknownSymbol = errors.New("unknown symbol")

	// ErrUnsupportedTimeframe reports a Timeframe outside the canonical set,
	// or one the Provider does not offer. It is permanent.
	ErrUnsupportedTimeframe = errors.New("unsupported timeframe")

	// ErrBackfillRunning reports a second Backfill requested for a Dataset
	// that already has one running.
	ErrBackfillRunning = errors.New("backfill already running for dataset")

	// ErrNotFound reports a Backfill, Gap or Dataset that does not exist.
	ErrNotFound = errors.New("not found")
)
