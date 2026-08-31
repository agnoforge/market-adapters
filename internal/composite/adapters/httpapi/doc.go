// Package httpapi is the inbound REST adapter over the Composite Market
// Dataset use cases in internal/composite/app. It follows the acquisition
// adapter's conventions — the standard library's pattern mux, the {"error": …}
// body, RFC3339 instants in UTC — so the two resources read as one API.
package httpapi
