package playground

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The page is the embedded file, served at exactly /playground/ — and
// registering it there does not take the other playground routes with it.
func TestThePageAndTheCatalogLoad(t *testing.T) {
	h := newHarness(t)

	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playground/", nil))
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /playground/ = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want a text/html prefix", got)
	}

	body := rec.Body.String()
	onDisk, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if body != string(onDisk) {
		t.Errorf("the served page is not the embedded file byte for byte (%d served, %d on disk)",
			len(body), len(onDisk))
	}
	if !strings.Contains(body, "<script>") {
		t.Error("the page carries no inline <script>")
	}
	if strings.Contains(body, "<script src=") {
		t.Error("the page loads an external script")
	}
	for _, external := range []string{`<link`, `src="http`, `href="http`} {
		if strings.Contains(body, external) {
			t.Errorf("the page reaches outside itself: %q", external)
		}
	}

	// The catalog the page asks for on load is still served: /playground/{$}
	// matches the root alone.
	if status, catalog := h.get("/playground/operations"); status != http.StatusOK {
		t.Errorf("GET /playground/operations = %d, want 200: %s", status, catalog)
	} else if !strings.Contains(string(catalog), `"Start a Backfill"`) {
		t.Errorf("the catalog is missing its operations: %s", catalog)
	}
}

// A path under /playground/ that is not the root is not the page.
func TestThePageDoesNotSwallowTheOtherRoutes(t *testing.T) {
	h := newHarness(t)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playground/traces/0af7651916cd43dd8448eb211c80319c", nil))
	if got := rec.Result().Header.Get("Content-Type"); got != contentTypeJSON {
		t.Errorf("Content-Type = %q, want %q: the page took over /playground/traces/{id}", got, contentTypeJSON)
	}
}

// externalAsset is anything the page would have to fetch from somewhere else.
var externalAsset = regexp.MustCompile(`src="http|href="http|@import|url\(http`)

// There is no Node toolchain and no external asset: one file, no siblings, no
// network.
func TestNoNodeToolchainAndNoExternalAsset(t *testing.T) {
	if _, err := os.Stat("package.json"); !os.IsNotExist(err) {
		t.Errorf("os.Stat(package.json) = %v, want a not-exist error: the playground has no Node toolchain", err)
	}
	page, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if match := externalAsset.Find(page); match != nil {
		t.Errorf("the page loads an external asset: %q", match)
	}
	if string(indexHTML) != string(page) {
		t.Error("the embedded page and index.html differ")
	}
}
