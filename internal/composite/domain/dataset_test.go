package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

var (
	start = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end   = time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
)

// validConfig is the smallest declaration this context accepts: one 1-minute
// base source of the dataset's own Instrument over a fixed range.
func validConfig() domain.Config {
	return domain.Config{
		Instrument: "BTC/USD",
		Base: domain.Source{
			Instrument: "BTC/USD",
			Provider:   "binance",
			Symbol:     acq.Symbol("BTCUSDT"),
			Timeframe:  domain.TF1m,
		},
		CatchUp:        domain.CatchUp{Kind: domain.CatchUpBase},
		RequestedStart: start,
		RequestedEnd:   domain.FixedEnd(end),
		Timeframes:     []domain.Timeframe{domain.TF1h},
		Mode:           domain.ModeStrict,
	}
}

func TestParseNameAcceptsKebabCaseSlugs(t *testing.T) {
	for _, s := range []string{"btc", "btc-usd", "btc-usd-1m-composite", "a1", "2024-history"} {
		if _, err := domain.ParseName(s); err != nil {
			t.Errorf("ParseName(%q) = %v, want a name", s, err)
		}
	}
}

func TestParseNameRejectsAnythingElse(t *testing.T) {
	for _, s := range []string{"", "BTC-USD", "btc_usd", "btc--usd", "-btc", "btc-", "btc usd", "btc/usd", "btc@1", "ünicode"} {
		if _, err := domain.ParseName(s); !errors.Is(err, domain.ErrInvalidConfig) {
			t.Errorf("ParseName(%q) = %v, want ErrInvalidConfig", s, err)
		}
	}
}

func TestParseInstrumentRejectsBlankAndPadded(t *testing.T) {
	if _, err := domain.ParseInstrument("BTC/USD"); err != nil {
		t.Fatalf("ParseInstrument(BTC/USD) = %v, want an instrument", err)
	}
	for _, s := range []string{"", " BTC/USD", "BTC/USD ", "BTC\nUSD"} {
		if _, err := domain.ParseInstrument(s); !errors.Is(err, domain.ErrInvalidConfig) {
			t.Errorf("ParseInstrument(%q) = %v, want ErrInvalidConfig", s, err)
		}
	}
}

func TestParseTimeframeKnowsCalendarFramesAndRejectsTheRest(t *testing.T) {
	for _, s := range []string{"1m", "5m", "15m", "30m", "1h", "4h", "1d", "1w", "1M"} {
		tf, err := domain.ParseTimeframe(s)
		if err != nil {
			t.Errorf("ParseTimeframe(%q) = %v, want a timeframe", s, err)
			continue
		}
		if tf.String() != s {
			t.Errorf("ParseTimeframe(%q).String() = %q", s, tf)
		}
	}
	for _, s := range []string{"", "2m", "1y", "1W", "1mo", "3d", "hour"} {
		if _, err := domain.ParseTimeframe(s); !errors.Is(err, domain.ErrInvalidConfig) {
			t.Errorf("ParseTimeframe(%q) = %v, want ErrInvalidConfig", s, err)
		}
	}
}

func TestParseModeDefaultsToStrict(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want domain.Mode
	}{
		{"", domain.ModeStrict},
		{"strict", domain.ModeStrict},
		{"research", domain.ModeResearch},
	} {
		got, err := domain.ParseMode(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseMode(%q) = %q, %v, want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := domain.ParseMode("loose"); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Errorf(`ParseMode("loose") = %v, want ErrInvalidConfig`, err)
	}
}

func TestValidateAcceptsAWellFormedConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateAcceptsANowEndWhateverTheStart(t *testing.T) {
	cfg := validConfig()
	cfg.RequestedEnd = domain.NowEnd()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with a now end = %v, want nil", err)
	}
	if got := cfg.RequestedEnd.String(); got != "now" {
		t.Fatalf("RequestedEnd.String() = %q, want \"now\"", got)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		edit func(*domain.Config)
	}{
		{"a blank instrument", func(c *domain.Config) { c.Instrument, c.Base.Instrument = "", "" }},
		{"an end equal to the start", func(c *domain.Config) { c.RequestedEnd = domain.FixedEnd(c.RequestedStart) }},
		{"an end before the start", func(c *domain.Config) { c.RequestedEnd = domain.FixedEnd(c.RequestedStart.Add(-time.Hour)) }},
		{"a missing start", func(c *domain.Config) { c.RequestedStart = time.Time{} }},
		{"an unknown materialized timeframe", func(c *domain.Config) { c.Timeframes = []domain.Timeframe{"2m"} }},
		{"1m as a materialized timeframe", func(c *domain.Config) { c.Timeframes = []domain.Timeframe{domain.TF1m} }},
		{"a base source with no provider", func(c *domain.Config) { c.Base.Provider = "" }},
		{"a base source with no symbol", func(c *domain.Config) { c.Base.Symbol = "" }},
		{"a base source that is not 1m", func(c *domain.Config) { c.Base.Timeframe = domain.TF1h }},
		{"a base source of another instrument", func(c *domain.Config) { c.Base.Instrument = "ETH/USD" }},
		{"a catch-up source of another instrument", func(c *domain.Config) {
			c.CatchUp = domain.CatchUp{Kind: domain.CatchUpSource, Source: domain.Source{
				Instrument: "ETH/USD", Provider: "coinbase", Symbol: "ETH-USD", Timeframe: domain.TF1m,
			}}
		}},
		{"an unknown catch-up kind", func(c *domain.Config) { c.CatchUp = domain.CatchUp{Kind: "maybe"} }},
		{"an unknown mode", func(c *domain.Config) { c.Mode = "loose" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.edit(&cfg)
			if err := cfg.Validate(); !errors.Is(err, domain.ErrInvalidConfig) {
				t.Fatalf("Validate() = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestValidateAcceptsACatchUpSourceOfTheSameInstrument(t *testing.T) {
	cfg := validConfig()
	cfg.CatchUp = domain.CatchUp{Kind: domain.CatchUpSource, Source: domain.Source{
		Instrument: "BTC/USD", Provider: "coinbase", Symbol: "BTC-USD", Timeframe: domain.TF1m,
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestNormalizeSortsAndDeduplicatesTimeframes(t *testing.T) {
	cfg := validConfig()
	cfg.Timeframes = []domain.Timeframe{domain.TF1M, domain.TF1h, domain.TF1w, domain.TF1h, domain.TF5m}
	got := cfg.Normalize().Timeframes
	want := []domain.Timeframe{domain.TF5m, domain.TF1h, domain.TF1w, domain.TF1M}
	if len(got) != len(want) {
		t.Fatalf("Normalize().Timeframes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Normalize().Timeframes = %v, want %v", got, want)
		}
	}
}

func TestAfterEditKeepsADraftADraftAndStalesEverythingBuilt(t *testing.T) {
	if got := domain.StateDraft.AfterEdit(); got != domain.StateDraft {
		t.Errorf("draft.AfterEdit() = %q, want draft", got)
	}
	for _, s := range []domain.State{domain.StateReady, domain.StateFailed, domain.StateStale, domain.StateBuilding} {
		if got := s.AfterEdit(); got != domain.StateStale {
			t.Errorf("%q.AfterEdit() = %q, want stale", s, got)
		}
	}
}
