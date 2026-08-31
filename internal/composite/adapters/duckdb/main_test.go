package duckdb_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain guards the rule that this package's tests never write a database
// anywhere but their own t.TempDir(): nothing may appear beside the package or
// at the repository root, where the real agnoforge.duckdb lives.
func TestMain(m *testing.M) {
	before := strays()
	code := m.Run()
	if code == 0 {
		if after := strays(); len(after) > len(before) {
			fmt.Fprintf(os.Stderr, "tests left database files on disk: %v\n", after)
			code = 1
		}
	}
	os.Exit(code)
}

// strays lists the database files sitting in the working directory and at the
// repository root.
func strays() []string {
	var found []string
	for _, dir := range []string{".", "..", filepath.Join("..", "..", "..", "..")} {
		for _, ext := range []string{"*.duckdb", "*.db", "*.wal"} {
			matches, err := filepath.Glob(filepath.Join(dir, ext))
			if err != nil {
				continue
			}
			found = append(found, matches...)
		}
	}
	return found
}
