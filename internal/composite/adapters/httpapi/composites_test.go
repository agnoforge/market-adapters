package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/agnos/agnoforge/internal/composite/domain"
)

// --- Create -----------------------------------------------------------------

func TestCreateDeclaresADraftAndAnswersTheWholeDeclaration(t *testing.T) {
	h := newHarness(t)
	created := h.create(named("btc-usd"))

	if created.Name != "btc-usd" {
		t.Errorf("name = %q, want btc-usd", created.Name)
	}
	if created.State != "draft" {
		t.Errorf("state = %q, want draft — declaring builds nothing", created.State)
	}
	if created.Instrument != instrument {
		t.Errorf("instrument = %q, want %q", created.Instrument, instrument)
	}
	want := sourceJSON{Instrument: instrument, Provider: "binance", Symbol: baseSymbol, Timeframe: "1m"}
	if created.Base != want {
		t.Errorf("base = %+v, want %+v", created.Base, want)
	}
	if created.CatchUp.Kind != "base" {
		t.Errorf("catch-up kind = %q, want base — the default is the base provider", created.CatchUp.Kind)
	}
	if created.RequestedStart != "2024-01-01T00:00:00Z" || created.RequestedEnd != "2024-02-01T00:00:00Z" {
		t.Errorf("requested range = %s .. %s, want the declared one", created.RequestedStart, created.RequestedEnd)
	}
	// The materialized set comes back ascending, whatever order it was sent in.
	if strings.Join(created.Timeframes, ",") != "5m,1h" {
		t.Errorf("timeframes = %v, want [5m 1h]", created.Timeframes)
	}
	if created.Mode != "strict" {
		t.Errorf("mode = %q, want strict", created.Mode)
	}
	if created.CreatedAt != "2026-08-31T12:00:00Z" || created.UpdatedAt != created.CreatedAt {
		t.Errorf("created/updated = %s / %s, want the clock's instant", created.CreatedAt, created.UpdatedAt)
	}
}

func TestCreateAcceptsAnEndOfNow(t *testing.T) {
	h := newHarness(t)
	body := named("btc-usd-now")
	body["requested_end"] = "now"
	created := h.create(body)
	if created.RequestedEnd != "now" {
		t.Fatalf("requested end = %q, want now", created.RequestedEnd)
	}
	// It survives storage: a `now` end is a declaration, not a resolved
	// instant, and reading it back must not turn it into one.
	var got compositeJSON
	h.decode(h.do("GET", "/composites/btc-usd-now", nil), http.StatusOK, &got)
	if got.RequestedEnd != "now" {
		t.Fatalf("requested end read back = %q, want now", got.RequestedEnd)
	}
}

func TestCreateAcceptsAnExplicitCatchUpProvider(t *testing.T) {
	h := newHarness(t)
	body := named("btc-usd-catch-up")
	body["catch_up"] = map[string]any{
		"kind": "source", "instrument": instrument,
		"provider": "coinbase", "symbol": "BTC-USD", "timeframe": "1m",
	}
	created := h.create(body)
	if created.CatchUp.Kind != "source" || created.CatchUp.Provider != "coinbase" || created.CatchUp.Symbol != "BTC-USD" {
		t.Fatalf("catch-up = %+v, want the declared coinbase source", created.CatchUp)
	}
}

func TestCreateAcceptsCatchUpNone(t *testing.T) {
	h := newHarness(t)
	body := named("btc-usd-no-catch-up")
	body["catch_up"] = map[string]any{"kind": "none"}
	if got := h.create(body).CatchUp.Kind; got != "none" {
		t.Fatalf("catch-up kind = %q, want none", got)
	}
}

func TestCreateAcceptsAnEmptyMaterializedSet(t *testing.T) {
	h := newHarness(t)
	body := named("btc-usd-1m-only")
	body["timeframes"] = []string{}
	if got := h.create(body).Timeframes; len(got) != 0 {
		t.Fatalf("timeframes = %v, want none", got)
	}
}

func TestCreateDefaultsAnOmittedModeToStrict(t *testing.T) {
	h := newHarness(t)
	body := named("btc-usd-default-mode")
	delete(body, "mode")
	if got := h.create(body).Mode; got != "strict" {
		t.Fatalf("mode = %q, want strict — a dataset never becomes research by omission", got)
	}
}

func TestCreateAcceptsResearchMode(t *testing.T) {
	h := newHarness(t)
	body := named("btc-usd-research")
	body["mode"] = "research"
	if got := h.create(body).Mode; got != "research" {
		t.Fatalf("mode = %q, want research", got)
	}
}

func TestCreateRejects(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"a name that is not a kebab-case slug", func(b map[string]any) { b["name"] = "BTC_USD" }},
		{"a missing name", func(b map[string]any) { delete(b, "name") }},
		{"an unknown materialized timeframe", func(b map[string]any) { b["timeframes"] = []string{"2m"} }},
		{"1m as a materialized timeframe", func(b map[string]any) { b["timeframes"] = []string{"1m"} }},
		{"an end equal to the start", func(b map[string]any) { b["requested_end"] = b["requested_start"] }},
		{"an end before the start", func(b map[string]any) { b["requested_end"] = "2023-12-01T00:00:00Z" }},
		{"an unspellable start", func(b map[string]any) { b["requested_start"] = "last tuesday" }},
		{"a missing instrument", func(b map[string]any) { delete(b, "instrument") }},
		{"a base source of another instrument", func(b map[string]any) {
			b["base"] = map[string]any{
				"instrument": "ETH/USD", "provider": "binance", "symbol": "ETHUSDT", "timeframe": "1m",
			}
		}},
		{"a catch-up source of another instrument", func(b map[string]any) {
			b["catch_up"] = map[string]any{
				"kind": "source", "instrument": "ETH/USD",
				"provider": "coinbase", "symbol": "ETH-USD", "timeframe": "1m",
			}
		}},
		{"a base source that is not 1m", func(b map[string]any) {
			b["base"] = map[string]any{
				"instrument": instrument, "provider": "binance", "symbol": baseSymbol, "timeframe": "1h",
			}
		}},
		{"a base source with no provider", func(b map[string]any) {
			b["base"] = map[string]any{"instrument": instrument, "symbol": baseSymbol, "timeframe": "1m"}
		}},
		{"an unknown mode", func(b map[string]any) { b["mode"] = "loose" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			body := named("btc-usd")
			tc.edit(body)
			h.expectError(h.do("POST", "/composites", body), http.StatusBadRequest)

			// A refused declaration left nothing behind.
			var all []compositeJSON
			h.decode(h.do("GET", "/composites", nil), http.StatusOK, &all)
			if len(all) != 0 {
				t.Fatalf("a refused declaration was stored: %+v", all)
			}
		})
	}
}

func TestCreateRejectsAMalformedBody(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("POST", "/composites", "{not json"), http.StatusBadRequest)
}

func TestCreateRefusesANameAlreadyTaken(t *testing.T) {
	h := newHarness(t)
	h.create(named("btc-usd"))

	second := named("btc-usd")
	second["mode"] = "research"
	message := h.expectError(h.do("POST", "/composites", second), http.StatusConflict)
	if !strings.Contains(message, "btc-usd") {
		t.Errorf("conflict message %q does not name the dataset", message)
	}

	// The first declaration is untouched by the refused one.
	var got compositeJSON
	h.decode(h.do("GET", "/composites/btc-usd", nil), http.StatusOK, &got)
	if got.Mode != "strict" {
		t.Fatalf("mode = %q, want the first declaration's strict", got.Mode)
	}
}

// --- Get and list -----------------------------------------------------------

func TestGetAnswersTheFullConfigAndTheState(t *testing.T) {
	h := newHarness(t)
	created := h.create(named("btc-usd"))
	var got compositeJSON
	h.decode(h.do("GET", "/composites/btc-usd", nil), http.StatusOK, &got)
	assertSame(t, got, created, "the declaration that was created")
}

func TestGetOfAnUnknownDatasetIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("GET", "/composites/no-such-dataset", nil), http.StatusNotFound)
}

func TestGetOfAnImpossibleNameIsABadRequest(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("GET", "/composites/NOT_A_SLUG", nil), http.StatusBadRequest)
}

func TestListAnswersEveryDatasetOrderedByName(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"eth-usd", "btc-usd", "sol-usd"} {
		h.create(named(name))
	}
	var all []compositeJSON
	h.decode(h.do("GET", "/composites", nil), http.StatusOK, &all)
	var names []string
	for _, d := range all {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "btc-usd,eth-usd,sol-usd" {
		t.Fatalf("list = %v, want the three names ordered", names)
	}
}

func TestListOfNothingIsAnEmptyArray(t *testing.T) {
	h := newHarness(t)
	res := h.do("GET", "/composites", nil)
	var all []compositeJSON
	h.decode(res, http.StatusOK, &all)
	if all == nil || len(all) != 0 {
		t.Fatalf("list = %v, want []", all)
	}
}

// --- Edit -------------------------------------------------------------------

func TestEditingADraftReplacesTheConfigAndLeavesItADraft(t *testing.T) {
	h := newHarness(t)
	h.create(named("btc-usd"))
	h.tick()

	body := declaration()
	body["mode"] = "research"
	body["timeframes"] = []string{"1d", "1w"}
	body["requested_end"] = "now"

	var edited compositeJSON
	h.decode(h.do("PUT", "/composites/btc-usd", body), http.StatusOK, &edited)
	if edited.State != "draft" {
		t.Errorf("state = %q, want draft — there is nothing built to diverge from", edited.State)
	}
	if edited.Mode != "research" || edited.RequestedEnd != "now" {
		t.Errorf("edited config = %+v, want the one that was sent", edited)
	}
	if strings.Join(edited.Timeframes, ",") != "1d,1w" {
		t.Errorf("timeframes = %v, want [1d 1w]", edited.Timeframes)
	}
	if edited.CreatedAt != "2026-08-31T12:00:00Z" || edited.UpdatedAt != "2026-08-31T13:00:00Z" {
		t.Errorf("created/updated = %s / %s, want the create and the edit instants", edited.CreatedAt, edited.UpdatedAt)
	}

	var got compositeJSON
	h.decode(h.do("GET", "/composites/btc-usd", nil), http.StatusOK, &got)
	assertSame(t, got, edited, "the edited declaration")
}

func TestEditingABuiltDatasetMarksItStale(t *testing.T) {
	for _, built := range []domain.State{domain.StateReady, domain.StateFailed} {
		t.Run(string(built), func(t *testing.T) {
			h := newHarness(t)
			h.create(named("btc-usd"))
			h.build("btc-usd", built)

			body := declaration()
			body["mode"] = "research"
			var edited compositeJSON
			h.decode(h.do("PUT", "/composites/btc-usd", body), http.StatusOK, &edited)
			if edited.State != "stale" {
				t.Fatalf("state = %q, want stale — what is served no longer matches what is declared", edited.State)
			}
		})
	}
}

func TestEditRejectsAnInvalidConfigAndChangesNothing(t *testing.T) {
	h := newHarness(t)
	created := h.create(named("btc-usd"))

	body := declaration()
	body["timeframes"] = []string{"2m"}
	h.expectError(h.do("PUT", "/composites/btc-usd", body), http.StatusBadRequest)

	var got compositeJSON
	h.decode(h.do("GET", "/composites/btc-usd", nil), http.StatusOK, &got)
	assertSame(t, got, created, "the untouched declaration")
}

func TestEditOfAnUnknownDatasetIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("PUT", "/composites/no-such-dataset", declaration()), http.StatusNotFound)
}

// --- Delete -----------------------------------------------------------------

func TestDeleteRemovesTheDefinition(t *testing.T) {
	h := newHarness(t)
	created := h.create(named("btc-usd"))
	h.create(named("eth-usd"))

	var deleted compositeJSON
	h.decode(h.do("DELETE", "/composites/btc-usd", nil), http.StatusOK, &deleted)
	assertSame(t, deleted, created, "the declaration it removed")
	h.expectError(h.do("GET", "/composites/btc-usd", nil), http.StatusNotFound)

	// Only the one dataset went.
	var all []compositeJSON
	h.decode(h.do("GET", "/composites", nil), http.StatusOK, &all)
	if len(all) != 1 || all[0].Name != "eth-usd" {
		t.Fatalf("list after delete = %+v, want only eth-usd", all)
	}
}

func TestDeleteOfAnUnknownDatasetIsNotFound(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("DELETE", "/composites/no-such-dataset", nil), http.StatusNotFound)
}

// --- Routing ----------------------------------------------------------------

func TestUnknownRoutesAndMethodsAnswerTheJSONError(t *testing.T) {
	h := newHarness(t)
	h.create(named("btc-usd"))
	h.expectError(h.do("GET", "/composites/btc-usd/segments", nil), http.StatusNotFound)
	h.expectError(h.do("POST", "/composites/btc-usd", declaration()), http.StatusMethodNotAllowed)
	h.expectError(h.do("DELETE", "/composites", nil), http.StatusMethodNotAllowed)
}
