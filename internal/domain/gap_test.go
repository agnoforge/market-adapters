package domain_test

import (
	"testing"

	"github.com/agnos/agnoforge/internal/domain"
)

func TestParseGapStatus(t *testing.T) {
	tests := []struct {
		in   string
		want domain.GapStatus
	}{
		{"open", domain.GapOpen},
		{"repaired", domain.GapRepaired},
		{"ignored", domain.GapIgnored},
		{"unrecoverable", domain.GapUnrecoverable},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := domain.ParseGapStatus(tt.in)
			if err != nil {
				t.Fatalf("ParseGapStatus(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
			if got.String() != tt.in {
				t.Errorf("String() = %q, want %q", got.String(), tt.in)
			}
		})
	}
}

func TestParseGapStatusRejects(t *testing.T) {
	for _, in := range []string{"", "OPEN", "closed", "fixed", "hole", "missing"} {
		t.Run(in, func(t *testing.T) {
			if _, err := domain.ParseGapStatus(in); err == nil {
				t.Errorf("ParseGapStatus(%q) = nil error, want error", in)
			}
		})
	}
}

func TestGapCarriesDatasetAndRange(t *testing.T) {
	g := domain.Gap{
		ID:      7,
		Dataset: domain.DatasetID{Provider: "binance", Symbol: "BTCUSDT", Timeframe: "1m"},
		Range:   rg(2, 4),
		Status:  domain.GapOpen,
		Reason:  "provider returned no bars",
	}
	if g.Dataset.String() != "binance/BTCUSDT/1m" {
		t.Errorf("Dataset.String() = %q", g.Dataset.String())
	}
	if g.Range.IsEmpty() {
		t.Error("Gap range should not be empty")
	}
}
