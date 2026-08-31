package httpapi_test

import (
	"net/http"
	"testing"
)

func TestListGaps(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)

	var all []gapJSON
	h.decode(h.do("GET", "/datasets/fake/BTCUSDT/1m/gaps", nil), http.StatusOK, &all)
	if len(all) != 1 {
		t.Fatalf("gaps = %+v, want the one open Gap at minute 4", all)
	}
	got := all[0]
	if got.ID == 0 {
		t.Error("gap carries no id")
	}
	if got.Dataset.Provider != providerName || got.Dataset.Symbol != string(symbol) || got.Dataset.Timeframe != "1m" {
		t.Errorf("dataset = %+v, want the fake Dataset", got.Dataset)
	}
	if got.Range.Start != at(4) || got.Range.End != at(5) {
		t.Errorf("range = %+v, want [%s,%s)", got.Range, at(4), at(5))
	}
	if got.Status != "open" {
		t.Errorf("status = %q, want open", got.Status)
	}

	var open []gapJSON
	h.decode(h.do("GET", "/datasets/fake/BTCUSDT/1m/gaps?status=open", nil), http.StatusOK, &open)
	if len(open) != 1 || open[0].ID != got.ID {
		t.Errorf("?status=open = %+v, want the same single Gap", open)
	}

	res := h.do("GET", "/datasets/fake/BTCUSDT/1m/gaps?status=ignored", nil)
	if body := string(h.body(res)); body != "[]\n" {
		t.Errorf("?status=ignored body = %q, want an empty array", body)
	}
}

func TestListGapsRejects(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("GET", "/datasets/fake/BTCUSDT/1m/gaps?status=maybe", nil), http.StatusBadRequest)
	h.expectError(h.do("GET", "/datasets/fake/BTCUSDT/1w/gaps", nil), http.StatusBadRequest)
}

func TestPatchGap(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)
	gap := h.seededGap()

	var got gapJSON
	h.decode(h.do("PATCH", "/gaps/"+itoa(gap.ID), map[string]string{
		"status": "ignored", "reason": "exchange maintenance",
	}), http.StatusOK, &got)

	if got.ID != gap.ID || got.Status != "ignored" || got.Reason != "exchange maintenance" {
		t.Fatalf("patched gap = %+v, want %d ignored with the reason", got, gap.ID)
	}
	if got.Range.Start != at(4) || got.Range.End != at(5) {
		t.Errorf("range = %+v, want it unchanged", got.Range)
	}

	// Acceptance #2: settling the only intersecting Gap makes the range
	// Complete.
	var completeness struct {
		Complete bool      `json:"complete"`
		Gaps     []gapJSON `json:"gaps"`
	}
	h.decode(h.do("GET", "/datasets/fake/BTCUSDT/1m/complete?start="+at(0)+"&end="+at(10), nil), http.StatusOK, &completeness)
	if !completeness.Complete {
		t.Errorf("complete = false after ignoring the only Gap, want true")
	}
}

func TestPatchGapRejects(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)
	gap := h.seededGap()

	// repaired is the detector's word, never an operator's.
	h.expectError(h.do("PATCH", "/gaps/"+itoa(gap.ID), map[string]string{"status": "repaired"}), http.StatusBadRequest)
	h.expectError(h.do("PATCH", "/gaps/"+itoa(gap.ID), map[string]string{"status": "sortof"}), http.StatusBadRequest)
	h.expectError(h.do("PATCH", "/gaps/"+itoa(gap.ID), `{"status":`), http.StatusBadRequest)
	h.expectError(h.do("PATCH", "/gaps/notanumber", map[string]string{"status": "ignored"}), http.StatusBadRequest)
	h.expectError(h.do("PATCH", "/gaps/999999", map[string]string{"status": "ignored"}), http.StatusNotFound)

	if got := h.seededGap(); got.Status != "open" {
		t.Errorf("status = %q after the refused requests, want it untouched at open", got.Status)
	}
}

func TestRepairGap(t *testing.T) {
	h := newHarness(t)
	h.backfill(0, 10)
	gap := h.seededGap()

	var got struct {
		BackfillID string `json:"backfill_id"`
	}
	h.decode(h.do("POST", "/gaps/"+itoa(gap.ID)+"/repair", nil), http.StatusAccepted, &got)
	if got.BackfillID == "" {
		t.Fatal("202 carried no backfill_id")
	}
	h.wait(got.BackfillID)

	var status struct {
		Range struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"range"`
	}
	h.decode(h.do("GET", "/backfills/"+got.BackfillID, nil), http.StatusOK, &status)
	if status.Range.Start != at(4) || status.Range.End != at(5) {
		t.Errorf("repair range = %+v, want exactly the Gap's [%s,%s)", status.Range, at(4), at(5))
	}
}

func TestRepairGapUnknown(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("POST", "/gaps/999999/repair", nil), http.StatusNotFound)
	h.expectError(h.do("POST", "/gaps/notanumber/repair", nil), http.StatusBadRequest)
}
