package binance

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// This file is the only place in the repository that speaks Binance's wire
// vocabulary. Everything it produces is domain language.

// klinesPath is the REST endpoint that serves bars.
const klinesPath = "/api/v3/klines"

// codeInvalidSymbol is the Binance error code for a symbol it does not know.
// It arrives with HTTP 400 and is permanent.
const codeInvalidSymbol = -1121

// kline is one row of the klines response. Binance sends each row as a
// heterogeneous JSON array:
//
//	[openTime, open, high, low, close, volume, closeTime, ...]
//
// with the numbers as JSON numbers and the prices as strings.
type kline struct {
	openTime  int64
	open      string
	high      string
	low       string
	closePx   string
	volume    string
	closeTime int64
}

// klineFields is the smallest row Binance sends that still carries everything
// a Bar needs.
const klineFields = 7

// decodeKlines parses a klines response body into rows, in the order the
// Provider sent them.
func decodeKlines(body []byte) ([]kline, error) {
	var raw [][]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("binance: decode klines: %w", err)
	}
	out := make([]kline, 0, len(raw))
	for i, row := range raw {
		if len(row) < klineFields {
			return nil, fmt.Errorf("binance: decode klines: row %d has %d fields, want at least %d", i, len(row), klineFields)
		}
		var k kline
		nums := []struct {
			dst *int64
			at  int
		}{{&k.openTime, 0}, {&k.closeTime, 6}}
		for _, n := range nums {
			if err := json.Unmarshal(row[n.at], n.dst); err != nil {
				return nil, fmt.Errorf("binance: decode klines: row %d field %d: %w", i, n.at, err)
			}
		}
		strs := []struct {
			dst *string
			at  int
		}{{&k.open, 1}, {&k.high, 2}, {&k.low, 3}, {&k.closePx, 4}, {&k.volume, 5}}
		for _, s := range strs {
			if err := json.Unmarshal(row[s.at], s.dst); err != nil {
				return nil, fmt.Errorf("binance: decode klines: row %d field %d: %w", i, s.at, err)
			}
		}
		out = append(out, k)
	}
	return out, nil
}

// bar renders the row in domain language. The decimal strings pass through
// untouched, exactly as the Provider spelled them.
func (k kline) bar() domain.Bar {
	return domain.Bar{
		OpenTime: time.UnixMilli(k.openTime).UTC(),
		Open:     k.open,
		High:     k.high,
		Low:      k.low,
		Close:    k.closePx,
		Volume:   k.volume,
	}
}

// apiError is the JSON body Binance returns with a 4xx: {"code":…,"msg":…}.
type apiError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// decodeAPIError reports the Binance error code carried by an error response,
// and whether the body was one.
func decodeAPIError(body []byte) (apiError, bool) {
	var e apiError
	if err := json.Unmarshal(body, &e); err != nil || e.Code == 0 {
		return apiError{}, false
	}
	return e, true
}
