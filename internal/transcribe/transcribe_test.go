package transcribe

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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

// TestWhisperXRejectsUntimedSegment is the speech-at-time-0 regression: the
// segment-level start/end were value-typed float64, so a segment whose start
// whisperx omitted decoded to 0 and mapSegments filed Bob's remark as an
// utterance beginning at session time 0 — speech planted at the head of the
// evidence record, with nothing to say the engine never placed it. The
// word-level fields were already pointers, which is what made the segment-level
// omission an oversight rather than a choice. A missing end is refused too: it
// would otherwise collapse t1 onto t0 and shrink the window EventsNear joins
// interactions over.
func TestWhisperXRejectsUntimedSegment(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"missing start", `{"segments":[{"end":4.0,"text":"Bob hesitates here."}]}`, "missing start"},
		{"missing end", `{"segments":[{"start":61.5,"text":"Bob hesitates here."}]}`, "missing end"},
	} {
		segs, err := parseWhisperX([]byte(c.raw))
		if err == nil {
			// Pre-fix this branch ran, and the utterance landed at t0=0.
			utts := mapSegments(segs, 0)
			t.Fatalf("%s: want refusal, got utterances %+v", c.name, utts)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: want an error naming %q, got %v", c.name, c.want, err)
		}
	}

	// A fully timed segment must still parse — the guard rejects absence, not a
	// legitimate start of 0 at the very beginning of the recording.
	segs, err := parseWhisperX([]byte(`{"segments":[{"start":0,"end":2.5,"text":"Alice begins."}]}`))
	if err != nil {
		t.Fatalf("a segment starting at a genuine 0 must be accepted, got %v", err)
	}
	if len(segs) != 1 || segs[0].start != 0 || segs[0].end != 2.5 {
		t.Fatalf("timed segment mis-parsed: %+v", segs)
	}
}

// TestWhisperCppRejectsUntimedSegment is the same speech-at-time-0 regression on
// the whisper.cpp adapter: offsets.from/to were value-typed int64, so a segment
// whose "from" whisper-cli omitted decoded to 0 ms and Carol's remark was filed
// at session time 0 rather than where she said it. This engine emits no
// word-level timings, so the offsets are its only clock and there is nothing to
// fall back on; a missing "to" is refused for the same reason as in whisperx.
func TestWhisperCppRejectsUntimedSegment(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"missing from", `{"transcription":[{"offsets":{"to":9000},"text":"Carol scrolls back."}]}`, "missing offsets.from"},
		{"missing to", `{"transcription":[{"offsets":{"from":75000},"text":"Carol scrolls back."}]}`, "missing offsets.to"},
	} {
		segs, err := parseWhisperCpp([]byte(c.raw))
		if err == nil {
			// Pre-fix this branch ran, and the utterance landed at t0=0.
			utts := mapSegments(segs, 0)
			t.Fatalf("%s: want refusal, got utterances %+v", c.name, utts)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: want an error naming %q, got %v", c.name, c.want, err)
		}
	}

	// A genuine 0 ms offset — speech from the first instant of the recording —
	// stays acceptable; only absence is refused.
	segs, err := parseWhisperCpp([]byte(`{"transcription":[{"offsets":{"from":0,"to":2500},"text":"Alice begins."}]}`))
	if err != nil {
		t.Fatalf("a segment starting at a genuine 0 must be accepted, got %v", err)
	}
	if len(segs) != 1 || segs[0].start != 0 || segs[0].end != 2.5 {
		t.Fatalf("timed segment mis-parsed: %+v", segs)
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
	off, prov, err := resolveOffset(Options{Offset: 4.25, OffsetSet: true}, session.Manifest{T0EpochMS: 0}, true)
	if err != nil {
		t.Fatalf("explicit -offset must not consult t0: got error %v", err)
	}
	if off != 4.25 || prov != "from -offset flag" {
		t.Fatalf("explicit -offset must win: got %v (%s)", off, prov)
	}
}

// TestResolveOffsetInPlace covers a record session: no external -audio, so the
// offset is 0 by construction (capture starts at t0) with no ffprobe involved.
func TestResolveOffsetInPlace(t *testing.T) {
	off, prov, err := resolveOffset(Options{}, session.Manifest{T0EpochMS: 1_700_000_000_000}, false)
	if err != nil {
		t.Fatalf("in-place path must not fail: got error %v", err)
	}
	if off != 0 {
		t.Fatalf("in-place audio.wav offset must be 0, got %v", off)
	}
	if prov != "default 0: session audio.wav captured at t0" {
		t.Fatalf("unexpected provenance: %q", prov)
	}
}

// TestResolveOffsetInPlaceNoT0 proves the crucial constraint's in-place half: the
// record flow transcribes the session's own audio.wav (captured at t0, offset 0)
// and never derives an offset from t0, so a missing t0_epoch_ms must not fail it.
// Pre-fix resolveOffset returned no error at all, so this succeeded incidentally;
// the guard added for the external path must not leak into this branch.
func TestResolveOffsetInPlaceNoT0(t *testing.T) {
	off, prov, err := resolveOffset(Options{}, session.Manifest{T0EpochMS: 0}, false)
	if err != nil {
		t.Fatalf("in-place audio.wav with no t0 must still succeed, got error %v", err)
	}
	if off != 0 {
		t.Fatalf("in-place audio.wav offset must be 0, got %v", off)
	}
	if prov != "default 0: session audio.wav captured at t0" {
		t.Fatalf("unexpected provenance: %q", prov)
	}
}

// TestResolveOffsetExternalNoT0 is the silent-transcript-corruption regression:
// pre-fix resolveOffset took the raw man.T0EpochMS and, on the external
// recording path, handed it to deriveOffset unchecked. A received or hand-edited
// session whose manifest omits t0_epoch_ms decodes that field to 0 (a negative
// value is likewise unusable), so deriveOffset returned the recording's real
// epoch-second creation time — roughly the whole Unix epoch, ~1.78e9 s — as the
// offset, mapSegments added it to every utterance, and transcript.jsonl was
// written with times about fifty-seven years into the session while Run returned
// success. The fix reads t0 through Manifest.T0, so an unusable anchor now surfaces
// as an ErrNoT0-based error and the run refuses rather than fabricating times.
func TestResolveOffsetExternalNoT0(t *testing.T) {
	for _, m := range []session.Manifest{
		{Session: "2026-07-22_bob-received", T0EpochMS: 0},
		{Session: "2026-07-22_carol-edited", T0EpochMS: -1},
	} {
		_, _, err := resolveOffset(Options{Audio: "bob-interview.m4a"}, m, true)
		if err == nil {
			t.Fatalf("external recording with unusable t0 (%d) must fail, got nil error", m.T0EpochMS)
		}
		if !errors.Is(err, session.ErrNoT0) {
			t.Fatalf("want an ErrNoT0-based error, got %v", err)
		}
	}
}

// TestResolveOffsetExternalOffsetFlagNoT0 proves the crucial constraint that an
// explicit -offset short-circuits before t0 is consulted: an operator who states
// the offset needs no anchor, so a missing t0_epoch_ms must not fail the run even
// on the external path. Pre-fix the raw field was passed through regardless; the
// flag branch now returns before Manifest.T0 is called at all.
func TestResolveOffsetExternalOffsetFlagNoT0(t *testing.T) {
	off, prov, err := resolveOffset(Options{Audio: "bob-interview.m4a", Offset: 3.0, OffsetSet: true}, session.Manifest{T0EpochMS: 0}, true)
	if err != nil {
		t.Fatalf("explicit -offset must succeed without a t0, got error %v", err)
	}
	if off != 3.0 || prov != "from -offset flag" {
		t.Fatalf("explicit -offset must win without consulting t0: got %v (%s)", off, prov)
	}
}

// TestSameFileTreatedInPlace proves -audio pointing at the session's own
// audio.wav is recognised as the in-place case (no self-conversion).
func TestSameFileTreatedInPlace(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, session.AudioFile)
	if err := os.WriteFile(wav, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !sameFile(wav, wav) {
		t.Fatal("a file must be the same file as itself")
	}
	if sameFile(filepath.Join(dir, "other.wav"), wav) {
		t.Fatal("distinct paths must not be reported as the same file")
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

// TestConvertAudioRefusesSymlinkOutput is the arbitrary-file-overwrite
// regression: ffmpeg -y would follow a symlink at the output path, so a
// pre-planted audio.wav symlink in an untrusted session must be refused before
// ffmpeg runs. Hermetic: the guard fires before the ffmpeg PATH lookup.
func TestConvertAudioRefusesSymlinkOutput(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "voice.wav")
	if err := os.WriteFile(in, []byte("not really audio"), 0o644); err != nil {
		t.Fatalf("seed input: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	out := filepath.Join(dir, session.AudioFile)
	if err := os.Symlink(outside, out); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// The guard must fire specifically on the symlink, before the ffmpeg PATH
	// lookup — otherwise (with ffmpeg present) the victim would be overwritten,
	// and on a machine without ffmpeg the error would merely be "not found".
	err := convertAudio(in, out)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("want symlink refusal, got %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "original\n" {
		t.Fatalf("victim overwritten through symlink: %q", b)
	}
}

// TestConvertAudioRefusesFIFOOutput is the hang regression: pre-fix the
// output-path guard tested only for ModeSymlink, so a FIFO planted at audio.wav
// in a session Alice merely received from Bob passed the check and ffmpeg's
// open(2) then blocked for ever waiting for a reader, hanging `testimony
// transcribe` with no error. The conversion runs in a goroutine and the test
// fails on a timeout, so a regression reports a failure rather than hanging the
// suite for ever.
func TestConvertAudioRefusesFIFOOutput(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "voice.wav")
	if err := os.WriteFile(in, []byte("not really audio"), 0o644); err != nil {
		t.Fatalf("seed input: %v", err)
	}
	out := filepath.Join(dir, session.AudioFile)
	if err := syscall.Mkfifo(out, 0o644); err != nil {
		t.Skipf("FIFOs unavailable on this platform: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- convertAudio(in, out) }()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("want non-regular-file refusal, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("convertAudio blocked on a FIFO output instead of refusing it")
	}
}

// TestConvertAudioRefusesFIFOInput is the input-side half of the same hang: the
// pre-fix existence check was a bare os.Stat, which a FIFO satisfies, so ffmpeg
// was handed a path whose open(2) never returns. A symlink to a real recording
// must still be accepted — os.Stat resolves it, and only writes are redirected
// by a symlink.
func TestConvertAudioRefusesFIFOInput(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "voice.wav")
	if err := syscall.Mkfifo(in, 0o644); err != nil {
		t.Skipf("FIFOs unavailable on this platform: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- convertAudio(in, filepath.Join(dir, session.AudioFile)) }()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("want non-regular-file refusal, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("convertAudio blocked on a FIFO input instead of refusing it")
	}

	real := filepath.Join(dir, "bob-interview.wav")
	if err := os.WriteFile(real, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.wav")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A symlinked recording is legitimate, so it must get past the input guard
	// and fail (if at all) only later, on the ffmpeg lookup or the conversion.
	if err := convertAudio(link, filepath.Join(dir, session.AudioFile)); err != nil &&
		strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink to a regular recording must be accepted, got %v", err)
	}
}

// TestCheckSessionAudioRefusesFIFO is the in-place branch of the same hang: with
// no -audio flag the session's own audio.wav is passed straight to the ASR
// engine, and pre-fix a bare os.Stat was the only check, so a FIFO planted there
// blocked the engine's read for ever. An absent file must still produce the
// actionable "run record first" message rather than this refusal.
func TestCheckSessionAudioRefusesFIFO(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, session.AudioFile)

	err := checkSessionAudio(wav, dir)
	if err == nil || !strings.Contains(err.Error(), "run `testimony record` first") {
		t.Fatalf("missing audio must stay an actionable error, got %v", err)
	}

	if err := syscall.Mkfifo(wav, 0o644); err != nil {
		t.Skipf("FIFOs unavailable on this platform: %v", err)
	}
	if err := checkSessionAudio(wav, dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("want non-regular-file refusal for a FIFO audio.wav, got %v", err)
	}

	if err := os.Remove(wav); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wav, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkSessionAudio(wav, dir); err != nil {
		t.Fatalf("a real audio.wav must be accepted, got %v", err)
	}
}
