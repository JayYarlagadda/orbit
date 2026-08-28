package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverOrdersAndClassifiesMigrations(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		"000002_second.down.sql",
		"README.md",
		"000001_first.up.sql",
		"000002_second.up.sql",
		"000001_first.down.sql",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("SELECT 1;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := discover(directory)
	if err != nil {
		t.Fatalf("discover() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len(discover()) = %d, want 4", len(got))
	}
	if got[0].version != 1 || got[0].direction != Down || got[1].direction != Up || got[2].version != 2 {
		t.Fatalf("discover() order = %+v", got)
	}
}

func TestDiscoverRequiresMigration(t *testing.T) {
	if _, err := discover(t.TempDir()); err == nil {
		t.Fatal("discover() unexpectedly succeeded")
	}
}
