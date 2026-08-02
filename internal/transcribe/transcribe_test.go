package transcribe

import (
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

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

// TestParseSegmentRejectsImplausibleTime is the +Inf regression: mapSegments'
// round2 (x*100) silently overflowed to +Inf for a segment start near 1e307,
// aborting the run with a bare "json: unsupported value: +Inf" at WriteJSONL,
// and a segment between maxOffsetSeconds and ~1.8e306 wrote a transcript at
// exit 0 that merge only refused one command later — naming transcript.jsonl
// rather than the engine that produced the bad time. Both engines' parsers
// must refuse an implausible start/end where it enters, matching their
// existing missing-start/missing-end refusals.
func TestParseSegmentRejectsImplausibleTime(t *testing.T) {
	for _, c := range []struct{ name, raw, want string }{
		{"whisperx huge start", `{"segments":[{"start":1e307,"end":1e307,"text":"Bob."}]}`, "implausible start"},
		{"whisperx huge end", `{"segments":[{"start":0,"end":2e9,"text":"Bob."}]}`, "implausible end"},
	} {
		if _, err := parseWhisperX([]byte(c.raw)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got %v", c.name, c.want, err)
		}
	}
	for _, c := range []struct{ name, raw, want string }{
		{"whispercpp huge from", `{"transcription":[{"offsets":{"from":1000000000000000,"to":1000000000000001},"text":"Carol."}]}`, "implausible offsets.from"},
		{"whispercpp huge to", `{"transcription":[{"offsets":{"from":0,"to":2000000000000},"text":"Carol."}]}`, "implausible offsets.to"},
	} {
		if _, err := parseWhisperCpp([]byte(c.raw)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got %v", c.name, c.want, err)
		}
	}

	// A large but plausible time (well under maxOffsetSeconds) still parses.
	segs, err := parseWhisperX([]byte(`{"segments":[{"start":86400,"end":86405,"text":"Alice logs in."}]}`))
	if err != nil || len(segs) != 1 {
		t.Fatalf("a plausible segment must be accepted, got segs=%+v err=%v", segs, err)
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

// TestCheckOffset pins the explicit-flag bound: the derived and sidecar paths
// refuse a non-finite or over-magnitude offset where the bad value enters, and
// the flag path must apply the same rule. Every genuine offset (including the
// exact ±1e9 boundary and negative values) stays accepted.
func TestCheckOffset(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 1e300, -1e300, 1e9 + 1} {
		if err := CheckOffset(v); err == nil {
			t.Errorf("CheckOffset(%v): want a refusal, got nil", v)
		}
	}
	for _, v := range []float64{0, 4.25, -12.4, 1e9, -1e9} {
		if err := CheckOffset(v); err != nil {
			t.Errorf("CheckOffset(%v): want acceptance, got %v", v, err)
		}
	}
}

// TestCheckDevice pins the closed -device enum docs/reference/cli.md
// documents. Pre-fix, an unrecognised value passed straight to whisperx and
// only failed after the offset resolution (and, on the -audio path, the
// audio conversion) had already run, at exit 1 rather than the exit 2 the
// docs promise for an invalid flag value.
func TestCheckDevice(t *testing.T) {
	for _, v := range []string{"", "auto", "cpu", "cuda"} {
		if err := CheckDevice(v); err != nil {
			t.Errorf("CheckDevice(%q): want acceptance, got %v", v, err)
		}
	}
	for _, v := range []string{"gpu", "cudda", "CPU"} {
		if err := CheckDevice(v); err == nil {
			t.Errorf("CheckDevice(%q): want a refusal, got nil", v)
		}
	}
}

// TestCheckVAD is CheckDevice's sibling for the closed -vad enum.
func TestCheckVAD(t *testing.T) {
	for _, v := range []string{"", "auto", "silero", "pyannote"} {
		if err := CheckVAD(v); err != nil {
			t.Errorf("CheckVAD(%q): want acceptance, got %v", v, err)
		}
	}
	for _, v := range []string{"webrtc", "silreo"} {
		if err := CheckVAD(v); err == nil {
			t.Errorf("CheckVAD(%q): want a refusal, got nil", v)
		}
	}
}

// TestCheckAudioExt pins the closed -audio extension set docs/reference/cli.md
// documents (.m4a, .mov, .wav). Pre-fix, an unsupported extension was reported
// only from checkExternalAudio inside transcribe.Run, after detectEngine had
// already run — on a machine with no ASR engine installed this surfaced as
// "no ASR engine found" at exit 1, masking the actual mistake, rather than the
// exit 2 the docs promise for an invalid flag value.
func TestCheckAudioExt(t *testing.T) {
	for _, v := range []string{"rec.m4a", "rec.MOV", "audio.wav", "/a/b/c.M4A"} {
		if err := CheckAudioExt(v); err != nil {
			t.Errorf("CheckAudioExt(%q): want acceptance, got %v", v, err)
		}
	}
	for _, v := range []string{"rec.txt", "rec.mp3", "rec", "rec.wav.bak"} {
		if err := CheckAudioExt(v); err == nil {
			t.Errorf("CheckAudioExt(%q): want a refusal, got nil", v)
		}
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

// TestResolveOffsetInPlaceReadsSidecar is the re-run-offset-loss regression. A
// first `transcribe -audio rec.m4a` converts the recording into audio.wav and
// derives a nonzero offset; a later bare `transcribe` reuses audio.wav and hits
// the in-place branch. Pre-fix that branch always returned 0, silently shifting
// every utterance by the forgotten offset. The offset is now persisted in a
// sidecar beside audio.wav and read back here. write-then-read round-trips the
// value.
func TestResolveOffsetInPlaceReadsSidecar(t *testing.T) {
	dir := t.TempDir()
	if err := writeOffsetSidecar(dir, 12.4, "derived: audio creation_time − manifest t0"); err != nil {
		t.Fatalf("writeOffsetSidecar: %v", err)
	}
	off, prov, err := resolveOffset(Options{SessionDir: dir}, session.Manifest{T0EpochMS: 1_700_000_000_000}, false)
	if err != nil {
		t.Fatalf("in-place with a sidecar must succeed: %v", err)
	}
	if off != 12.4 {
		t.Fatalf("offset: got %v, want 12.4 (from the sidecar, not the naive 0)", off)
	}
	if !strings.Contains(prov, "persisted") {
		t.Fatalf("provenance should name the persisted origin, got %q", prov)
	}
}

// TestResolveOffsetInPlaceRefusesBadSidecar covers the refuse-with-guidance
// fallback: a present-but-unusable sidecar (here malformed JSON) means the audio
// is known external but its offset is unrecoverable, so the run must refuse and
// tell the operator how to state the offset, never silently fall back to 0.
func TestResolveOffsetInPlaceRefusesBadSidecar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, session.AudioOffsetFile), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	_, _, err := resolveOffset(Options{SessionDir: dir}, session.Manifest{T0EpochMS: 1}, false)
	if err == nil || !strings.Contains(err.Error(), "re-run with -audio") {
		t.Fatalf("a malformed sidecar must refuse with guidance, got %v", err)
	}
}

// TestResolveOffsetRefusesOversizedSidecar is the offset-magnitude regression: a
// hand-edited sidecar carrying an astronomical offset (finite, so it clears the
// NaN/Inf check) would shift every re-run's utterance past the magnitude merge
// accepts, so the corruption surfaces one command later at merge. Bounding the offset
// at read time refuses with guidance naming the sidecar as the fault. A large-but-
// in-bounds offset still round-trips.
func TestResolveOffsetRefusesOversizedSidecar(t *testing.T) {
	dir := t.TempDir()
	if err := writeOffsetSidecar(dir, 5e9, "derived: audio creation_time − manifest t0"); err != nil {
		t.Fatalf("writeOffsetSidecar: %v", err)
	}
	_, _, err := resolveOffset(Options{SessionDir: dir}, session.Manifest{T0EpochMS: 1}, false)
	if err == nil || !strings.Contains(err.Error(), "re-run with -audio") {
		t.Fatalf("an oversized sidecar offset must refuse with guidance, got %v", err)
	}

	// A large but in-bounds offset (10 hours) is still accepted and round-trips.
	if err := writeOffsetSidecar(dir, 36000, "derived: audio creation_time − manifest t0"); err != nil {
		t.Fatalf("writeOffsetSidecar: %v", err)
	}
	off, _, err := resolveOffset(Options{SessionDir: dir}, session.Manifest{T0EpochMS: 1}, false)
	if err != nil {
		t.Fatalf("an in-bounds sidecar offset must be accepted: %v", err)
	}
	if off != 36000 {
		t.Fatalf("offset: got %v, want 36000", off)
	}
}

// TestResolveOffsetRefusesOversizedDerivedOffset is the derived half of the
// offset-magnitude bound (the sidecar half has its own test above): a poisoned
// recording creation_time (attacker-influenceable metadata) that derives an
// astronomical offset must refuse at derivation, naming the input, rather than
// bake a decades-long shift into every utterance for merge to reject one
// command later. The ffprobe-gated derivation is stubbed through the
// deriveOffsetFn seam so the bound itself is exercised hermetically.
func TestResolveOffsetRefusesOversizedDerivedOffset(t *testing.T) {
	old := deriveOffsetFn
	t.Cleanup(func() { deriveOffsetFn = old })

	deriveOffsetFn = func(audio string, t0EpochMS int64) (float64, bool) { return 5e9, true }
	_, _, err := resolveOffset(Options{Audio: "ext.wav"}, session.Manifest{T0EpochMS: 1}, true)
	if err == nil || !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "-offset") {
		t.Fatalf("an oversized derived offset must refuse with -offset guidance, got %v", err)
	}

	// A large but in-bounds derived offset (10 hours) still resolves.
	deriveOffsetFn = func(audio string, t0EpochMS int64) (float64, bool) { return 36000, true }
	off, provenance, err := resolveOffset(Options{Audio: "ext.wav"}, session.Manifest{T0EpochMS: 1}, true)
	if err != nil {
		t.Fatalf("an in-bounds derived offset must be accepted: %v", err)
	}
	if off != 36000 || !strings.Contains(provenance, "derived") {
		t.Fatalf("offset: got %v (%q), want 36000 (derived)", off, provenance)
	}
}

// TestResolveOffsetInPlaceNoSidecarIsRecordOrigin proves the record flow is
// untouched: audio.wav with no sidecar is captured at t0, so the offset is 0.
func TestResolveOffsetInPlaceNoSidecarIsRecordOrigin(t *testing.T) {
	dir := t.TempDir()
	off, prov, err := resolveOffset(Options{SessionDir: dir}, session.Manifest{T0EpochMS: 1}, false)
	if err != nil {
		t.Fatalf("record-origin in-place must succeed: %v", err)
	}
	if off != 0 || !strings.Contains(prov, "captured at t0") {
		t.Fatalf("record-origin must be offset 0 captured at t0, got %v (%s)", off, prov)
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

// recordOriginAudio stands in for the bytes `testimony record` captured into
// audio.wav — the irreplaceable original a failed transcribe run must not
// destroy.
const recordOriginAudio = "RIFF record-origin capture"

// fakeTools puts a stub whisperx, ffmpeg, and ffprobe on PATH so Run's
// detectEngine, convertAudio, and deriveOffset all resolve without any of the
// three installed. The stub whisperx writes a minimal valid engine output into
// the --output_dir it is handed (always its last argument). The stub ffprobe
// READS the recording it is pointed at, exactly as the real one does — that is
// what makes a FIFO at -audio block its open(2) for ever.
func fakeTools(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	whisperx := "#!/bin/sh\nfor last; do :; done\n" +
		`printf '%s' '{"segments":[{"start":1,"end":2,"text":"Alice speaks."}]}' > "$last/audio.json"` + "\n"
	// `: < FILE` opens the recording with a shell builtin — PATH here holds only
	// these stubs, so an external reader such as cat would not be found and the
	// open would never happen.
	ffprobe := "#!/bin/sh\nfor last; do :; done\n: < \"$last\"\nprintf '%s' '{}'\n"
	for name, script := range map[string]string{
		"whisperx": whisperx,
		"ffmpeg":   "#!/bin/sh\nexit 0\n",
		"ffprobe":  ffprobe,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

// seedSession writes a manifest and a record-origin audio.wav, returning the
// session directory and the audio path.
func seedSession(t *testing.T, m session.Manifest) (dir, wav string) {
	t.Helper()
	dir = t.TempDir()
	if err := session.SaveManifest(dir, m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	wav = filepath.Join(dir, session.AudioFile)
	if err := os.WriteFile(wav, []byte(recordOriginAudio), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, wav
}

// stubConvert stands in for ffmpeg and reports, through the returned pointer,
// whether the conversion ran at all. It writes different bytes from
// recordOriginAudio, so a conversion that reached audio.wav is visible in the
// file's content.
func stubConvert(t *testing.T) *bool {
	t.Helper()
	old := convertRunner
	t.Cleanup(func() { convertRunner = old })
	ran := false
	convertRunner = func(ffmpeg, in, tmpPath string) error {
		ran = true
		return os.WriteFile(tmpPath, []byte("RIFF converted external recording"), 0o644)
	}
	return &ran
}

// externalRecording writes a stand-in recording outside the session directory.
func externalRecording(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bob-interview.m4a")
	if err := os.WriteFile(path, []byte("external recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunResolvesOffsetBeforeConverting is the destroyed-capture regression.
// Pre-fix Run converted the external recording over the session's audio.wav
// BEFORE resolving the offset, so every way resolution can fail — an unusable
// manifest t0, an implausible derived offset — exited 1 with the record-origin
// capture already overwritten and no audio.offset.json written. The session was
// then indistinguishable from a record-origin one (no sidecar ⇒ captured at t0,
// docs/reference/session-directory.md), so a later bare `transcribe` took the
// in-place branch, reported the false provenance "captured at t0", and wrote a
// silently time-shifted transcript at exit 0. Resolution now runs first, so a
// refused run leaves the session exactly as it found it.
func TestRunResolvesOffsetBeforeConverting(t *testing.T) {
	t.Run("unusable t0", func(t *testing.T) {
		fakeTools(t)
		// No t0_epoch_ms: a received or hand-edited manifest.
		dir, wav := seedSession(t, session.Manifest{Session: "2026-07-22_bob-received"})
		converted := stubConvert(t)

		_, err := Run(Options{SessionDir: dir, Audio: externalRecording(t), Engine: EngineWhisperX, Log: io.Discard})
		if err == nil {
			t.Fatal("an external recording with no usable t0 must fail the run")
		}
		if !errors.Is(err, session.ErrNoT0) {
			t.Fatalf("want an ErrNoT0-based error, got %v", err)
		}
		assertSessionUntouched(t, dir, wav, converted)
	})

	t.Run("implausible derived offset", func(t *testing.T) {
		fakeTools(t)
		dir, wav := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})
		converted := stubConvert(t)
		old := deriveOffsetFn
		t.Cleanup(func() { deriveOffsetFn = old })
		deriveOffsetFn = func(audio string, t0EpochMS int64) (float64, bool) { return 5e9, true }

		_, err := Run(Options{SessionDir: dir, Audio: externalRecording(t), Engine: EngineWhisperX, Log: io.Discard})
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("an implausible derived offset must fail the run, got %v", err)
		}
		assertSessionUntouched(t, dir, wav, converted)
	})
}

// assertSessionUntouched checks that a refused run left neither a converted
// audio.wav nor a sidecar behind.
func assertSessionUntouched(t *testing.T, dir, wav string, converted *bool) {
	t.Helper()
	if *converted {
		t.Error("the conversion ran before the offset was resolved; a refused run must not touch the session's audio")
	}
	if b, err := os.ReadFile(wav); err != nil || string(b) != recordOriginAudio {
		t.Errorf("the record-origin %s was replaced by a refused run: %q (err=%v)", session.AudioFile, b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, session.AudioOffsetFile)); !os.IsNotExist(err) {
		t.Errorf("a refused run wrote %s (err=%v); the session now claims an offset it never resolved", session.AudioOffsetFile, err)
	}
}

// TestSidecarRefusalDoesNotDestroyAudio closes the residual gap in the
// resolve-before-convert invariant: resolution was hoisted ahead of the
// conversion, but the sidecar persist still ran after it, so a refused sidecar
// write — a directory or symlink planted at audio.offset.json in a received
// session, ENOSPC — exited 1 with the record-origin audio.wav already replaced
// by the converted external recording and no offset recorded. The persist now
// happens before the rename that replaces audio.wav, so this refusal too
// leaves the session byte-for-byte as it found it.
func TestSidecarRefusalDoesNotDestroyAudio(t *testing.T) {
	fakeTools(t)
	dir, wav := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})
	stubConvert(t)
	if err := os.Mkdir(filepath.Join(dir, session.AudioOffsetFile), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Options{SessionDir: dir, Audio: externalRecording(t), Engine: EngineWhisperX, Log: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "persist audio offset") {
		t.Fatalf("want the sidecar persist refusal, got %v", err)
	}
	if b, rerr := os.ReadFile(wav); rerr != nil || string(b) != recordOriginAudio {
		t.Errorf("the record-origin %s was replaced by a run refused at the sidecar: %q (err=%v)", session.AudioFile, b, rerr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 { // manifest.json, audio.wav, the planted directory
		t.Errorf("a refused run left extra files behind: %v", entries)
	}
}

// TestRenameFailureRollsBackSidecar pins the other half of the persist-before-
// rename ordering: when the rename itself fails after the sidecar was written,
// the sidecar must not survive — a session carrying a persisted offset for an
// audio.wav that was never converted primes every later bare run to apply that
// offset to record-origin audio at exit 0.
func TestRenameFailureRollsBackSidecar(t *testing.T) {
	fakeTools(t)
	dir, wav := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})
	old := convertRunner
	t.Cleanup(func() { convertRunner = old })
	convertRunner = func(_, _, tmpPath string) error {
		if err := os.WriteFile(tmpPath, []byte("RIFF converted external recording"), 0o644); err != nil {
			return err
		}
		// Sabotage the finalise: a non-empty directory at audio.wav makes the
		// rename fail after the conversion (and the sidecar write) succeeded.
		if err := os.Remove(wav); err != nil {
			return err
		}
		if err := os.Mkdir(wav, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(wav, "occupied"), []byte("x"), 0o644)
	}

	_, err := Run(Options{SessionDir: dir, Audio: externalRecording(t), Engine: EngineWhisperX, Log: io.Discard})
	if err == nil {
		t.Fatal("a failed finalise must fail the run")
	}
	if _, statErr := os.Stat(filepath.Join(dir, session.AudioOffsetFile)); !os.IsNotExist(statErr) {
		t.Errorf("the sidecar survived a failed finalise (err=%v); the session claims an offset for audio that was never converted", statErr)
	}
}

// TestRenameFailureKeepsUncapturablePriorSidecar pins the third rollback
// state: a prior sidecar too large to capture (the bounded read refuses it —
// a received session can ship anything at this name) must survive a failed
// finalise as whatever this run wrote, never be removed. Removing a sidecar
// the run did not create would relabel external audio as record-origin — the
// silent shift the sidecar exists to prevent. The bound itself is the point:
// the capture read stops at the cap instead of buffering an arbitrarily large
// attacker-authored file.
func TestRenameFailureKeepsUncapturablePriorSidecar(t *testing.T) {
	fakeTools(t)
	dir, wav := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})
	// An over-cap prior: readable, regular, but past maxOffsetSidecarBytes.
	huge := make([]byte, maxOffsetSidecarBytes+2)
	for i := range huge {
		huge[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(dir, session.AudioOffsetFile), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	old := convertRunner
	t.Cleanup(func() { convertRunner = old })
	convertRunner = func(_, _, tmpPath string) error {
		if err := os.WriteFile(tmpPath, []byte("RIFF converted external recording"), 0o644); err != nil {
			return err
		}
		if err := os.Remove(wav); err != nil {
			return err
		}
		if err := os.Mkdir(wav, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(wav, "occupied"), []byte("x"), 0o644)
	}

	_, err := Run(Options{SessionDir: dir, Audio: externalRecording(t), Engine: EngineWhisperX, Log: io.Discard})
	if err == nil {
		t.Fatal("a failed finalise must fail the run")
	}
	if _, statErr := os.Stat(filepath.Join(dir, session.AudioOffsetFile)); statErr != nil {
		t.Errorf("the sidecar was removed on rollback despite a pre-existing one the run could not capture: %v", statErr)
	}
}

// TestRunRefusesNonRegularExternalAudio is the hang regression on the offset
// derivation. The refusals that make -audio safe to open — the accepted-container
// check and the regular-file check — lived inside convertAudio, so hoisting the
// offset resolution above the conversion put an ffprobe subprocess on the
// operator-named path AHEAD of them: ffprobe opens without O_NONBLOCK, so a FIFO
// at -audio (a session bundle is an exchange unit, and tar preserves FIFOs)
// blocked its open(2) for ever and `testimony transcribe` hung with no error
// instead of refusing in milliseconds. The guard now runs before anything opens
// the recording. The run happens in a goroutine and the test fails on a timeout,
// so a regression reports a failure rather than hanging the suite for ever.
func TestRunRefusesNonRegularExternalAudio(t *testing.T) {
	fakeTools(t)
	dir, _ := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})
	fifo := filepath.Join(t.TempDir(), "bob-interview.m4a")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("FIFOs unavailable on this platform: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Run(Options{SessionDir: dir, Audio: fifo, Engine: EngineWhisperX, Log: io.Discard})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("want a non-regular-file refusal, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transcribe blocked on a FIFO -audio instead of refusing it")
	}
}

// TestRunRefusesUnsupportedExternalAudioBeforeProbing is the same ordering
// invariant on the container check: a file whose extension the pipeline does not
// accept must be refused by name, before ffprobe is asked to parse it — the
// probe is a media parser, and pointing it at arbitrary operator-named files
// widens its attack surface for nothing.
func TestRunRefusesUnsupportedExternalAudioBeforeProbing(t *testing.T) {
	fakeTools(t)
	dir, _ := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})
	notes := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notes, []byte("not a recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := deriveOffsetFn
	t.Cleanup(func() { deriveOffsetFn = old })
	probed := false
	deriveOffsetFn = func(audio string, t0EpochMS int64) (float64, bool) {
		probed = true
		return 0, false
	}

	_, err := Run(Options{SessionDir: dir, Audio: notes, Engine: EngineWhisperX, Log: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "unsupported audio format") {
		t.Fatalf("want an unsupported-format refusal, got %v", err)
	}
	if probed {
		t.Error("the recording was probed before its container was accepted")
	}
}

// TestRunExplicitOffsetUpdatesExistingSidecar is the discarded-correction
// regression: an operator who spots a wrong offset re-runs with -offset N, but
// pre-fix the in-place path never rewrote the sidecar, so the correction applied
// to that one transcript and the next bare re-run silently resurrected the stale
// value. An existing sidecar is the session's record of an external audio origin,
// so an explicit -offset now rewrites it.
func TestRunExplicitOffsetUpdatesExistingSidecar(t *testing.T) {
	fakeTools(t)
	man := session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000}
	dir, _ := seedSession(t, man)
	if err := writeOffsetSidecar(dir, 12.5, "derived: audio creation_time − manifest t0"); err != nil {
		t.Fatalf("writeOffsetSidecar: %v", err)
	}

	if _, err := Run(Options{SessionDir: dir, Engine: EngineWhisperX, Offset: 30, OffsetSet: true, Log: io.Discard}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	off, _, ok, err := readOffsetSidecar(dir)
	if err != nil || !ok {
		t.Fatalf("sidecar after the correction: ok=%v err=%v", ok, err)
	}
	if off != 30 {
		t.Fatalf("sidecar offset: got %v, want 30 (the operator's correction, not the stale 12.5)", off)
	}
	// The next bare run must resolve to the corrected value.
	got, _, err := resolveOffset(Options{SessionDir: dir}, man, false)
	if err != nil {
		t.Fatalf("bare re-run: %v", err)
	}
	if got != 30 {
		t.Fatalf("bare re-run offset: got %v, want 30", got)
	}
}

// TestRunExplicitOffsetWritesNoSidecarForRecordOrigin guards the other half of
// the sidecar model: a session with no sidecar is record-origin (audio.wav
// captured at t0), and an explicit -offset for one transcript must not invent a
// sidecar that would make every later bare run inherit it.
func TestRunExplicitOffsetWritesNoSidecarForRecordOrigin(t *testing.T) {
	fakeTools(t)
	dir, _ := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})

	if _, err := Run(Options{SessionDir: dir, Engine: EngineWhisperX, Offset: 30, OffsetSet: true, Log: io.Discard}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, session.AudioOffsetFile)); !os.IsNotExist(err) {
		t.Fatalf("a record-origin session must stay sidecar-free, got err=%v", err)
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
	if err := convertAudio(in, out, nil); err != nil {
		t.Fatalf("convertAudio: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("expected non-empty %s: %v", session.AudioFile, err)
	}
	// Atomicity: a successful conversion leaves no `.audio-*.wav` temp behind (it was
	// renamed over out, and the deferred cleanup is a no-op).
	if temps, _ := filepath.Glob(filepath.Join(dir, ".audio-*.wav")); len(temps) != 0 {
		t.Fatalf("conversion left temp files behind: %v", temps)
	}

	if err := convertAudio(filepath.Join(dir, "voice.mp3"), out, nil); err == nil {
		t.Fatal("unsupported extension must error")
	}

	// A conversion where ffmpeg itself fails — a corrupt recording wearing a .wav
	// extension — must leave no output and no temp. This pins the convertAudio →
	// atomicConvert wiring at the call site: the hermetic helper test alone stays
	// green if a later edit routes ffmpeg straight at out again.
	failDir := t.TempDir()
	corrupt := filepath.Join(failDir, "corrupt.wav")
	if err := os.WriteFile(corrupt, []byte("not audio at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	failOut := filepath.Join(failDir, session.AudioFile)
	if err := convertAudio(corrupt, failOut, nil); err == nil {
		t.Fatal("a corrupt input must fail the conversion")
	}
	if _, statErr := os.Stat(failOut); !os.IsNotExist(statErr) {
		t.Fatalf("a failed conversion left %s behind (err=%v)", session.AudioFile, statErr)
	}
	if temps, _ := filepath.Glob(filepath.Join(failDir, ".audio-*.wav")); len(temps) != 0 {
		t.Fatalf("a failed conversion left temp files behind: %v", temps)
	}
}

// TestAtomicConvertHonoursUmask is the file-mode regression. os.CreateTemp
// reserves the temp at 0600, so the finished audio.wav must be widened back to
// the mode a direct ffmpeg write would have produced — which honours the
// operator's umask, exactly like the record-side audio.wav and every sibling
// artefact. A flat chmod 0644 (the pre-fix code) widened this one file past a
// restrictive umask, so a privacy-conscious operator's microphone recording came
// out group/world-readable. Under umask 077 the result must be 0600, not 0644.
func TestAtomicConvertHonoursUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	out := filepath.Join(dir, session.AudioFile)
	if err := atomicConvert(out, func(tmpPath string) error {
		return os.WriteFile(tmpPath, []byte("RIFF....complete"), 0o644)
	}, nil); err != nil {
		t.Fatalf("atomicConvert: %v", err)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat converted audio: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("under umask 077 the converted %s must honour the umask (0600), got %v — a flat 0644 widens the recording past the operator's umask", session.AudioFile, got)
	}
}

// TestConvertAudioRoutesThroughTemp pins the convertAudio → atomicConvert
// wiring at the call site, hermetically (the convertRunner stub stands in for
// ffmpeg; LookPath resolves against a fake on PATH). The producer must be
// handed a temp path beside out — never out itself — and its failure must leave
// out absent and no temp behind. The helper's own unit test stays green if a
// later edit routes ffmpeg straight at out again; this one does not.
func TestConvertAudioRoutesThroughTemp(t *testing.T) {
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "ffmpeg"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	dir := t.TempDir()
	in := filepath.Join(dir, "voice.wav")
	if err := os.WriteFile(in, []byte("RIFF input"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, session.AudioFile)

	oldRunner := convertRunner
	t.Cleanup(func() { convertRunner = oldRunner })
	var producedAt string
	convertRunner = func(ffmpeg, in, tmpPath string) error {
		producedAt = tmpPath
		// Exactly what an interrupted or ENOSPC-hit ffmpeg does: a partial file,
		// then failure.
		if werr := os.WriteFile(tmpPath, []byte("RIFF....partial"), 0o644); werr != nil {
			t.Fatalf("seed partial: %v", werr)
		}
		return errors.New("ffmpeg: killed by signal")
	}

	if err := convertAudio(in, out, nil); err == nil {
		t.Fatal("convertAudio must surface the conversion failure")
	}
	if producedAt == "" {
		t.Fatal("convertRunner was never invoked")
	}
	if producedAt == out {
		t.Fatal("the producer was pointed straight at out; a partial conversion would land as audio.wav")
	}
	if filepath.Dir(producedAt) != dir {
		t.Fatalf("temp %q is not beside out (rename would cross filesystems)", producedAt)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("a failed conversion left %s behind (err=%v)", session.AudioFile, statErr)
	}
	if temps, _ := filepath.Glob(filepath.Join(dir, ".audio-*.wav")); len(temps) != 0 {
		t.Fatalf("a failed conversion left temp files behind: %v", temps)
	}
}

// TestAtomicConvertLeavesNoPartialOnFailure is the interrupted-conversion regression,
// hermetic (no ffmpeg): a producer that writes a partial temp and then fails — exactly
// what a Ctrl+C'd or ENOSPC-hit ffmpeg does — must leave out untouched and the temp
// removed, so a later bare `transcribe` never mistakes a truncated fragment for the
// whole recording. Pre-fix (ffmpeg wrote straight to out) the fragment survived at out.
func TestAtomicConvertLeavesNoPartialOnFailure(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, session.AudioFile)

	// Producer writes a partial WAV to the temp, then reports failure (interrupted).
	err := atomicConvert(out, func(tmpPath string) error {
		if werr := os.WriteFile(tmpPath, []byte("RIFF....partial fragment"), 0o644); werr != nil {
			t.Fatalf("seed partial temp: %v", werr)
		}
		return errors.New("ffmpeg: killed by signal")
	}, nil)
	if err == nil {
		t.Fatal("atomicConvert must surface the producer's error")
	}
	// out must not exist: the partial temp was never renamed into place.
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("a failed conversion left a partial %s behind (err=%v)", session.AudioFile, statErr)
	}
	// The temp must be cleaned up.
	if temps, _ := filepath.Glob(filepath.Join(dir, ".audio-*.wav")); len(temps) != 0 {
		t.Fatalf("a failed conversion left temp files behind: %v", temps)
	}

	// A succeeding producer renames its temp into place and leaves no temp.
	if err := atomicConvert(out, func(tmpPath string) error {
		return os.WriteFile(tmpPath, []byte("RIFF....complete"), 0o644)
	}, nil); err != nil {
		t.Fatalf("atomicConvert on success: %v", err)
	}
	if b, rerr := os.ReadFile(out); rerr != nil || string(b) != "RIFF....complete" {
		t.Fatalf("successful conversion did not land the full output: %q (err=%v)", b, rerr)
	}
	// The finished artefact carries an ordinary 0644, not os.CreateTemp's 0600 —
	// the conversion must not silently narrow the mode a direct write gave it.
	if fi, serr := os.Stat(out); serr != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("converted %s mode: got %v (err=%v), want 0644", session.AudioFile, fi.Mode().Perm(), serr)
	}
	if temps, _ := filepath.Glob(filepath.Join(dir, ".audio-*.wav")); len(temps) != 0 {
		t.Fatalf("successful conversion left temp files behind: %v", temps)
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
	err := convertAudio(in, out, nil)
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
	go func() { done <- convertAudio(in, out, nil) }()

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
	go func() { done <- convertAudio(in, filepath.Join(dir, session.AudioFile), nil) }()

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
	if err := convertAudio(link, filepath.Join(dir, session.AudioFile), nil); err != nil &&
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

// TestCheckSessionAudioRefusesSymlink is the read-side exfil regression. A session
// is an exchange unit, so a received one can ship audio.wav as a symlink to a file
// outside the session — a private recording. Pre-fix checkSessionAudio used os.Stat,
// which resolves the link to its regular target and returns nil, so the engine
// transcribes that out-of-session file into transcript.jsonl inside the re-shareable
// session directory. os.Lstat must refuse the symlink, matching the read-side stance
// session.OpenFileNoFollowRead takes everywhere else. A symlink to a genuinely absent
// target must also be reported as a symlink, not misreported as "no audio.wav" — the
// F9 half: the file exists, so re-recording is the wrong advice.
func TestCheckSessionAudioRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, session.AudioFile)

	// (a) symlink to a real regular file outside the session.
	outside := filepath.Join(t.TempDir(), "private.wav")
	if err := os.WriteFile(outside, []byte("RIFF private audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, wav); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := checkSessionAudio(wav, dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("a symlinked audio.wav must be refused as a symlink (pre-fix it was silently followed), got %v", err)
	}

	// (b) symlink to an absent target must still be named a symlink, not "no audio.wav".
	if err := os.Remove(wav); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "does-not-exist.wav"), wav); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := checkSessionAudio(wav, dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("a dangling symlink must be reported as a symlink, not as absence, got %v", err)
	}
}

// TestCheckSessionAudioDistinguishesUnreadableFromMissing pins the
// missing-vs-unreadable split: only a genuinely absent audio.wav earns the "run
// `testimony record` first" guidance. A different Lstat failure — here EACCES
// from an unsearchable directory — must surface as a read error, because
// sending the operator to re-record a session whose audio exists destroys the
// misdiagnosed session's value.
func TestCheckSessionAudioDistinguishesUnreadableFromMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}
	sealed := filepath.Join(t.TempDir(), "sealed")
	if err := os.Mkdir(sealed, 0o755); err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(sealed, session.AudioFile)
	if err := os.WriteFile(wav, []byte("RIFF real audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sealed, 0o755) })

	err := checkSessionAudio(wav, sealed)
	if err == nil {
		t.Fatal("an unreadable audio.wav must error")
	}
	if strings.Contains(err.Error(), "testimony record") {
		t.Fatalf("an unreadable audio.wav was misreported as absent (re-record advice): %v", err)
	}
	if !strings.Contains(err.Error(), "reading") {
		t.Fatalf("the error should name the read failure, got %v", err)
	}
}

// TestResolveModelRefusesFIFO is the model-path twin of the FIFO refusals on the
// package's other subprocess-input sites (TestConvertAudioRefusesFIFOInput,
// TestConvertAudioRefusesFIFOOutput, TestCheckSessionAudioRefusesFIFO). The
// resolved -model path is handed to whisper-cli's -m and opened by it, so a FIFO
// there would block that open(2) for ever. resolveModel must fall through to the
// candidate search / not-found error rather than returning the FIFO. Pre-fix the
// `!fi.IsDir()` test accepted it, since a FIFO is not a directory.
func TestResolveModelRefusesFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "ggml-fifo.bin")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	got, err := resolveModel(fifo)
	if err == nil {
		t.Fatalf("resolveModel returned %q for a FIFO path; want a not-found error", got)
	}
	if got == fifo {
		t.Fatalf("resolveModel returned the FIFO path itself: %q", got)
	}
}

// TestResolveModelAcceptsRegularFile guards the ordinary case the refusal must
// not break: an existing regular ggml file (or a symlink to one, which os.Stat
// resolves) is returned as-is.
func TestResolveModelAcceptsRegularFile(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "ggml-test.bin")
	if err := os.WriteFile(model, []byte("ggml"), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	got, err := resolveModel(model)
	if err != nil {
		t.Fatalf("resolveModel(regular file): %v", err)
	}
	if got != model {
		t.Fatalf("want %q, got %q", model, got)
	}
}

// TestWriteOffsetSidecarFailurePreservesPrior pins the atomic sidecar write.
// The previous truncating write (O_TRUNC, then write) destroyed the prior
// sidecar's bytes the moment the open succeeded, so a write that failed after
// that point left a truncated sidecar with the rollback skipped — and, in this
// read-only-directory arrangement, no failure at all: the existing file's own
// permissions let the truncating open through, silently replacing the one
// durable record of the offset. With temp + rename the write must instead
// fail without touching the prior bytes.
func TestWriteOffsetSidecarFailurePreservesPrior(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory write permissions do not bind root")
	}
	dir := t.TempDir()
	sidecar := filepath.Join(dir, session.AudioOffsetFile)
	const prior = `{"offset_seconds":12.5,"provenance":"derived: audio creation_time - manifest t0"}` + "\n"
	if err := os.WriteFile(sidecar, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	if err := writeOffsetSidecar(dir, 99, "explicit -offset"); err == nil {
		t.Fatal("writeOffsetSidecar succeeded in a directory it cannot create the temp file in")
	}
	got, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read sidecar back: %v", err)
	}
	if string(got) != prior {
		t.Fatalf("prior sidecar bytes not preserved:\ngot  %q\nwant %q", got, prior)
	}
}

// TestRunDefaultsNilLog pins the log-sink default: record.Run defaults its
// Log to os.Stderr, but transcribe.Run wrote its offset status line straight
// to opts.Log, so a caller leaving Log nil panicked at the first Fprintf.
func TestRunDefaultsNilLog(t *testing.T) {
	fakeTools(t)
	dir, _ := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})

	if _, err := Run(Options{SessionDir: dir, Engine: EngineWhisperX}); err != nil {
		t.Fatalf("Run with nil Log: %v", err)
	}
}

// TestRunRefusesZeroUtteranceOverwrite is the truncating-write regression: a
// re-run whose engine returns no usable segments (wrong -language, a model
// that yields only whitespace, a genuinely silent take) must not destroy a
// transcript.jsonl a prior run already produced. Pre-fix, WriteJSONL's
// O_TRUNC open erased the file and Run reported "transcribed 0 utterances" at
// exit 0.
func TestRunRefusesZeroUtteranceOverwrite(t *testing.T) {
	bin := t.TempDir()
	whisperx := "#!/bin/sh\nfor last; do :; done\n" +
		`printf '%s' '{"segments":[]}' > "$last/audio.json"` + "\n"
	for name, script := range map[string]string{
		"whisperx": whisperx,
		"ffmpeg":   "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	dir, _ := seedSession(t, session.Manifest{Session: "s", T0EpochMS: 1_700_000_000_000})
	transcript := filepath.Join(dir, session.TranscriptFile)
	const prior = `{"id":"utt-001","t0":0,"t1":1,"speaker":"P1","text":"Alice speaks."}` + "\n"
	if err := os.WriteFile(transcript, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := Run(Options{SessionDir: dir, Engine: EngineWhisperX, Log: io.Discard})
	if err == nil {
		t.Fatalf("want a refusal for zero utterances, got n=%d, err=nil", n)
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("want a refusing-to-overwrite error, got %v", err)
	}
	got, readErr := os.ReadFile(transcript)
	if readErr != nil {
		t.Fatalf("read transcript: %v", readErr)
	}
	if string(got) != prior {
		t.Fatalf("the prior transcript was overwritten: got %q, want %q", got, prior)
	}
}

// TestTailCutsOnRuneBoundary pins the rune-aligned truncation of the engine
// diagnostic tail (see the identical property in record's tails): a byte-offset
// cut could open the tail mid-rune and render replacement garbage.
func TestTailCutsOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 450) + "a"
	if got := tail([]byte(long)); !utf8.ValidString(got) {
		t.Fatalf("tail split a rune: %q...", got[:12])
	}
}
