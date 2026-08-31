package binance_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
)

// TestMain guards the rule that this package's tests never touch the real
// Binance. Every request in the binary — including one made through a
// default http.Client, which has no Transport of its own — has to land on
// loopback, which is where httptest servers live.
func TestMain(m *testing.M) {
	http.DefaultTransport = &loopbackOnly{next: http.DefaultTransport}
	os.Exit(m.Run())
}

// loopbackOnly refuses any request that would leave the machine.
type loopbackOnly struct{ next http.RoundTripper }

func (l *loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("test tried to reach the network: %s", r.URL)
	}
	return l.next.RoundTrip(r)
}
