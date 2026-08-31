package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agnos/agnoforge/internal/adapters/binance"
	"github.com/agnos/agnoforge/internal/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/adapters/httpapi"
	"github.com/agnos/agnoforge/internal/adapters/playground"
	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/composite/adapters/acqport"
	compositeduckdb "github.com/agnos/agnoforge/internal/composite/adapters/duckdb"
	compositehttpapi "github.com/agnos/agnoforge/internal/composite/adapters/httpapi"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
)

// Config defaults. The service is configured by environment alone: there is
// no config file and no flag that duplicates one.
const (
	defaultDBPath = "agnoforge.duckdb"
	defaultListen = ":8080"
)

// providerTimeout bounds one Provider request, retries and all being the
// adapter's own business.
const providerTimeout = 30 * time.Second

// shutdownGrace is how long an in-flight request has to finish after a
// SIGINT or SIGTERM.
const shutdownGrace = 10 * time.Second

// serveConfig is everything serve reads from the environment.
type serveConfig struct {
	DBPath         string
	Listen         string
	BinanceBaseURL string
}

// serveConfigFrom reads the configuration: AGNOFORGE_DB_PATH,
// AGNOFORGE_LISTEN, and BINANCE_BASE_URL through the adapter's own helper, so
// the override lives in exactly one place.
func serveConfigFrom(env func(string) string) serveConfig {
	cfg := serveConfig{
		DBPath:         env("AGNOFORGE_DB_PATH"),
		Listen:         env("AGNOFORGE_LISTEN"),
		BinanceBaseURL: env("BINANCE_BASE_URL"),
	}
	if cfg.DBPath == "" {
		cfg.DBPath = defaultDBPath
	}
	if cfg.Listen == "" {
		cfg.Listen = defaultListen
	}
	if cfg.BinanceBaseURL == "" {
		cfg.BinanceBaseURL = binance.BaseURL()
	}
	return cfg
}

// runServe is the `agnoforge serve` command: bind the configured address and
// serve until SIGINT or SIGTERM.
func runServe(args []string, stdout, stderr io.Writer, env func(string) string) int {
	fs := flagsFor("serve", stderr)
	if _, code, ok := parseArgs(fs, args, 0, "serve", stderr); !ok {
		return code
	}
	cfg := serveConfigFrom(env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return failure(stderr, err)
	}
	if err := serveOn(ctx, listener, cfg, stdout, newLogger(stderr)); err != nil {
		return failure(stderr, err)
	}
	return exitOK
}

// newLogger builds the structured logger the whole service writes through.
func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// serveOn is the wiring: this function is the one place in the program where
// a concrete Store and a concrete Provider are named. It opens the DuckDB
// database, builds the Binance Provider, hands both to the use cases, serves
// the HTTP adapter over the listener, and shuts down gracefully when ctx is
// cancelled.
//
// The bound address is announced on stdout before the first request, which is
// what lets a caller — a test, or a person running with AGNOFORGE_LISTEN
// ":0" — learn where the service actually is.
func serveOn(ctx context.Context, listener net.Listener, cfg serveConfig, stdout io.Writer, logger *slog.Logger) error {
	// The trace store is the only sink there is: every span this process
	// records lands in it, and the playground serves it back (ADR 0003).
	traces := playground.NewStore()
	tp := newTracerProvider(traces)
	installTracing(tp)
	defer func() {
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := tp.Shutdown(grace); err != nil {
			logger.Warn("tracing did not shut down cleanly", "err", err)
		}
	}()

	store, err := duckdb.Open(cfg.DBPath)
	if err != nil {
		listener.Close()
		return err
	}
	defer store.Close()

	provider := binance.New(cfg.BinanceBaseURL, &http.Client{Timeout: providerTimeout},
		binance.WithLogger(logger))
	svc := app.New(store, []app.Provider{provider}, app.WithLogger(logger))

	// The Composite Market Dataset context runs beside acquisition in the same
	// process and the same database file, but owns its own schema, its own use
	// cases and its own routes. Opening it here is what applies its DDL on
	// startup (ADR-0005).
	compositeStore, err := compositeduckdb.Open(cfg.DBPath)
	if err != nil {
		listener.Close()
		return err
	}
	defer compositeStore.Close()
	// The composite context asks acquisition for coverage, completeness,
	// backfills and gaps through its own port; the adapter wrapping the
	// acquisition service is the only thing that knows the two live in one
	// process. Source bars never travel that way — the composite store reads
	// them in SQL from the same file (ADR-0005).
	compositeSvc := compositeapp.New(compositeStore, acqport.New(svc), compositeapp.WithLogger(logger))

	// One mux, one port: the playground's own routes and the composites
	// resource are more specific than the API's catch-all, so the pattern mux
	// gives them precedence and every other path reaches the domain routes
	// untouched.
	mux := http.NewServeMux()
	traces.Register(mux)
	composites := compositehttpapi.New(compositeSvc, logger)
	mux.Handle("/composites", composites)
	mux.Handle("/composites/", composites)
	mux.Handle("/", httpapi.New(svc, logger))
	server := &http.Server{Handler: instrument(mux, tp)}

	logger.Info("serving", "addr", listener.Addr().String(), "db", cfg.DBPath, "binance", cfg.BinanceBaseURL)
	fmt.Fprintf(stdout, "listening on %s\n", listener.Addr().String())

	stopped := make(chan error, 1)
	go func() {
		<-ctx.Done()
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		stopped <- server.Shutdown(grace)
	}()

	err = server.Serve(listener)
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Serve only returns ErrServerClosed once Shutdown was called, so waiting
	// for it here is what makes the deferred Close happen after the last
	// request, not during it.
	if err := <-stopped; err != nil {
		return err
	}
	// Running Backfills are cancelled and awaited before the Store closes
	// under them, so their terminal state and gap detection still land.
	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := svc.Shutdown(grace); err != nil {
		logger.Warn("backfills did not finish before shutdown", "err", err)
	}
	logger.Info("stopped")
	return nil
}
