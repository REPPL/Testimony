package transcribe

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

// mapFixture parses a committed engine-output fixture, maps it with the
// given offset, and returns the utterances plus the transcript lines as
// they would be written to transcript.jsonl.
func mapFixture(t *testing.T, name string, parse func([]byte) ([]segment, error), offset float64) ([]timeline.Utterance, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	segs, err := parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	utts := mapSegments(segs, offset)

	out := filepath.Join(t.TempDir(), session.TranscriptFile)
	if err := session.WriteJSONL(out, utts); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return utts, string(got)
}

func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWhisperXFixture(t *testing.T) {
	utts, got := mapFixture(t, "whisperx.json", parseWhisperX, 0)

	// Multi-segment mapping: the blank segment is skipped, IDs stay sequential.
	if len(utts) != 3 {
		t.Fatalf("want 3 utterances (blank segment skipped), got %d", len(utts))
	}
	for i, want := range []string{"utt-001", "utt-002", "utt-003"} {
		if utts[i].ID != want {
			t.Fatalf("utterance %d: want ID %s, got %s", i, want, utts[i].ID)
		}
	}

	// Word timestamps: "Alice" has no start time and must be omitted.
	if len(utts[0].Words) != 6 {
		t.Fatalf("utt-001: want 6 timed words (unaligned word omitted), got %d", len(utts[0].Words))
	}
	for _, w := range utts[0].Words {
		if w.W == "Alice" {
			t.Fatalf("utt-001: word without start time should be omitted, got %+v", w)
		}
	}

	// Diarisation labels pass through; their absence defaults to P1.
	if utts[0].Speaker != "SPEAKER_00" || utts[1].Speaker != "SPEAKER_01" {
		t.Fatalf("diarisation labels not preserved: %q, %q", utts[0].Speaker, utts[1].Speaker)
	}
	if utts[2].Speaker != "P1" {
		t.Fatalf("missing speaker should default to P1, got %q", utts[2].Speaker)
	}
	if utts[2].Words != nil {
		t.Fatalf("utt-003 has no word list, got %v", utts[2].Words)
	}

	// Text is trimmed and times are rounded to 2 dp — golden-compare the lines.
	if want := golden(t, "whisperx.golden.jsonl"); got != want {
		t.Fatalf("transcript lines differ from golden:\n got: %s\nwant: %s", got, want)
	}
}

func TestWhisperCppFixture(t *testing.T) {
	// Offset 2.5 s: audio clock → session clock shift applied to every time.
	utts, got := mapFixture(t, "whispercpp.json", parseWhisperCpp, 2.5)

	if len(utts) != 2 {
		t.Fatalf("want 2 utterances (empty segment skipped), got %d", len(utts))
	}
	if utts[0].T0 != 3.5 || utts[0].T1 != 6.04 {
		t.Fatalf("offset not applied: got t0=%v t1=%v, want 3.5/6.04", utts[0].T0, utts[0].T1)
	}
	// The empty middle segment must not consume an ID.
	if utts[1].ID != "utt-002" {
		t.Fatalf("IDs must stay sequential across skips, got %s", utts[1].ID)
	}
	if utts[1].Speaker != "P1" {
		t.Fatalf("whisper.cpp has no diarisation; speaker should be P1, got %q", utts[1].Speaker)
	}

	if want := golden(t, "whispercpp.golden.jsonl"); got != want {
		t.Fatalf("transcript lines differ from golden:\n got: %s\nwant: %s", got, want)
	}
}

func TestMapSegmentsNegativeOffset(t *testing.T) {
	utts := mapSegments([]segment{
		{start: 10.0, end: 12.345, text: " Carol pauses. ", words: []timeline.Word{{W: " Carol ", T: 10.004}}},
	}, -1.5)
	if len(utts) != 1 {
		t.Fatalf("want 1 utterance, got %d", len(utts))
	}
	u := utts[0]
	if u.T0 != 8.5 || u.T1 != 10.85 {
		t.Fatalf("negative offset: got t0=%v t1=%v, want 8.5/10.85", u.T0, u.T1)
	}
	if u.Text != "Carol pauses." {
		t.Fatalf("text not trimmed: %q", u.Text)
	}
	if len(u.Words) != 1 || u.Words[0].W != "Carol" || u.Words[0].T != 8.5 {
		t.Fatalf("word not trimmed/shifted/rounded: %+v", u.Words)
	}
}

func TestResolveOffsetFlagWins(t *testing.T) {
	off, prov := resolveOffset(Options{Offset: 4.25, OffsetSet: true}, 0)
	if off != 4.25 || prov != "from -offset flag" {
		t.Fatalf("explicit -offset must win: got %v (%s)", off, prov)
	}
}

func TestParseCreationTime(t *testing.T) {
	for _, ok := range []string{"2026-07-17T15:30:00.000000Z", "2026-07-17T15:30:00Z", "2026-07-17T15:30:00.5"} {
		if _, parsed := parseCreationTime(ok); !parsed {
			t.Fatalf("should parse %q", ok)
		}
	}
	for _, bad := range []string{"", "yesterday"} {
		if _, parsed := parseCreationTime(bad); parsed {
			t.Fatalf("should reject %q", bad)
		}
	}
}

func TestResolveCompute(t *testing.T) {
	cases := []struct {
		name                    string
		devicePref, computePref string
		goos                    string
		hasCUDA                 bool
		wantDevice, wantCompute string
	}{
		// whisperx's own defaults (cuda/float16) must never be relied on:
		// macOS has no CUDA, so auto resolves to cpu/int8 regardless.
		{"darwin auto", "auto", "auto", "darwin", false, "cpu", "int8"},
		{"darwin auto ignores nvidia-smi", "auto", "auto", "darwin", true, "cpu", "int8"},
		{"linux auto no gpu", "auto", "auto", "linux", false, "cpu", "int8"},
		{"linux auto with gpu", "auto", "auto", "linux", true, "cuda", "float16"},
		{"empty prefs behave as auto", "", "", "darwin", false, "cpu", "int8"},
		// Explicit values pass through untouched.
		{"explicit device", "cpu", "auto", "linux", true, "cpu", "int8"},
		{"explicit compute", "auto", "float32", "darwin", false, "cpu", "float32"},
		{"explicit both", "cuda", "int8_float16", "linux", false, "cuda", "int8_float16"},
	}
	for _, c := range cases {
		device, compute := resolveCompute(c.devicePref, c.computePref, c.goos, c.hasCUDA)
		if device != c.wantDevice || compute != c.wantCompute {
			t.Errorf("%s: got %s/%s, want %s/%s", c.name, device, compute, c.wantDevice, c.wantCompute)
		}
	}
}

func TestDetectEngineUnknown(t *testing.T) {
	if _, _, err := detectEngine("siri"); err == nil {
		t.Fatal("unknown engine must error")
	}
}

// TestConvertAudioIntegration exercises the real ffmpeg conversion. It skips
// on machines without ffmpeg (CI has none); no model or network is involved.
func TestConvertAudioIntegration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping conversion integration test")
	}

	dir := t.TempDir()
	in := filepath.Join(dir, "voice.wav")
	// Synthesize a 0.2 s stereo test tone as the "recording".
	gen := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-ac", "2", in)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg synth: %v\n%s", err, out)
	}

	out := filepath.Join(dir, session.AudioFile)
	if err := convertAudio(in, out); err != nil {
		t.Fatalf("convertAudio: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty %s: %v", session.AudioFile, err)
	}

	if err := convertAudio(filepath.Join(dir, "voice.mp3"), out); err == nil {
		t.Fatal("unsupported extension must error")
	}
}

func TestResolveVAD(t *testing.T) {
	cases := []struct{ pref, want string }{
		{"", "silero"},
		{"auto", "silero"},
		{"silero", "silero"},
		{"pyannote", "pyannote"},
	}
	for _, c := range cases {
		if got := resolveVAD(c.pref); got != c.want {
			t.Errorf("resolveVAD(%q) = %q, want %q", c.pref, got, c.want)
		}
	}
}
