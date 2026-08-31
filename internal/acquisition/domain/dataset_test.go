package domain_test

import (
	"testing"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

func TestDatasetIDString(t *testing.T) {
	d := domain.DatasetID{Provider: "binance", Symbol: "BTCUSDT", Timeframe: "1m"}
	if got, want := d.String(), "binance/BTCUSDT/1m"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
