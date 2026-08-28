package main

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The defaults are the ones the spec names, and each is overridden by its own
// environment variable.
func TestServeConfigDefaults(t *testing.T) {
	t.Setenv("BINANCE_BASE_URL", "")

	cfg := serveConfigFrom(envFunc(nil))
	if cfg.DBPath != "agnoforge.duckdb" || cfg.Listen != ":8080" {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.BinanceBaseURL != "https://api.binance.com" {
		t.Errorf("binance base URL = %q, want the public endpoint", cfg.BinanceBaseURL)
	}

	cfg = serveConfigFrom(envFunc(map[string]string{
		"AGNOFORGE_DB_PATH": "/tmp/x.duckdb",
		"AGNOFORGE_LISTEN":  "127.0.0.1:9999",
		"BINANCE_BASE_URL":  "http://127.0.0.1:1",
	}))
	if cfg.DBPath != "/tmp/x.duckdb" || cfg.Listen != "127.0.0.1:9999" || cfg.BinanceBaseURL != "http://127.0.0.1:1" {
		t.Errorf("overrides = %+v", cfg)
	}
}

// `agnoforge serve` binds AGNOFORGE_LISTEN, serves the API there, and stops
// gracefully on SIGINT.
func TestServeListensOnConfiguredAddress(t *testing.T) {
	fake := newFakeBinance(t, 1)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	env := envFunc(map[string]string{
		"AGNOFORGE_LISTEN":  "127.0.0.1:0",
		"AGNOFORGE_DB_PATH": filepath.Join(t.TempDir(), "agnoforge.duckdb"),
		"BINANCE_BASE_URL":  fake.url(),
	})

	exit := make(chan int, 1)
	go func() {
		defer writer.Close()
		exit <- run([]string{"serve"}, writer, io.Discard, env)
	}()

	announced, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the address serve announced: %v", err)
	}
	addr, ok := strings.CutPrefix(strings.TrimSpace(announced), "listening on ")
	if !ok {
		t.Fatalf("serve announced %q, want a %q line", announced, "listening on <addr>")
	}
	go io.Copy(io.Discard, reader)

	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		}
	}
	t.Cleanup(stop)

	res, err := http.Get("http://" + addr + "/providers")
	if err != nil {
		t.Fatalf("GET /providers: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /providers = %d, want 200", res.StatusCode)
	}
	if !strings.Contains(string(body), `"binance"`) {
		t.Errorf("GET /providers = %s, want the Binance Provider", body)
	}

	stop()
	select {
	case code := <-exit:
		if code != 0 {
			t.Errorf("serve exited %d, want 0", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not stop after SIGINT")
	}
}

// service is one in-process agnoforge serving on a loopback port.
type service struct {
	baseURL string
}

// startService wires and starts the real service on 127.0.0.1:0, the same way
// `agnoforge serve` does, and stops it when the test ends.
func startService(t *testing.T, env map[string]string) *service {
	t.Helper()
	cfg := serveConfigFrom(envFunc(env))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveOn(ctx, listener, cfg, io.Discard, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("the service did not stop")
		}
	})
	return &service{baseURL: "http://" + listener.Addr().String()}
}

// This is the only package that may name a concrete adapter, and it names all
// three of them.
func TestCommandWiresEveryAdapter(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/agnos/agnoforge/cmd/agnoforge").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	deps := map[string]bool{}
	for _, dep := range strings.Fields(string(out)) {
		deps[dep] = true
	}
	for _, want := range []string{
		"github.com/agnos/agnoforge/internal/adapters/duckdb",
		"github.com/agnos/agnoforge/internal/adapters/binance",
		"github.com/agnos/agnoforge/internal/adapters/httpapi",
		"github.com/agnos/agnoforge/internal/app",
		"github.com/agnos/agnoforge/internal/domain",
	} {
		if !deps[want] {
			t.Errorf("cmd/agnoforge does not depend on %s", want)
		}
	}
}

// Nothing imports the command: wiring is a leaf.
func TestNoPackageImportsTheCommand(t *testing.T) {
	const command = "github.com/agnos/agnoforge/cmd/agnoforge"
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Deps " "}}`, "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == command {
			continue
		}
		for _, dep := range fields[1:] {
			if dep == command {
				t.Errorf("%s imports %s", fields[0], command)
			}
		}
	}
}
