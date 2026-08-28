package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/agnos/agnoforge/internal/domain"
)

func TestSentinelErrorsAreDistinctAndWrappable(t *testing.T) {
	sentinels := map[string]error{
		"ErrUnknownSymbol":        domain.ErrUnknownSymbol,
		"ErrUnsupportedTimeframe": domain.ErrUnsupportedTimeframe,
		"ErrBackfillRunning":      domain.ErrBackfillRunning,
		"ErrNotFound":             domain.ErrNotFound,
	}
	for name, err := range sentinels {
		if err == nil {
			t.Fatalf("%s is nil", name)
		}
		wrapped := fmt.Errorf("context: %w", err)
		if !errors.Is(wrapped, err) {
			t.Errorf("%s does not survive wrapping", name)
		}
		for otherName, other := range sentinels {
			if otherName == name {
				continue
			}
			if errors.Is(wrapped, other) {
				t.Errorf("%s matches %s; sentinels must be distinct", name, otherName)
			}
		}
	}
}
