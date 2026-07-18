package session

import (
	"os"
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

// TestWriteJSONLRefusesSymlink is the arbitrary-file-overwrite regression: a
// session artefact planted as a symlink (e.g. in a downloaded session) must not
// be followed, so the write cannot escape the session directory. Pre-fix
// os.Create followed the link and truncated the target.
func TestWriteJSONLRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(outside, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	link := filepath.Join(dir, TimelineFile)
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := WriteJSONL(link, []map[string]any{{"x": 1}})
	if err == nil {
		t.Fatal("WriteJSONL followed a symlink; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim file overwritten through symlink: %q", b)
	}
}

// TestWriteFileNoFollowRefusesSymlink covers the same guard for SaveManifest /
// report.md, which route through WriteFileNoFollow.
func TestWriteFileNoFollowRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	link := filepath.Join(dir, ReportFile)
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := WriteFileNoFollow(link, []byte("clobber\n"), 0o644); err == nil {
		t.Fatal("WriteFileNoFollow followed a symlink; want refusal")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim file overwritten through symlink: %q", b)
	}
}

// TestWriteJSONLPlainFileStillWorks confirms legitimate writes (regular files,
// including truncating an existing one) are unaffected by the symlink guard.
func TestWriteJSONLPlainFileStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), TimelineFile)
	if err := WriteJSONL(path, []map[string]any{{"a": 1}}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteJSONL(path, []map[string]any{{"b": 2}}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, err := ReadJSONL[map[string]any](path)
	if err != nil {
		t.Fatalf("ReadJSONL: %v", err)
	}
	if len(got) != 1 || got[0]["b"] != float64(2) {
		t.Fatalf("unexpected content after overwrite: %v", got)
	}
}

// TestSafeText strips control bytes (newline, CR, ESC, DEL, C1) that would
// otherwise forge report structure or drive an ANSI terminal, while leaving
// ordinary text intact.
func TestSafeText(t *testing.T) {
	cases := map[string]string{
		"plain save button":      "plain save button",
		"click\n### INJECTED":    "click### INJECTED",
		"a\x1b[31mred\x1b[0m":    "a[31mred[0m",
		"tab\there":              "tab here",
		"crlf\r\nline":           "crlfline",
		"del\x7fbell\a":          "delbell",
		"[data-testid=save-btn]": "[data-testid=save-btn]",
		"rtl\u202eoverride":      "rtloverride", // U+202E RIGHT-TO-LEFT OVERRIDE (Trojan-Source)
		"iso\u2066\u2069late":    "isolate",     // U+2066/U+2069 directional isolates
		"line\u2028sep":          "linesep",     // U+2028 line separator
		"mark\u200e\u200fs":      "marks",       // U+200E/U+200F LRM/RLM
		"caf\u00e9":              "caf\u00e9",   // ordinary accented text is unchanged
	}
	for in, want := range cases {
		if got := SafeText(in); got != want {
			t.Errorf("SafeText(%q) = %q, want %q", in, got, want)
		}
	}
}
