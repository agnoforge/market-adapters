package domain

import (
	"fmt"
	"strings"
)

// Instrument is the logical market a Composite Dataset represents, spelled
// the way a person names one: "BTC/USD". It is a validated string and nothing
// more — there is no registry and no symbol mapping. Every Source in a
// configuration must declare this same Instrument; the user is asserting that
// those provider Symbols really are this market.
type Instrument string

// maxInstrumentLength bounds an Instrument the way maxNameLength bounds a
// Name.
const maxInstrumentLength = 64

// ParseInstrument accepts a non-empty, unpadded string of printable
// characters and rejects everything else with an error wrapping
// ErrInvalidConfig.
func ParseInstrument(s string) (Instrument, error) {
	if s == "" || len(s) > maxInstrumentLength ||
		s != strings.TrimSpace(s) || strings.ContainsAny(s, "\n\r\t") {
		return "", fmt.Errorf("%w: instrument %q is not a market name", ErrInvalidConfig, s)
	}
	return Instrument(s), nil
}

// String returns the Instrument as it was declared.
func (i Instrument) String() string { return string(i) }
