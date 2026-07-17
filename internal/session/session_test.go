package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCreate(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 17, 15, 30, 45, 0, time.UTC)

	dir, err := Create(root, now, Manifest{
		App:         "settings prototype",
		Participant: "P1",
		Tasks:       []string{"Find the save button"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Directory is named from now, under outRoot.
	wantBase := "2026-07-17_153045"
	if got := filepath.Base(dir); got != wantBase {
		t.Fatalf("dir name: got %q, want %q", got, wantBase)
	}
	if filepath.Dir(dir) != root {
		t.Fatalf("dir parent: got %q, want %q", filepath.Dir(dir), root)
	}

	// Manifest round-trips with session + t0 derived from the same now.
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Session != wantBase {
		t.Fatalf("manifest session: got %q, want %q", m.Session, wantBase)
	}
	if m.T0EpochMS != now.UnixMilli() {
		t.Fatalf("t0_epoch_ms: got %d, want %d", m.T0EpochMS, now.UnixMilli())
	}
	if m.App != "settings prototype" || m.Participant != "P1" {
		t.Fatalf("flags not carried into manifest: %+v", m)
	}
	if len(m.Tasks) != 1 || m.Tasks[0] != "Find the save button" {
		t.Fatalf("tasks not carried into manifest: %+v", m.Tasks)
	}
}
