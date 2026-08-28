package domain

import "fmt"

// GapStatus is the lifecycle state of a Gap.
type GapStatus string

const (
	// GapOpen means Bars are expected in the range and still absent.
	GapOpen GapStatus = "open"
	// GapRepaired means a Repair filled the range.
	GapRepaired GapStatus = "repaired"
	// GapIgnored means an operator accepted the absence; the range no longer
	// blocks Complete.
	GapIgnored GapStatus = "ignored"
	// GapUnrecoverable means the Provider will never serve those Bars.
	GapUnrecoverable GapStatus = "unrecoverable"
)

var gapStatuses = []GapStatus{GapOpen, GapRepaired, GapIgnored, GapUnrecoverable}

// ParseGapStatus accepts exactly the four canonical statuses.
func ParseGapStatus(s string) (GapStatus, error) {
	for _, st := range gapStatuses {
		if GapStatus(s) == st {
			return st, nil
		}
	}
	return "", fmt.Errorf("unknown gap status %q", s)
}

// String returns the canonical status name.
func (s GapStatus) String() string { return string(s) }

// Gap is a contiguous range inside a Dataset's Coverage where Bars are
// expected but absent.
type Gap struct {
	ID      int64
	Dataset DatasetID
	Range   Range
	Status  GapStatus
	Reason  string
}
