package httpapi_test

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// Every failure is JSON, including the two the mux answers on its own.
func TestUnroutedRequestsAreJSON(t *testing.T) {
	h := newHarness(t)

	t.Run("unknown route", func(t *testing.T) {
		h.expectError(h.do("GET", "/nothing/here", nil), http.StatusNotFound)
	})
	t.Run("known route, wrong method", func(t *testing.T) {
		res := h.do("PUT", "/backfills", `{}`)
		h.expectError(res, http.StatusMethodNotAllowed)
		if allow := res.Header.Get("Allow"); !strings.Contains(allow, "POST") {
			t.Errorf("Allow = %q, want it to name POST", allow)
		}
	})
	t.Run("unknown dataset sub-resource", func(t *testing.T) {
		h.expectError(h.do("GET", "/datasets/fake/BTCUSDT/1m/histogram", nil), http.StatusNotFound)
	})
}

// The adapter depends on the use cases, not on the Store or the Provider
// behind them: no DuckDB and no Binance anywhere in its import graph. Only
// this test file, which needs a second DuckDB to read an export back, imports
// one.
func TestPackageDependsOnNoAdapter(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/agnos/agnoforge/internal/acquisition/adapters/httpapi").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, forbidden := range []string{
		"github.com/agnos/agnoforge/internal/acquisition/adapters/duckdb",
		"github.com/agnos/agnoforge/internal/acquisition/adapters/binance",
	} {
		for _, dep := range strings.Fields(string(out)) {
			if dep == forbidden {
				t.Errorf("httpapi depends on %s", forbidden)
			}
		}
	}
}
