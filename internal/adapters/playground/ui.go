package playground

import (
	_ "embed"
	"net/http"
)

// indexHTML is the whole playground UI: one file, inline style and inline
// script, no build step and nothing fetched from anywhere else. It is
// compiled into the binary, so `agnoforge serve` is the only process there
// ever is.
//
//go:embed index.html
var indexHTML []byte

// contentTypeHTML is what the page is served as. The charset is spelled out
// because the file is written as UTF-8 and a browser should not have to
// guess.
const contentTypeHTML = "text/html; charset=utf-8"

// showIndex answers GET /playground/ with the embedded page.
func showIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(http.StatusOK)
	w.Write(indexHTML)
}
