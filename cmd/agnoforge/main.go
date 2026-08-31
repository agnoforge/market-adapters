// Command agnoforge serves the market data acquisition API and provides a
// thin HTTP client for it.
//
// This is the only package that names a concrete adapter: it wires the DuckDB
// Store and the Binance Provider into the use cases and serves them over the
// HTTP adapter. Every `data` subcommand is a client of that same HTTP API and
// knows nothing but the route it calls.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// The three exit codes this command has: nothing else is used.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usageText = `agnoforge — market data acquisition and composite datasets

Usage:
  agnoforge serve
  agnoforge data <command> [arguments]
  agnoforge composite <command> [arguments]

serve reads AGNOFORGE_LISTEN (default :8080), AGNOFORGE_DB_PATH (default
agnoforge.duckdb) and BINANCE_BASE_URL. data and composite talk to a running
service over AGNOFORGE_URL (default http://localhost:8080).

Data commands:
  providers
  backfill <provider> <symbol> <timeframe> <start> <end> [-wait] [-interval d]
  status   <backfill-id>
  cancel   <backfill-id>
  gaps     <provider> <symbol> <timeframe> [-status open|repaired|ignored|unrecoverable]
  repair   <gap-id>
  complete <provider> <symbol> <timeframe> <start> <end>
  query    <provider> <symbol> <timeframe> <start> <end> -o <file|-> [-format json]

Composite commands:
  create  <name> <declaration flags>
  list
  get     <name>
  edit    <name> <declaration flags>
  build   <name>
  query   <name> -o <file|-> [-timeframe 1h] [-start t] [-end t] [-format json]
  quality <name>
  delete  <name>

query reads the dataset's own resolved range when -start and -end are omitted,
and the 1m composite timeline when -timeframe is.

A declaration is -instrument <market> -base <provider:symbol> -start <t>
-end <t|now> [-catch-up none|provider:symbol] [-timeframes 5m,1h,1d]
[-mode strict|research]. An edit replaces the whole declaration.

Times are RFC3339 instants (2024-01-01T00:00:00Z) or YYYY-MM-DD dates, read as
UTC, and passed to the service verbatim.
`

// run is the whole command, with every effect it has passed in: the two
// streams it writes and the environment it reads. main is the only caller
// that hands it the real ones.
func run(args []string, stdout, stderr io.Writer, env func(string) string) int {
	if len(args) == 0 {
		return usageError(stderr, "")
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], stdout, stderr, env)
	case "data":
		return runData(args[1:], stdout, stderr, env)
	case "composite":
		return runComposite(args[1:], stdout, stderr, env)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	default:
		return usageError(stderr, fmt.Sprintf("unknown command %q", args[0]))
	}
}

// usageError reports how the command is spelled and exits 2. Nothing has
// happened yet when this is reached: no request has been made.
func usageError(stderr io.Writer, message string) int {
	if message != "" {
		fmt.Fprintf(stderr, "error: %s\n\n", message)
	}
	fmt.Fprint(stderr, usageText)
	return exitUsage
}

// failure reports a request that was made and did not work, and exits 1. A
// failure the service answered with names the trace it happened in, which is
// what turns "it broke" into a span tree in the playground.
func failure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "error: %v\n", err)
	var service *serviceError
	if errors.As(err, &service) && service.traceID != "" {
		fmt.Fprintf(stderr, "trace: %s\n", service.traceID)
	}
	return exitFailure
}
