package domain_test

import (
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

func bar(open, high, low, clos, vol string) domain.Bar {
	return domain.Bar{
		OpenTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Open:     open,
		High:     high,
		Low:      low,
		Close:    clos,
		Volume:   vol,
	}
}

func TestBarValidate(t *testing.T) {
	tests := []struct {
		name    string
		bar     domain.Bar
		wantErr bool
	}{
		{"normal rising bar", bar("42000.00000000", "42500.10000000", "41900.00000000", "42400.00000000", "13.55000000"), false},
		{"normal falling bar", bar("42400.00000000", "42500.00000000", "41900.00000000", "42000.00000000", "13.55000000"), false},
		{"flat bar", bar("100", "100", "100", "100", "0"), false},
		{"zero volume ok", bar("100", "101", "99", "100", "0"), false},
		{"high equals max(o,c)", bar("100", "105", "95", "105", "1"), false},
		{"low equals min(o,c)", bar("100", "105", "95", "95", "1"), false},
		{"tiny satoshi prices", bar("0.00000001", "0.00000002", "0.00000001", "0.00000002", "0.00000001"), false},

		{"low above open", bar("100", "110", "101", "105", "1"), true},
		{"low above close", bar("105", "110", "101", "100", "1"), true},
		{"high below open", bar("110", "105", "90", "100", "1"), true},
		{"high below close", bar("100", "105", "90", "110", "1"), true},
		{"zero open", bar("0", "110", "0", "100", "1"), true},
		{"zero high", bar("100", "0", "90", "100", "1"), true},
		{"zero low", bar("100", "110", "0", "100", "1"), true},
		{"zero close", bar("100", "110", "90", "0", "1"), true},
		{"negative open", bar("-100", "110", "-100", "100", "1"), true},
		{"negative low", bar("100", "110", "-1", "100", "1"), true},
		{"negative volume", bar("100", "110", "90", "100", "-1"), true},
		{"unparsable open", bar("abc", "110", "90", "100", "1"), true},
		{"unparsable high", bar("100", "", "90", "100", "1"), true},
		{"unparsable low", bar("100", "110", "nine", "100", "1"), true},
		{"unparsable close", bar("100", "110", "90", "1.2.3", "1"), true},
		{"unparsable volume", bar("100", "110", "90", "100", "NaN"), true},
		{"empty open", bar("", "110", "90", "100", "1"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bar.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error for %+v", tt.bar)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil for %+v", err, tt.bar)
			}
		})
	}
}

func TestBarValidateOpenTime(t *testing.T) {
	b := bar("100", "110", "90", "100", "1")

	b.OpenTime = time.Time{}
	if err := b.Validate(); err == nil {
		t.Error("Validate() accepted a zero OpenTime")
	}

	b.OpenTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.FixedZone("UTC+7", 7*3600))
	if err := b.Validate(); err == nil {
		t.Error("Validate() accepted a non-UTC OpenTime")
	}
}

// Exactness: values beyond float64 precision must still compare correctly.
func TestBarValidateIsExact(t *testing.T) {
	b := bar("1.00000000000000001", "1.00000000000000002", "1.00000000000000000", "1.00000000000000001", "1")
	if err := b.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	b.High = "1.00000000000000000" // just below max(open,close)
	if err := b.Validate(); err == nil {
		t.Error("Validate() accepted high below max(open,close) at 17 decimal places; comparison is not exact")
	}
}
