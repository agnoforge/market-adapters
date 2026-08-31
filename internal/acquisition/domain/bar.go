package domain

import (
	"fmt"
	"math/big"
	"time"
)

// Bar is one OHLCV bar in a Dataset, identified by its open_time. Only fully
// closed bars exist; the currently forming bar is never a Bar.
//
// Prices and volume are decimal strings, not floats. They are stored as
// DECIMAL(20,8) and compared exactly, so a value never changes by passing
// through this type.
type Bar struct {
	OpenTime time.Time
	Open     string
	High     string
	Low      string
	Close    string
	Volume   string
}

// Validate reports why a Bar is not usable, or nil when it is. A Provider
// adapter drops and logs the bars this rejects.
//
// A Bar is valid when its open_time is a non-zero UTC instant, every price
// parses as a decimal and is strictly positive, volume parses and is not
// negative, low is no greater than min(open, close) and high is no less than
// max(open, close).
func (b Bar) Validate() error {
	if b.OpenTime.IsZero() {
		return fmt.Errorf("bar: open_time is zero")
	}
	if b.OpenTime.Location() != time.UTC {
		return fmt.Errorf("bar: open_time %s is not UTC (location %s)", b.OpenTime, b.OpenTime.Location())
	}

	open, err := parsePositive("open", b.Open)
	if err != nil {
		return err
	}
	high, err := parsePositive("high", b.High)
	if err != nil {
		return err
	}
	low, err := parsePositive("low", b.Low)
	if err != nil {
		return err
	}
	clos, err := parsePositive("close", b.Close)
	if err != nil {
		return err
	}
	volume, ok := new(big.Rat).SetString(b.Volume)
	if !ok {
		return fmt.Errorf("bar: volume %q is not a decimal number", b.Volume)
	}
	if volume.Sign() < 0 {
		return fmt.Errorf("bar: volume %q is negative", b.Volume)
	}

	body := open
	if clos.Cmp(body) < 0 {
		body = clos
	}
	if low.Cmp(body) > 0 {
		return fmt.Errorf("bar: low %q is above min(open,close) %q", b.Low, body.RatString())
	}

	body = open
	if clos.Cmp(body) > 0 {
		body = clos
	}
	if high.Cmp(body) < 0 {
		return fmt.Errorf("bar: high %q is below max(open,close) %q", b.High, body.RatString())
	}
	return nil
}

// parsePositive parses a decimal price exactly and requires it to be > 0.
func parsePositive(field, s string) (*big.Rat, error) {
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("bar: %s %q is not a decimal number", field, s)
	}
	if v.Sign() <= 0 {
		return nil, fmt.Errorf("bar: %s %q is not positive", field, s)
	}
	return v, nil
}
