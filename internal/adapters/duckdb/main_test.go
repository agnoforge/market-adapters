package duckdb_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain guards the rule that this package's tests run entirely in memory:
// every Store is opened on ":memory:", and the only bytes that reach a
// filesystem are the Parquet exports each test writes into its own
// t.TempDir(). Nothing may appear beside the package or at the repository
// root.
func TestMain(m *testing.M) {
	before := strays()
	code := m.Run()
	if code == 0 {
		if after := strays(); len(after) > len(before) {
			fmt.Fprintf(os.Stderr, "tests left database or Parquet files on disk: %v\n", after)
			code = 1
		}
	}
	os.Exit(code)
}

// strays lists the database and Parquet files sitting in the working
// directory and at the repository root.
func strays() []string {
	var found []string
	for _, dir := range []string{".", "..", filepath.Join("..", "..", "..")} {
		for _, ext := range []string{"*.duckdb", "*.parquet", "*.db", "*.wal"} {
			matches, err := filepath.Glob(filepath.Join(dir, ext))
			if err != nil {
				continue
			}
			found = append(found, matches...)
		}
	}
	return found
}
