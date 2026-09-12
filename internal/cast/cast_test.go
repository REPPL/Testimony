package cast

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/REPPL/Testimony/internal/report"
	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// The fixture pair describes one recording in both formats: the same header
// timestamp, event codes, and absolute times, with v3's intervals as the
// differences. Its header timestamp sits two seconds before testT0, so the
// session-relative clock starts negative and crosses zero.
const (
	fixtureV2 = "v2.cast"
	fixtureV3 = "v3.cast"
)

// newSession makes a hermetic session directory holding just a manifest.
func newSession(t *testing.T, t0 int64) string {
	t.Helper()
	dir := t.TempDir()
	m := session.Manifest{Session: "cast-test", App: "a shell", Participant: "P1", T0EpochMS: t0}
	if t0 == 0 {
		// SaveManifest writes whatever it is given; an absent anchor is the point
		// of the caller's case.
		m.T0EpochMS = 0
	}
	if err := session.SaveManifest(dir, m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	return dir
}

// snapshot hashes every file in dir, so a refusal can be shown to have left the
// session byte-for-byte as it found it.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		// Lstat, and only regular files are read: a FIFO a test planted in the
		// session would block os.ReadFile in open(2) for ever, and a symlink's own
		// target is not part of the session.
		fi, err := os.Lstat(path)
		if err != nil || !fi.Mode().IsRegular() {
			out[e.Name()] = fmt.Sprintf("non-regular %v", e.Type())
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			out[e.Name()] = "unreadable"
			continue
		}
		out[e.Name()] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return out
}

func assertSessionUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := snapshot(t, dir)
	if len(before) != len(after) {
		t.Fatalf("session file set changed: %v -> %v", keys(before), keys(after))
	}
	for name, sum := range before {
		if after[name] != sum {
			t.Errorf("%s changed: %s -> %s", name, sum, after[name])
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

func mustRun(t *testing.T, opts Options) (int, string) {
	t.Helper()
	var log bytes.Buffer
	opts.Log = &log
	n, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return n, log.String()
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func decodeRecords(t *testing.T, path string) []timeline.Interaction {
	t.Helper()
	var out []timeline.Interaction
	for i, line := range readLines(t, path) {
		var rec timeline.Interaction
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%s:%d: %v", path, i+1, err)
		}
		out = append(out, rec)
	}
	return out
}

// TestRunImportsFixture is the happy path: the records written, the return
// value, the printed lines, and the archived cast.
func TestRunImportsFixture(t *testing.T) {
	dir := newSession(t, testT0)
	n, log := mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	if n != 5 {
		t.Fatalf("imported %d records, want 5", n)
	}
	recs := decodeRecords(t, filepath.Join(dir, session.InteractionsFile))
	if len(recs) != 5 {
		t.Fatalf("interactions.jsonl holds %d records, want 5", len(recs))
	}
	// The header timestamp is two seconds before t0, so the cast's first record
	// lands two seconds before the session clock's zero.
	wantRel := []int64{-2000, -1480, -498, 0, 1000}
	for i, rec := range recs {
		if got := rec.T - testT0; got != wantRel[i] {
			t.Errorf("record %d: session-relative t = %d ms, want %d", i+1, got, wantRel[i])
		}
		if rec.Kind != OutputKind {
			t.Errorf("record %d: kind = %q, want %q", i+1, rec.Kind, OutputKind)
		}
	}
	if got := recs[1].Text; got != "ls --color\r\n" {
		t.Errorf("the echoed command coalesced to %q, want %q", got, "ls --color\r\n")
	}
	if got := recs[3].Text; !strings.Contains(got, "building   0%\rbuilding  50%\rbuilding 100%\r\n") {
		t.Errorf("the progress frames coalesced to %q", got)
	}
	for _, want := range []string{
		"offset: -2.00s (derived: cast header timestamp − manifest t0 (whole seconds, ±1s))",
		"dropped 2 input (i) event(s): keystrokes are never imported",
		"dropped 3 other event(s): 1 exit (x), 1 marker (m), 1 resize (r)",
		"dropped 1 record(s) that render empty",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("printed output %q does not contain %q", log, want)
		}
	}
}

// TestImportMatchesGolden pins the exact records v2.cast produces.
func TestImportMatchesGolden(t *testing.T) {
	dir := newSession(t, testT0)
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	got, err := os.ReadFile(filepath.Join(dir, session.InteractionsFile))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(fixture(t, "golden.interactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("interactions.jsonl does not match the golden file\n got: %s\nwant: %s", got, want)
	}
}

// TestV2AndV3Agree is criterion 2: the operator never states, or needs to know,
// which format their recorder wrote.
func TestV2AndV3Agree(t *testing.T) {
	var out [2][]byte
	for i, name := range []string{fixtureV2, fixtureV3} {
		dir := newSession(t, testT0)
		mustRun(t, Options{SessionDir: dir, Cast: fixture(t, name)})
		b, err := os.ReadFile(filepath.Join(dir, session.InteractionsFile))
		if err != nil {
			t.Fatal(err)
		}
		out[i] = b
	}
	if !bytes.Equal(out[0], out[1]) {
		t.Errorf("v2 and v3 of the same recording produced different records\n v2: %s\n v3: %s", out[0], out[1])
	}
}

// TestUnsupportedVersionRefuses is criterion 3: a message naming the file and
// the version found, and nothing written.
func TestUnsupportedVersionRefuses(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"version 1", `{"version":1,"timestamp":1784300398}`, "asciicast version 1 is not supported"},
		{"version as a string", `{"version":"2"}`, "asciicast version \"2\" is not supported"},
		{"version absent", `{"timestamp":1784300398}`, "carries no \"version\" field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newSession(t, testT0)
			castPath := filepath.Join(t.TempDir(), "session.cast")
			if err := os.WriteFile(castPath, []byte(tc.header+"\n[0,\"o\",\"x\\r\\n\"]\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, dir)
			_, err := Run(Options{SessionDir: dir, Cast: castPath, Log: &bytes.Buffer{}})
			if err == nil {
				t.Fatal("want a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "session.cast") {
				t.Errorf("error %q does not name the file", err)
			}
			assertSessionUnchanged(t, dir, before)
		})
	}
}

// TestVersion4FixtureRefuses uses the committed fixture, so the refusal is
// exercised against a file on disk as well as against a generated header.
func TestVersion4FixtureRefuses(t *testing.T) {
	dir := newSession(t, testT0)
	before := snapshot(t, dir)
	_, err := Run(Options{SessionDir: dir, Cast: fixture(t, "bad-version.cast"), Log: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "asciicast version 4 is not supported") {
		t.Fatalf("error = %v, want an unsupported-version refusal", err)
	}
	assertSessionUnchanged(t, dir, before)
}

func TestRefusalsLeaveTheSessionUnchanged(t *testing.T) {
	cases := []struct {
		name string
		// setup prepares the session and returns the Options to run.
		setup func(t *testing.T) (string, Options)
		want  string
	}{
		{
			name: "malformed header",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: fixture(t, "bad-header.cast")}
			},
			want: "not an asciicast header",
		},
		{
			name: "malformed event",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: fixture(t, "bad-event.cast")}
			},
			want: "malformed asciicast event",
		},
		{
			name: "decreasing v2 time",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: fixture(t, "decreasing.cast")}
			},
			want: "must not decrease",
		},
		{
			name: "negative v3 interval",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: fixture(t, "negative-interval.cast")}
			},
			want: "must not be negative",
		},
		{
			name: "no manifest",
			setup: func(t *testing.T) (string, Options) {
				dir := t.TempDir()
				return dir, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)}
			},
			want: "load manifest",
		},
		{
			name: "manifest with no usable t0",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, 0)
				return dir, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)}
			},
			want: "anchoring the terminal cast",
		},
		{
			name: "manifest with a negative t0",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, -1)
				return dir, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)}
			},
			want: "anchoring the terminal cast",
		},
		{
			name: "non-positive header timestamp",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: writeCast(t, `{"version":2,"timestamp":0}`, `[0,"o","x\r\n"]`)}
			},
			want: "is not a recording instant",
		},
		{
			name: "negative header timestamp",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: writeCast(t, `{"version":2,"timestamp":-5}`, `[0,"o","x\r\n"]`)}
			},
			want: "is not a recording instant",
		},
		{
			name: "implausible derived offset",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: writeCast(t, `{"version":2,"timestamp":9000000000000}`, `[0,"o","x\r\n"]`)}
			},
			want: "derived cast offset",
		},
		{
			name: "no output events",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: writeCast(t, `{"version":2,"timestamp":1784300398}`, `[0,"i","l"]`, `[0.5,"r","80x24"]`)}
			},
			want: "holds no output events",
		},
		{
			name: "output events that all render empty",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: writeCast(t, `{"version":2,"timestamp":1784300398}`, `[0,"o","\r\n"]`)}
			},
			want: "holds no output events",
		},
		{
			name: "neither -cast nor terminal.cast",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir}
			},
			want: "and no -cast given",
		},
		{
			name: "-cast naming a missing file",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: filepath.Join(t.TempDir(), "absent.cast")}
			},
			want: "cast file:",
		},
		{
			name: "-cast naming a directory",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				return dir, Options{SessionDir: dir, Cast: t.TempDir()}
			},
			want: "it is not a regular file",
		},
		{
			name: "a symlink at terminal.cast",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				target := filepath.Join(t.TempDir(), "elsewhere.cast")
				if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(dir, session.TerminalCastFile)); err != nil {
					t.Fatal(err)
				}
				return dir, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)}
			},
			want: "it is a symlink",
		},
		{
			name: "a symlink at interactions.jsonl",
			setup: func(t *testing.T) (string, Options) {
				dir := newSession(t, testT0)
				target := filepath.Join(t.TempDir(), "elsewhere.jsonl")
				if err := os.WriteFile(target, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(dir, session.InteractionsFile)); err != nil {
					t.Fatal(err)
				}
				return dir, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)}
			},
			want: "it is a symlink",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, opts := tc.setup(t)
			opts.Log = &bytes.Buffer{}
			before := snapshot(t, dir)
			if _, err := Run(opts); err == nil {
				t.Fatal("want a refusal, got nil")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
			assertSessionUnchanged(t, dir, before)
		})
	}
}

// writeCast writes a cast made of the given lines into a fresh temp directory
// and returns its path.
func writeCast(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.cast")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRefusesFIFOAtTerminalCast(t *testing.T) {
	dir := newSession(t, testT0)
	if err := syscall.Mkfifo(filepath.Join(dir, session.TerminalCastFile), 0o644); err != nil {
		t.Skipf("FIFOs unavailable on this platform: %v", err)
	}
	before := snapshot(t, dir)
	done := make(chan error, 1)
	go func() {
		_, err := Run(Options{SessionDir: dir, Cast: fixture(t, fixtureV2), Log: &bytes.Buffer{}})
		done <- err
	}()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want a non-regular-file refusal", err)
	}
	assertSessionUnchanged(t, dir, before)
}

func TestRefusesFIFOAsTheCastInput(t *testing.T) {
	dir := newSession(t, testT0)
	fifo := filepath.Join(t.TempDir(), "session.cast")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("FIFOs unavailable on this platform: %v", err)
	}
	before := snapshot(t, dir)
	done := make(chan error, 1)
	go func() {
		_, err := Run(Options{SessionDir: dir, Cast: fifo, Log: &bytes.Buffer{}})
		done <- err
	}()
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v, want a non-regular-file refusal", err)
	}
	assertSessionUnchanged(t, dir, before)
}

func TestOffsetProvenance(t *testing.T) {
	cases := []struct {
		name      string
		header    string
		offset    float64
		offsetSet bool
		t0        int64
		wantMS    int64
		wantProv  string
		wantErr   string
	}{
		{
			name:   "explicit -offset wins over the header",
			header: `{"version":2,"timestamp":1784300398}`, offset: -12.4, offsetSet: true, t0: testT0,
			wantMS: -12400, wantProv: "from -offset flag",
		},
		{
			name:   "explicit -offset rounds to the millisecond",
			header: `{"version":2}`, offset: 1.23456, offsetSet: true, t0: testT0,
			wantMS: 1235, wantProv: "from -offset flag",
		},
		{
			name:   "derived from the header timestamp",
			header: `{"version":2,"timestamp":1784300398}`, t0: testT0,
			wantMS: -2000, wantProv: "derived: cast header timestamp − manifest t0 (whole seconds, ±1s)",
		},
		{
			name:   "no header timestamp defaults to zero",
			header: `{"version":3}`, t0: testT0,
			wantMS: 0, wantProv: "default 0: cast header carries no timestamp",
		},
		{
			name:   "a null header timestamp counts as absent",
			header: `{"version":3,"timestamp":null}`, t0: testT0,
			wantMS: 0, wantProv: "default 0: cast header carries no timestamp",
		},
		{
			name:   "an unusable t0 refuses even with an explicit offset",
			header: `{"version":2,"timestamp":1784300398}`, offset: 1, offsetSet: true, t0: 0,
			wantErr: "anchoring the terminal cast",
		},
		{
			name:   "a non-positive header timestamp refuses",
			header: `{"version":2,"timestamp":0}`, t0: testT0,
			wantErr: "is not a recording instant",
		},
		{
			name:   "an implausible derived offset refuses",
			header: `{"version":2,"timestamp":9000000000000}`, t0: testT0,
			wantErr: "derived cast offset",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr, err := scanCast(strings.NewReader(tc.header+"\n"), "session.cast", nil, nil)
			if err != nil {
				t.Fatalf("scanCast: %v", err)
			}
			man := session.Manifest{Session: "s", T0EpochMS: tc.t0}
			opts := Options{Offset: tc.offset, OffsetSet: tc.offsetSet}
			gotMS, gotProv, err := resolveOffset("session.cast", opts, man, hdr)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveOffset: %v", err)
			}
			if gotMS != tc.wantMS {
				t.Errorf("offsetMS = %d, want %d", gotMS, tc.wantMS)
			}
			// The three provenance strings are a documented contract, printed
			// verbatim for the operator.
			if gotProv != tc.wantProv {
				t.Errorf("provenance = %q, want %q", gotProv, tc.wantProv)
			}
		})
	}
}

func TestRunRefusesNonFiniteOffset(t *testing.T) {
	dir := newSession(t, testT0)
	before := snapshot(t, dir)
	_, err := Run(Options{
		SessionDir: dir, Cast: fixture(t, fixtureV2),
		Offset: math.Inf(1), OffsetSet: true, Log: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "-offset must be a finite number") {
		t.Fatalf("error = %v, want transcribe.CheckOffset's refusal", err)
	}
	assertSessionUnchanged(t, dir, before)
}

// TestReimportIsIdempotent: the second run drops exactly what the first wrote
// and writes exactly the same records back.
func TestReimportIsIdempotent(t *testing.T) {
	dir := newSession(t, testT0)
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	path := filepath.Join(dir, session.InteractionsFile)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, log := mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("a re-import changed interactions.jsonl\nfirst:  %s\nsecond: %s", first, second)
	}
	if !strings.Contains(log, "replaced 5 terminal_output record(s) from an earlier import") {
		t.Errorf("printed output %q does not report the replaced records", log)
	}
}

// TestForeignRecordsPreserved: a -demo session's clicks and inputs survive an
// import byte-for-byte, in their original order, and so does a line this
// importer cannot decode at all.
func TestForeignRecordsPreserved(t *testing.T) {
	dir := newSession(t, testT0)
	foreign := []string{
		`{"t":1784300419200,"kind":"click","selector":"[data-testid=save-btn]","text":"Save","route":"#general"}`,
		`{"t":1784300421000,"kind":"input","selector":"[data-testid=name]","value":"Alice"}`,
		`{"kind":"terminal_output","t":1,"text":"from an earlier import"}`,
		`not json at all`,
		``,
		`{"t":1784300422000,"kind":"click","unknown_field":{"kept":true}}`,
	}
	path := filepath.Join(dir, session.InteractionsFile)
	if err := os.WriteFile(path, []byte(strings.Join(foreign, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})

	got := readLines(t, path)
	wantKept := []string{foreign[0], foreign[1], foreign[3], foreign[4], foreign[5]}
	if len(got) != len(wantKept)+5 {
		t.Fatalf("interactions.jsonl holds %d lines, want %d", len(got), len(wantKept)+5)
	}
	for i, want := range wantKept {
		if got[i] != want {
			t.Errorf("line %d = %q, want %q byte-for-byte", i+1, got[i], want)
		}
	}
	for i, line := range got[len(wantKept):] {
		if !strings.Contains(line, `"kind":"`+OutputKind+`"`) {
			t.Errorf("appended line %d is not a terminal record: %q", i+1, line)
		}
	}
}

func TestImportPreservesInteractionsFileMode(t *testing.T) {
	dir := newSession(t, testT0)
	path := filepath.Join(dir, session.InteractionsFile)
	if err := os.WriteFile(path, []byte("{\"t\":1784300419200,\"kind\":\"click\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("interactions.jsonl mode = %04o, want 0600 preserved", got)
	}
}

// TestCastArchivedVerbatim is where the byte-for-byte claim is made: the
// archived cast, not the decoded records.
func TestCastArchivedVerbatim(t *testing.T) {
	dir := newSession(t, testT0)
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	got, err := os.ReadFile(filepath.Join(dir, session.TerminalCastFile))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(fixture(t, fixtureV2))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("terminal.cast is not byte-identical to the imported file")
	}
	fi, err := os.Stat(filepath.Join(dir, session.TerminalCastFile))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o400 == 0 {
		t.Errorf("terminal.cast mode = %04o, want at least owner-readable", fi.Mode().Perm())
	}
}

func TestArchivePreservesExistingMode(t *testing.T) {
	dir := newSession(t, testT0)
	archive := filepath.Join(dir, session.TerminalCastFile)
	if err := os.WriteFile(archive, []byte("{\"version\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	fi, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("terminal.cast mode = %04o, want 0600 preserved", got)
	}
}

// TestInPlaceReimport: -cast omitted reuses the session's own terminal.cast, so
// a corrected -offset is a one-liner.
func TestInPlaceReimport(t *testing.T) {
	dir := newSession(t, testT0)
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	archive := filepath.Join(dir, session.TerminalCastFile)
	before, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}

	n, log := mustRun(t, Options{SessionDir: dir, Offset: -12.4, OffsetSet: true})
	if n != 5 {
		t.Fatalf("imported %d records, want 5", n)
	}
	if !strings.Contains(log, "offset: -12.40s (from -offset flag)") {
		t.Errorf("printed output %q does not carry the explicit offset", log)
	}
	after, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the in-place re-import rewrote terminal.cast")
	}
	recs := decodeRecords(t, filepath.Join(dir, session.InteractionsFile))
	if got := recs[0].T - testT0; got != -12400 {
		t.Errorf("first record's session-relative t = %d ms, want -12400", got)
	}
}

// TestSameFileCastIsInPlace: -cast pointing at the session's own terminal.cast
// is the omitted case, so the archive is never copied onto itself.
func TestSameFileCastIsInPlace(t *testing.T) {
	dir := newSession(t, testT0)
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	archive := filepath.Join(dir, session.TerminalCastFile)
	inPlace, log := mustRun(t, Options{SessionDir: dir, Cast: archive})
	if inPlace != 5 {
		t.Fatalf("imported %d records, want 5", inPlace)
	}
	if !strings.Contains(log, "derived: cast header timestamp") {
		t.Errorf("printed output %q does not re-derive the offset from the archived header", log)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+session.TerminalCastFile) {
			t.Errorf("a staging temp file was left behind: %s", e.Name())
		}
	}
}

// TestInputEventsDropped: keystrokes never reach a record, and the count says
// the recorder captured them.
func TestInputEventsDropped(t *testing.T) {
	dir := newSession(t, testT0)
	castPath := writeCast(t,
		`{"version":2,"timestamp":1784300398}`,
		`[0,"i","s"]`,
		`[0.1,"i","e"]`,
		`[0.2,"i","c"]`,
		`[0.3,"i","r"]`,
		`[0.4,"i","e"]`,
		`[0.5,"i","t"]`,
		`[0.6,"o","visible output\r\n"]`,
	)
	n, log := mustRun(t, Options{SessionDir: dir, Cast: castPath})
	if n != 1 {
		t.Fatalf("imported %d records, want 1", n)
	}
	recs := decodeRecords(t, filepath.Join(dir, session.InteractionsFile))
	for _, rec := range recs {
		for _, key := range []string{"s", "e", "c", "r", "t"} {
			if rec.Text == key {
				t.Errorf("a keystroke reached a record: %q", rec.Text)
			}
		}
	}
	if got := recs[0].Text; got != "visible output\r\n" {
		t.Errorf("record text = %q, want the output only", got)
	}
	if !strings.Contains(log, "dropped 6 input (i) event(s)") {
		t.Errorf("printed output %q does not report the dropped keystrokes", log)
	}
}

// TestOversizedEventSplits is criterion 7 at the package boundary.
func TestOversizedEventSplits(t *testing.T) {
	dir := newSession(t, testT0)
	// 6 MiB raw, not the 12 MiB the coalescer unit uses: escaping inflates the
	// records by about a third, and the assembled interactions.jsonl has its own
	// 16 MiB cap, so a larger event would be refused for the file's size rather
	// than split. 6 MiB is comfortably past the 4 MiB line limit, which is what
	// the split needs.
	unit := "abcé中\x1b[0;34m"
	data := strings.Repeat(unit, 6<<20/len(unit))
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	castPath := writeCast(t, `{"version":2,"timestamp":1784300398}`, `[0,"o",`+string(encoded)+`]`)

	n, _ := mustRun(t, Options{SessionDir: dir, Cast: castPath})
	if n < 2 {
		t.Fatalf("imported %d records, want more than one", n)
	}
	recs := decodeRecords(t, filepath.Join(dir, session.InteractionsFile))
	var joined strings.Builder
	for i, rec := range recs {
		entryLen, err := session.EncodedLen(timeline.EventEntry(rec, testT0))
		if err != nil {
			t.Fatal(err)
		}
		if entryLen > session.MaxJSONLLine {
			t.Errorf("record %d's timeline entry is %d bytes, over the %d-byte limit", i+1, entryLen, session.MaxJSONLLine)
		}
		joined.WriteString(rec.Text)
	}
	if joined.String() != data {
		t.Errorf("the records do not reproduce the event's data (%d bytes read back, %d written)", joined.Len(), len(data))
	}
	// The records are still mergeable, which is the whole point of the budget.
	if _, _, err := timeline.Merge(dir); err != nil {
		t.Errorf("merge refused the split records: %v", err)
	}
}

// TestImportedRecordsPassCheckInteraction runs every fixture's records through
// the guard merge applies, so import cannot persist a record merge refuses.
func TestImportedRecordsPassCheckInteraction(t *testing.T) {
	for _, name := range []string{fixtureV2, fixtureV3, "v3-nots.cast"} {
		t.Run(name, func(t *testing.T) {
			dir := newSession(t, testT0)
			mustRun(t, Options{SessionDir: dir, Cast: fixture(t, name)})
			for i, line := range readLines(t, filepath.Join(dir, session.InteractionsFile)) {
				if err := timeline.CheckInteraction([]byte(line), testT0); err != nil {
					t.Errorf("record %d %v", i+1, err)
				}
			}
		})
	}
}

func TestInteractionsFileSizeLimitRefuses(t *testing.T) {
	dir := newSession(t, testT0)
	// A pre-existing file just under the cap, so the import's own records are
	// what push the assembly over it.
	line := `{"t":1784300419200,"kind":"click","text":"` + strings.Repeat("x", 1000) + `"}`
	var b strings.Builder
	for b.Len() < session.MaxJSONLBytes-2000 {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, session.InteractionsFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("y", 4000) + "\r\n"
	encoded, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	castPath := writeCast(t, `{"version":2,"timestamp":1784300398}`, `[0,"o",`+string(encoded)+`]`)
	before := snapshot(t, dir)
	_, err = Run(Options{SessionDir: dir, Cast: castPath, Log: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "past its") {
		t.Fatalf("error = %v, want a size refusal", err)
	}
	if !strings.Contains(err.Error(), session.InteractionsFile) {
		t.Errorf("error %q does not name interactions.jsonl", err)
	}
	assertSessionUnchanged(t, dir, before)
}

// TestMergedTimelineSizeLimitRefuses is the case only an offline importer can
// measure: interactions.jsonl itself stays under the cap while the timeline the
// import implies would not.
func TestMergedTimelineSizeLimitRefuses(t *testing.T) {
	dir := newSession(t, testT0)
	// Speech entries take up most of the merged budget. They live in
	// transcript.jsonl, which does not count towards interactions.jsonl's own cap.
	var utts []timeline.Utterance
	text := strings.Repeat("z", 4000)
	for i := 0; len(utts) < 4000; i++ {
		utts = append(utts, timeline.Utterance{
			ID: fmt.Sprintf("utt-%03d", i+1), T0: float64(i), T1: float64(i) + 1, Speaker: "P1", Text: text,
		})
	}
	if err := session.WriteJSONL(filepath.Join(dir, session.TranscriptFile), utts); err != nil {
		t.Skipf("transcript fixture over the writer's own cap: %v", err)
	}
	big := strings.Repeat("y", 900_000) + "\r\n"
	encoded, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	castPath := writeCast(t, `{"version":2,"timestamp":1784300398}`, `[0,"o",`+string(encoded)+`]`)
	before := snapshot(t, dir)
	_, err = Run(Options{SessionDir: dir, Cast: castPath, Log: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "merged "+session.TimelineFile) {
		t.Fatalf("error = %v, want a merged-timeline size refusal", err)
	}
	assertSessionUnchanged(t, dir, before)
}

func TestRefusesOversizedExistingInteractions(t *testing.T) {
	dir := newSession(t, testT0)
	var b strings.Builder
	line := `{"t":1784300419200,"kind":"click","text":"` + strings.Repeat("x", 1000) + `"}`
	for b.Len() <= session.MaxJSONLBytes {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, session.InteractionsFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	_, err := Run(Options{SessionDir: dir, Cast: fixture(t, fixtureV2), Log: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "refusing to read") {
		t.Fatalf("error = %v, want a read refusal", err)
	}
	assertSessionUnchanged(t, dir, before)
}

// --- Integration: import, merge, report ---

// writeTranscript seeds a two-utterance transcript spanning the fixture's
// records, so the join can be observed.
func writeTranscript(t *testing.T, dir string) {
	t.Helper()
	utts := []timeline.Utterance{
		{ID: "utt-001", T0: -1.5, T1: -0.5, Speaker: "P1", Text: "Right, let me list the project files."},
		{ID: "utt-002", T0: 0.5, T1: 2, Speaker: "P1", Text: "The colours make that hard to read."},
	}
	if err := session.WriteJSONL(filepath.Join(dir, session.TranscriptFile), utts); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
}

// TestImportThenMergeInterleaves is criterion 1: one interleaved clock from the
// same t0, with no separate clock for the terminal stream.
func TestImportThenMergeInterleaves(t *testing.T) {
	for _, name := range []string{fixtureV2, fixtureV3} {
		t.Run(name, func(t *testing.T) {
			dir := newSession(t, testT0)
			writeTranscript(t, dir)
			mustRun(t, Options{SessionDir: dir, Cast: fixture(t, name)})
			speech, events, err := timeline.Merge(dir)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if speech != 2 || events != 5 {
				t.Fatalf("merged %d speech and %d events, want 2 and 5", speech, events)
			}
			entries, err := timeline.ReadEntries(filepath.Join(dir, session.TimelineFile))
			if err != nil {
				t.Fatalf("ReadEntries: %v", err)
			}
			type want struct {
				t   float64
				src string
			}
			wants := []want{
				{-2, "event"},    // the first prompt, two seconds before t0
				{-1.5, "speech"}, // Alice starts speaking
				{-1.48, "event"}, // the echoed command
				{-0.498, "event"},
				{0, "event"},
				{0.5, "speech"},
				{1, "event"},
			}
			if len(entries) != len(wants) {
				t.Fatalf("timeline holds %d entries, want %d", len(entries), len(wants))
			}
			for i, w := range wants {
				if entries[i].T != w.t || entries[i].Src != w.src {
					t.Errorf("entry %d = (t %g, src %s), want (t %g, src %s)", i+1, entries[i].T, entries[i].Src, w.t, w.src)
				}
			}
		})
	}
}

// TestReportRendersTerminalEvents is criterion 4: the existing event rendering,
// unchanged.
func TestReportRendersTerminalEvents(t *testing.T) {
	dir := newSession(t, testT0)
	writeTranscript(t, dir)
	mustRun(t, Options{SessionDir: dir, Cast: fixture(t, fixtureV2)})
	if _, _, err := timeline.Merge(dir); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	md, err := report.Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"**[-00:02] P1:** ",
		// report escapes the underscore for its Markdown sink; the record is
		// rendered by the existing eventLine, unchanged.
		"- [-00:02] terminal\\_output ",
		"[00:00] terminal\\_output ",
		"ls --color",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report does not contain %q\n%s", want, md)
		}
	}
	// SafeText strips the ESC byte, so no raw terminal control sequence reaches
	// report.md — the printable CSI tail stays, which is why the how-to asks for
	// colour to be disabled at record time.
	if strings.ContainsRune(md, 0x1b) {
		t.Error("report.md carries a raw ESC byte")
	}
	if !strings.Contains(md, "0;34m") {
		t.Error("report.md does not show the printable CSI residue the docs describe")
	}
}

// TestEarlyStartedCastGoesNegative is criterion 6: an early-started recorder's
// events render exactly as an early-started audio recording's utterances do.
func TestEarlyStartedCastGoesNegative(t *testing.T) {
	dir := newSession(t, testT0)
	castPath := writeCast(t,
		fmt.Sprintf(`{"version":2,"timestamp":%d}`, testT0/1000-30),
		`[0,"o","early output\r\n"]`,
		`[30,"o","on time\r\n"]`,
	)
	mustRun(t, Options{SessionDir: dir, Cast: castPath})
	if _, _, err := timeline.Merge(dir); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	entries, err := timeline.ReadEntries(filepath.Join(dir, session.TimelineFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].T != -30 || entries[1].T != 0 {
		t.Fatalf("entry times = %v, want -30 and 0", entries)
	}
	md, err := report.Render(dir, 2.5)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(md, "[-00:30]") {
		t.Errorf("report does not render the negative clock\n%s", md)
	}
}

// TestDropTallyIsBounded: a cast carrying a different event code on every line
// must not grow the tally, or the line it prints, in step with the file.
func TestDropTallyIsBounded(t *testing.T) {
	dir := newSession(t, testT0)
	lines := []string{`{"version":2,"timestamp":1784300398}`}
	for i := 0; i < maxDropCodes*4; i++ {
		lines = append(lines, fmt.Sprintf(`[%d,"c%d","x"]`, i, i))
	}
	lines = append(lines, fmt.Sprintf(`[%d,"o","real output\r\n"]`, maxDropCodes*4))
	n, log := mustRun(t, Options{SessionDir: dir, Cast: writeCast(t, lines...)})
	if n != 1 {
		t.Fatalf("imported %d records, want 1", n)
	}
	if !strings.Contains(log, "under further codes") {
		t.Errorf("printed output %q does not report the codes past the bound", log)
	}
	if got := strings.Count(log, "unrecognised"); got > maxDropCodes {
		t.Errorf("printed output names %d codes, over the %d bound", got, maxDropCodes)
	}
}

// TestDropLineClipsALongCode keeps an attacker-authored code out of the
// operator's terminal at its own length.
func TestDropLineClipsALongCode(t *testing.T) {
	dir := newSession(t, testT0)
	long := strings.Repeat("z", 4096)
	_, log := mustRun(t, Options{SessionDir: dir, Cast: writeCast(t,
		`{"version":2,"timestamp":1784300398}`,
		`[0,"`+long+`","ignored"]`,
		`[1,"o","real output\r\n"]`,
	)})
	if strings.Contains(log, strings.Repeat("z", 16)) {
		t.Errorf("printed output carries the code at full length: %q", log)
	}
	if !strings.Contains(log, "…") {
		t.Errorf("printed output %q does not mark the clip", log)
	}
}
