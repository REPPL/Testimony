package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// TestCreateRefusesSameSecondCollision is the conflated-session regression: two
// captures starting within the same wall-clock second derive the same
// second-granularity directory name. Create must not silently reuse the first
// session's directory (pre-fix os.MkdirAll returned it, clobbering the first
// manifest and conflating the two sessions' append-only streams).
func TestCreateRefusesSameSecondCollision(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 17, 15, 30, 45, 0, time.UTC)

	if _, err := Create(root, now, Manifest{App: "first"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	dir2, err := Create(root, now, Manifest{App: "second"})
	if err == nil {
		t.Fatalf("second Create reused existing dir %q; want refusal", dir2)
	}

	// The first session's manifest must be intact, not overwritten by the second
	// run's metadata.
	m, err := LoadManifest(filepath.Join(root, now.Format(dirLayout)))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.App != "first" {
		t.Fatalf("first manifest clobbered: App=%q, want %q", m.App, "first")
	}
}

// TestReadJSONLSkipsWhitespaceOnlyLine is the blank-line regression: ReadJSONL
// documents that blank lines are skipped, so a whitespace-only line (as may
// appear in a hand-edited or exchanged session) must be skipped rather than fed
// to json.Unmarshal (pre-fix it crashed with "unexpected end of JSON input").
func TestReadJSONLSkipsWhitespaceOnlyLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), TimelineFile)
	content := "{\"a\":1}\n   \n\t\n{\"a\":2}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadJSONL[map[string]any](path)
	if err != nil {
		t.Fatalf("ReadJSONL: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2 (whitespace lines skipped): %v", len(got), got)
	}
}

// TestManifestT0RejectsAbsentAnchor is the epoch-anchored-session regression: a
// manifest that omits t0_epoch_ms decodes to the int64 zero value, which pre-fix
// was read straight out of the field and subtracted from real epoch-millisecond
// event times — anchoring the whole session at 1 January 1970 and placing every
// event about fifty-seven years in, as plausible-looking numbers in a written
// timeline. Manifest.T0 must refuse the absent anchor instead of handing back 0.
func TestManifestT0RejectsAbsentAnchor(t *testing.T) {
	dir := t.TempDir()
	// Write the manifest by hand, with no t0_epoch_ms at all: this is what an
	// exchanged or hand-edited session looks like, and the case SaveManifest of a
	// zero-valued struct cannot distinguish from a recorded zero.
	body := `{"session":"2026-07-17_153045","app":"settings prototype","participant":"Alice"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Loading still succeeds: report and analyze need the context fields and no
	// anchor, so the refusal belongs at the point of use, not at load.
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.App != "settings prototype" {
		t.Fatalf("context fields lost: %+v", m)
	}

	t0, err := m.T0()
	if err == nil {
		t.Fatalf("T0 returned %d for a manifest with no t0_epoch_ms; want refusal", t0)
	}
	if !errors.Is(err, ErrNoT0) {
		t.Fatalf("T0 error does not match ErrNoT0: %v", err)
	}
	if t0 != 0 {
		t.Fatalf("T0 returned %d alongside its error; want 0", t0)
	}
}

// TestManifestT0RejectsPreEpochAnchor covers the same anchor guard for a
// negative t0_epoch_ms, which no recorder can produce and which pre-fix would
// have been used as a real anchor, pushing every event past the end of the
// session instead of before its start.
func TestManifestT0RejectsPreEpochAnchor(t *testing.T) {
	m := Manifest{Session: "s", T0EpochMS: -1}
	if t0, err := m.T0(); err == nil {
		t.Fatalf("T0 accepted a pre-epoch anchor, returning %d; want refusal", t0)
	}
}

// TestManifestT0AcceptsRecordedAnchor is the other half of the guard: a session
// created by Create carries a real t0, and T0 must hand back exactly the value
// Create recorded, so the refusal above cannot be satisfied by rejecting
// everything.
func TestManifestT0AcceptsRecordedAnchor(t *testing.T) {
	now := time.Date(2026, 7, 17, 15, 30, 45, 0, time.UTC)
	dir, err := Create(t.TempDir(), now, Manifest{App: "settings prototype"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	t0, err := m.T0()
	if err != nil {
		t.Fatalf("T0 on a recorded session: %v", err)
	}
	if t0 != now.UnixMilli() {
		t.Fatalf("T0: got %d, want %d", t0, now.UnixMilli())
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

// TestWriteJSONLRefusesFIFO is the hang regression: a session artefact planted
// as a FIFO (in a session Alice merely received from Bob) must be refused, not
// opened. Pre-fix the guard covered only symlinks, so the write open blocked
// for ever waiting for a reader and merge or report never returned. The test
// runs the write in a goroutine and fails on a timeout, so a regression reports
// a failure rather than hanging the suite.
func TestWriteJSONLRefusesFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), TimelineFile)
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("FIFOs unavailable on this platform: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- WriteJSONL(path, []map[string]any{{"actor": "Alice"}}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("WriteJSONL wrote to a FIFO; want refusal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteJSONL blocked on a FIFO instead of refusing it")
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

// TestWriteJSONLRefusesOverlongRecord is the unreadable-artefact regression:
// MaxJSONLLine is the invariant every writer must respect, but pre-fix
// WriteJSONL enforced no upper bound, so merge could persist a timeline.jsonl
// record larger than ReadJSONL can scan back and still report success. The
// oversized record must be refused, and — because the refusal happens before
// the file is opened — an existing artefact must survive untouched rather than
// be truncated by the failed write.
func TestWriteJSONLRefusesOverlongRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), TimelineFile)
	if err := WriteJSONL(path, []map[string]string{{"actor": "Alice"}}); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	huge := []map[string]string{{"v": strings.Repeat("a", MaxJSONLLine)}}
	err := WriteJSONL(path, huge)
	if err == nil {
		t.Fatal("WriteJSONL persisted a record over MaxJSONLLine; want refusal")
	}
	if !strings.Contains(err.Error(), "record 0") {
		t.Errorf("error does not name the offending index: %v", err)
	}

	// The earlier artefact is intact: nothing was written, not even a truncation.
	got, err := ReadJSONL[map[string]string](path)
	if err != nil {
		t.Fatalf("ReadJSONL after refusal: %v", err)
	}
	if len(got) != 1 || got[0]["actor"] != "Alice" {
		t.Fatalf("refused write disturbed the existing artefact: %v", got)
	}
}

// TestWriteJSONLRefusalLeavesNoPartialFile pins the transactional stance on the
// records *before* the offending one: pre-fix there was no check at all, and a
// naive per-record check would still leave the earlier lines on disk followed
// by a line no reader can reach past. A refused set must write nothing.
func TestWriteJSONLRefusalLeavesNoPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FindingsFile)
	values := []map[string]string{
		{"actor": "Bob"},
		{"v": strings.Repeat("b", MaxJSONLLine)},
	}
	if err := WriteJSONL(path, values); err == nil {
		t.Fatal("WriteJSONL accepted a set carrying an over-long record; want refusal")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused write created %s (err=%v); want no file at all", FindingsFile, err)
	}
}

// TestWriteJSONLBoundaryLineRoundTrips pins the exact boundary WriteJSONL
// chose: a line is readable when its bytes *including* the trailing newline fit
// within MaxJSONLLine, because ReadJSONL's scanner buffer must hold the
// terminator too. A record encoding to exactly MaxJSONLLine bytes with its
// newline must therefore be accepted and read back; one byte more must not.
func TestWriteJSONLBoundaryLineRoundTrips(t *testing.T) {
	// {"v":"<payload>"}\n is len(payload) + 9 bytes.
	const overhead = 9
	path := filepath.Join(t.TempDir(), TimelineFile)
	atLimit := []map[string]string{{"v": strings.Repeat("c", MaxJSONLLine-overhead)}}
	if err := WriteJSONL(path, atLimit); err != nil {
		t.Fatalf("WriteJSONL refused a record at the limit: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() != MaxJSONLLine {
		t.Fatalf("boundary line is %d bytes, want exactly %d — adjust the test, not the guard", fi.Size(), MaxJSONLLine)
	}
	got, err := ReadJSONL[map[string]string](path)
	if err != nil {
		t.Fatalf("ReadJSONL could not read back a line WriteJSONL accepted: %v", err)
	}
	if len(got) != 1 || len(got[0]["v"]) != MaxJSONLLine-overhead {
		t.Fatalf("boundary record did not round-trip: %d records", len(got))
	}

	overLimit := []map[string]string{{"v": strings.Repeat("c", MaxJSONLLine-overhead+1)}}
	if err := WriteJSONL(path, overLimit); err == nil {
		t.Fatal("WriteJSONL accepted a line one byte past the limit; want refusal")
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
		"alm\u061cstrip":         "almstrip",    // U+061C ARABIC LETTER MARK (Bidi_Control)
		"caf\u00e9":              "caf\u00e9",   // ordinary accented text is unchanged
	}
	for in, want := range cases {
		if got := SafeText(in); got != want {
			t.Errorf("SafeText(%q) = %q, want %q", in, got, want)
		}
	}
}
