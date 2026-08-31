package domain

import "errors"

// The sentinel errors that cross this context's port boundaries. Adapters
// wrap these so the use cases — and the REST adapter's status mapping — can
// branch on them with errors.Is.
var (
	// ErrInvalidConfig reports a declaration this context refuses: a name that
	// is not a kebab-case slug, an unknown Timeframe, a requested end that is
	// not after the start, a Source declaring a different Instrument. It is
	// permanent: the same declaration will always be refused.
	ErrInvalidConfig = errors.New("invalid composite dataset configuration")

	// ErrDuplicateName reports a Composite Dataset created under a name that
	// is already taken. Name is the identity, so there can only be one.
	ErrDuplicateName = errors.New("composite dataset already exists")

	// ErrNotFound reports a Composite Dataset that does not exist.
	ErrNotFound = errors.New("composite dataset not found")
)
