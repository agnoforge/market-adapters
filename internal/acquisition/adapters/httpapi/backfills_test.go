package httpapi_test

import (
	"net/http"
	"testing"
)

func TestListProviders(t *testing.T) {
	h := newHarness(t)

	var got []struct {
		Name       string   `json:"name"`
		Timeframes []string `json:"timeframes"`
	}
	h.decode(h.do("GET", "/providers", nil), http.StatusOK, &got)

	if len(got) != 1 || got[0].Name != providerName {
		t.Fatalf("providers = %+v, want the single fake Provider", got)
	}
	want := []string{"1m", "1h"}
	if len(got[0].Timeframes) != len(want) {
		t.Fatalf("timeframes = %v, want %v", got[0].Timeframes, want)
	}
	for i, tf := range want {
		if got[0].Timeframes[i] != tf {
			t.Errorf("timeframe %d = %q, want %q", i, got[0].Timeframes[i], tf)
		}
	}
}

func TestListProvidersRejectsOtherMethods(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("POST", "/providers", `{}`), http.StatusMethodNotAllowed)
}

func TestStartBackfill(t *testing.T) {
	h := newHarness(t)

	res := h.do("POST", "/backfills", map[string]string{
		"provider": providerName, "symbol": string(symbol), "timeframe": "1m",
		"start": at(0), "end": at(10),
	})
	var started struct {
		ID             string `json:"id"`
		EffectiveRange struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"effective_range"`
	}
	h.decode(res, http.StatusAccepted, &started)

	if started.ID == "" {
		t.Error("202 carried no backfill id")
	}
	if started.EffectiveRange.Start != at(0) || started.EffectiveRange.End != at(10) {
		t.Errorf("effective_range = %+v, want [%s,%s)", started.EffectiveRange, at(0), at(10))
	}
	h.wait(started.ID)
}

// A date-only bound is accepted, so an operator can ask for a whole day
// without spelling an RFC3339 instant.
func TestStartBackfillAcceptsDateOnlyBounds(t *testing.T) {
	h := newHarness(t)

	res := h.do("POST", "/backfills", map[string]string{
		"provider": providerName, "symbol": string(symbol), "timeframe": "1m",
		"start": "2024-01-01", "end": "2024-01-02",
	})
	var started struct {
		ID             string `json:"id"`
		EffectiveRange struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"effective_range"`
	}
	h.decode(res, http.StatusAccepted, &started)

	if started.EffectiveRange.Start != "2024-01-01T00:00:00Z" || started.EffectiveRange.End != "2024-01-02T00:00:00Z" {
		t.Errorf("effective_range = %+v, want the whole day of 2024-01-01", started.EffectiveRange)
	}
	h.wait(started.ID)
}

func TestStartBackfillRejects(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"malformed JSON", `{"provider":`},
		{"unknown provider", map[string]string{"provider": "nope", "symbol": string(symbol), "timeframe": "1m", "start": at(0), "end": at(10)}},
		{"unknown symbol", map[string]string{"provider": providerName, "symbol": string(unknownSymbol), "timeframe": "1m", "start": at(0), "end": at(10)}},
		{"unsupported timeframe", map[string]string{"provider": providerName, "symbol": string(symbol), "timeframe": "1w", "start": at(0), "end": at(10)}},
		{"timeframe the provider does not offer", map[string]string{"provider": providerName, "symbol": string(symbol), "timeframe": "4h", "start": at(0), "end": at(10)}},
		{"unparseable start", map[string]string{"provider": providerName, "symbol": string(symbol), "timeframe": "1m", "start": "yesterday", "end": at(10)}},
		{"missing end", map[string]string{"provider": providerName, "symbol": string(symbol), "timeframe": "1m", "start": at(0)}},
		{"start after end", map[string]string{"provider": providerName, "symbol": string(symbol), "timeframe": "1m", "start": at(10), "end": at(0)}},
		{"empty range", map[string]string{"provider": providerName, "symbol": string(symbol), "timeframe": "1m", "start": at(3), "end": at(3)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.expectError(h.do("POST", "/backfills", c.body), http.StatusBadRequest)
		})
	}
}

// A second Backfill of a Dataset that already has one running is refused,
// and so is a Repair of a Gap in it: one running Backfill per Dataset.
func TestBackfillConflict(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)
	gap := h.seededGap()

	release := h.provider.hold()
	res := h.do("POST", "/backfills", map[string]string{
		"provider": providerName, "symbol": string(symbol), "timeframe": "1m",
		"start": at(0), "end": at(10),
	})
	var started struct {
		ID string `json:"id"`
	}
	h.decode(res, http.StatusAccepted, &started)

	h.expectError(h.do("POST", "/backfills", map[string]string{
		"provider": providerName, "symbol": string(symbol), "timeframe": "1m",
		"start": at(0), "end": at(10),
	}), http.StatusConflict)

	h.expectError(h.do("POST", "/gaps/"+itoa(gap.ID)+"/repair", nil), http.StatusConflict)

	release()
	h.wait(started.ID)
}

func TestShowBackfill(t *testing.T) {
	h := newHarness(t)
	id := h.backfill(0, 10)

	var got struct {
		ID      string `json:"id"`
		Dataset struct {
			Provider  string `json:"provider"`
			Symbol    string `json:"symbol"`
			Timeframe string `json:"timeframe"`
		} `json:"dataset"`
		Range struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"range"`
		State          string  `json:"state"`
		BarsDownloaded int64   `json:"bars_downloaded"`
		Position       *string `json:"position"`
		LastError      string  `json:"last_error"`
	}
	h.decode(h.do("GET", "/backfills/"+id, nil), http.StatusOK, &got)

	if got.ID != id {
		t.Errorf("id = %q, want %q", got.ID, id)
	}
	if got.Dataset.Provider != providerName || got.Dataset.Symbol != string(symbol) || got.Dataset.Timeframe != "1m" {
		t.Errorf("dataset = %+v, want the fake Dataset", got.Dataset)
	}
	if got.Range.Start != at(0) || got.Range.End != at(10) {
		t.Errorf("range = %+v, want [%s,%s)", got.Range, at(0), at(10))
	}
	if got.State != "completed" {
		t.Errorf("state = %q, want completed", got.State)
	}
	if got.BarsDownloaded != 9 {
		t.Errorf("bars_downloaded = %d, want 9", got.BarsDownloaded)
	}
	if got.Position == nil || *got.Position != at(9) {
		t.Errorf("position = %v, want %s", got.Position, at(9))
	}
	if got.LastError != "" {
		t.Errorf("last_error = %q, want empty", got.LastError)
	}
}

func TestShowBackfillUnknown(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("GET", "/backfills/nosuchbackfill", nil), http.StatusNotFound)
}

func TestCancelBackfill(t *testing.T) {
	h := newHarness(t)

	release := h.provider.hold()
	res := h.do("POST", "/backfills", map[string]string{
		"provider": providerName, "symbol": string(symbol), "timeframe": "1m",
		"start": at(0), "end": at(10),
	})
	var started struct {
		ID string `json:"id"`
	}
	h.decode(res, http.StatusAccepted, &started)

	var cancelled struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	h.decode(h.do("DELETE", "/backfills/"+started.ID, nil), http.StatusAccepted, &cancelled)
	if cancelled.ID != started.ID {
		t.Errorf("id = %q, want %q", cancelled.ID, started.ID)
	}

	release()
	if status, ok := h.svc.Wait(backfillID(started.ID)); !ok || string(status.State) != "cancelled" {
		t.Fatalf("final state = %q (registered %t), want cancelled", status.State, ok)
	}
}

func TestCancelBackfillUnknown(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("DELETE", "/backfills/nosuchbackfill", nil), http.StatusNotFound)
}
