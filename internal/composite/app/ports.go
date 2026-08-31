package app

import (
	"context"

	"github.com/agnos/agnoforge/internal/composite/domain"
)

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
}
