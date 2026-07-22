package record

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/REPPL/Testimony/internal/demo"
	"github.com/REPPL/Testimony/internal/session"
)

// --- flags ---

func TestStringSliceRepeatable(t *testing.T) {
	var s StringSlice
	for _, v := range []string{"Find the save button", "Change the theme"} {
		if err := s.Set(v); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	if len(s) != 2 || s[0] != "Find the save button" || s[1] != "Change the theme" {
		t.Fatalf("repeatable -task not accumulated: %+v", s)
	}
}

func TestResolveVideo(t *testing.T) {
	cases := []struct {
		video, noVideo, want bool
	}{
		{false, false, false}, // default: audio-only
		{true, false, true},   // -video opts in
		{false, true, false},  // -no-video explicit off
		{true, true, false},   // -no-video wins when both given
	}
	for _, c := range cases {
		if got := ResolveVideo(c.video, c.noVideo); got != c.want {
			t.Errorf("ResolveVideo(%v,%v)=%v, want %v", c.video, c.noVideo, got, c.want)
		}
	}
}

// --- argv builders ---

func TestMicArgs(t *testing.T) {
	got := micArgs("sessions/s1/audio.wav")
	want := []string{
		"-f", "avfoundation",
		"-i", ":default",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		"-y", "sessions/s1/audio.wav",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("micArgs mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestScreenArgs(t *testing.T) {
	got := screenArgs(1, "sessions/s1/screen.mp4")
	want := []string{
		"-f", "avfoundation",
		"-framerate", "30",
		"-capture_cursor", "1",
		"-i", "1",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-pix_fmt", "yuv420p",
		"-y", "sessions/s1/screen.mp4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("screenArgs mismatch:\n got %q\nwant %q", got, want)
	}
}

// --- device parsing/selection ---

const sampleDevices = `[AVFoundation indev @ 0x12a604710] AVFoundation video devices:
[AVFoundation indev @ 0x12a604710] [0] Studio Display Camera
[AVFoundation indev @ 0x12a604710] [1] Capture screen 0
[AVFoundation indev @ 0x12a604710] AVFoundation audio devices:
[AVFoundation indev @ 0x12a604710] [0] Studio Display Microphone
[AVFoundation indev @ 0x12a604710] [1] USB audio CODEC`

func TestParseAVDevices(t *testing.T) {
	video, audio := parseAVDevices(sampleDevices)
	if len(video) != 2 || video[1].index != 1 || video[1].name != "Capture screen 0" {
		t.Fatalf("video devices parsed wrong: %+v", video)
	}
	if len(audio) != 2 || audio[0].index != 0 || audio[0].name != "Studio Display Microphone" {
		t.Fatalf("audio devices parsed wrong: %+v", audio)
	}
}

func TestSelectDevices(t *testing.T) {
	video, audio := parseAVDevices(sampleDevices)

	screen, mics, err := selectDevices(video, audio, true)
	if err != nil {
		t.Fatalf("selectDevices: %v", err)
	}
	if screen != 1 {
		t.Fatalf("screen index: got %d, want 1 (Capture screen)", screen)
	}
	// The audio-input roster is returned for logging, in listing order. The mic
	// itself is captured via avfoundation :default, not by this list.
	wantMics := []string{"Studio Display Microphone", "USB audio CODEC"}
	if !reflect.DeepEqual(mics, wantMics) {
		t.Fatalf("audio roster: got %q, want %q", mics, wantMics)
	}

	// Audio-only: screen index is not resolved and no error.
	if _, _, err := selectDevices(video, audio, false); err != nil {
		t.Fatalf("audio-only selection must not require a screen device: %v", err)
	}

	// No screen device but screen requested → actionable error.
	if _, _, err := selectDevices([]avDevice{{0, "Studio Display Camera"}}, audio, true); err == nil {
		t.Fatal("missing Capture screen device must error when screen requested")
	}

	// No audio device at all → error.
	if _, _, err := selectDevices(video, nil, false); err == nil {
		t.Fatal("no audio device must error")
	}
}

// TestOutputTail covers the ffmpeg-diagnostic surfacing: an empty listing paired
// with real ffmpeg output must yield a bounded, trimmed tail so probeDevices can
// tell the operator the true cause (e.g. an avfoundation-less build) instead of
// misdirecting them to their microphone.
func TestOutputTail(t *testing.T) {
	if got := outputTail(nil); got != "" {
		t.Fatalf("empty input must yield empty tail, got %q", got)
	}
	if got := outputTail([]byte("  \n ")); got != "" {
		t.Fatalf("whitespace-only input must trim to empty, got %q", got)
	}
	short := []byte("Unknown input format: 'avfoundation'")
	if got := outputTail(short); got != string(short) {
		t.Fatalf("short output must pass through trimmed: got %q", got)
	}
	long := make([]byte, 0, 1000)
	for i := 0; i < 1000; i++ {
		long = append(long, 'x')
	}
	got := outputTail(long)
	if len(got) > 420 || !strings.HasPrefix(got, "...") {
		t.Fatalf("long output must be bounded and elided, got len %d prefix %.3q", len(got), got)
	}
}

// --- platform plan ---

func TestPlan(t *testing.T) {
	if rec, skips := plan("darwin", false); !reflect.DeepEqual(rec, []string{"microphone"}) || len(skips) != 0 {
		t.Fatalf("darwin audio-only: got %v skips %v", rec, skips)
	}
	if rec, skips := plan("darwin", true); !reflect.DeepEqual(rec, []string{"microphone", "screen"}) || len(skips) != 0 {
		t.Fatalf("darwin video: got %v skips %v", rec, skips)
	}
	rec, skips := plan("linux", true)
	if len(rec) != 0 {
		t.Fatalf("linux must record nothing, got %v", rec)
	}
	if len(skips) == 0 {
		t.Fatal("linux must explain what was skipped")
	}
	joined := strings.Join(skips, "\n")
	if !strings.Contains(joined, "transcribe") || !strings.Contains(joined, "-audio") {
		t.Fatalf("linux skip must point at external-audio transcribe: %q", joined)
	}
}

// --- TCC classifier ---

func TestClassifyRecorderExit(t *testing.T) {
	// Start-up exit with an avfoundation signature → permissions guidance naming
	// the Microphone pane.
	micStderr := "[AVFoundation indev @ 0x0] Failed to open device.\nInput/output error"
	msg := classifyRecorderExit(streamMicrophone, errors.New("exit status 1"), micStderr, true)
	if !strings.Contains(msg, "Microphone") || strings.Contains(msg, "Screen Recording") {
		t.Fatalf("mic failure must name the Microphone pane: %q", msg)
	}
	if !strings.Contains(msg, "permissions") {
		t.Fatalf("message must phrase the likely cause as permissions: %q", msg)
	}
	if !strings.Contains(msg, "Input/output error") {
		t.Fatalf("message must append the raw ffmpeg tail: %q", msg)
	}
	if strings.Contains(msg, "goroutine") || strings.Contains(msg, ".go:") {
		t.Fatalf("message must not be a stack trace: %q", msg)
	}

	scr := classifyRecorderExit(streamScreen, nil, "avfoundation: not authorized", true)
	if !strings.Contains(scr, "Screen Recording") || strings.Contains(scr, "→ Privacy & Security → Microphone") {
		t.Fatalf("screen failure must name the Screen Recording pane: %q", scr)
	}

	// Start-up exit WITHOUT an avfoundation signature → must not claim
	// permissions or name a pane; the ffmpeg tail carries the real cause.
	benign := classifyRecorderExit(streamMicrophone, errors.New("exit status 1"), "Conversion failed: invalid output format", true)
	if strings.Contains(benign, "permissions") || strings.Contains(benign, "Privacy & Security") {
		t.Fatalf("a start-up exit without an AV signature must not claim permissions: %q", benign)
	}
	if !strings.Contains(benign, "Conversion failed") {
		t.Fatalf("message must still append the raw ffmpeg tail: %q", benign)
	}

	// Mid-session exit (past the startup window) even WITH an AV signature
	// ("Input/output error" from a device unplug) must be reported as an
	// unexpected stop, never as a start-up permissions denial.
	midSession := classifyRecorderExit(streamMicrophone, errors.New("exit status 1"), "Input/output error", false)
	if strings.Contains(midSession, "permissions") || strings.Contains(midSession, "Privacy & Security") {
		t.Fatalf("a mid-session drop must not be mislabelled as a permissions denial: %q", midSession)
	}
	if strings.Contains(midSession, "failed to start") {
		t.Fatalf("a mid-session drop must not be labelled a start failure: %q", midSession)
	}
	if !strings.Contains(midSession, "Input/output error") {
		t.Fatalf("mid-session message must still append the raw ffmpeg tail: %q", midSession)
	}

	if !looksLikeAVFailure("Failed to open the device") {
		t.Fatal("avfoundation signature should be detected")
	}
	if looksLikeAVFailure("all good here") {
		t.Fatal("benign output must not look like a failure")
	}
}

func TestClassifyMissingOutput(t *testing.T) {
	// A microphone recorder stopped at finalise with no audio.wav must name the
	// missing artefact, point at the Microphone permission pane, phrase the cause
	// as most likely, and append the raw ffmpeg tail — never a stack trace.
	msg := classifyMissingOutput(streamMicrophone, session.AudioFile, "[AVFoundation indev @ 0x0] Failed to open device")
	if !strings.Contains(msg, session.AudioFile) {
		t.Fatalf("message must name the missing artefact: %q", msg)
	}
	if !strings.Contains(msg, "Microphone") || strings.Contains(msg, "Screen Recording") {
		t.Fatalf("microphone artefact must name the Microphone pane: %q", msg)
	}
	if !strings.Contains(msg, "Privacy & Security") || !strings.Contains(msg, "most likely") {
		t.Fatalf("message must name the settings pane and hedge the cause: %q", msg)
	}
	if !strings.Contains(msg, "Failed to open device") {
		t.Fatalf("message must append the ffmpeg tail: %q", msg)
	}
	if strings.Contains(msg, "goroutine") || strings.Contains(msg, ".go:") {
		t.Fatalf("message must not be a stack trace: %q", msg)
	}

	// A screen recorder that left no screen.mp4 must name the Screen Recording
	// pane instead, and tolerate an empty tail.
	scr := classifyMissingOutput(streamScreen, session.ScreenFile, "")
	if !strings.Contains(scr, "Screen Recording") || strings.Contains(scr, "→ Privacy & Security → Microphone") {
		t.Fatalf("screen artefact must name the Screen Recording pane: %q", scr)
	}
	if strings.Contains(scr, "ffmpeg output:") {
		t.Fatalf("an empty tail must not print an empty ffmpeg section: %q", scr)
	}
}

// TestFinaliseOutputs proves the per-recorder artefact validation: a present,
// non-empty audio.wav is accepted (audioReady, no problem); a missing or empty
// file yields a problem and withholds audioReady.
func TestFinaliseOutputs(t *testing.T) {
	dir := t.TempDir()

	// Microphone wrote a real audio.wav; screen wrote nothing.
	if err := os.WriteFile(filepath.Join(dir, session.AudioFile), []byte("RIFF...."), 0o644); err != nil {
		t.Fatal(err)
	}
	mic := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGINT), &lockedBuffer{})
	scr := newLiveChild(streamScreen, newFakeProc(syscall.SIGINT), &lockedBuffer{})

	audioReady, problems := finaliseOutputs(dir, []*liveChild{mic, scr})
	if !audioReady {
		t.Fatalf("a present non-empty audio.wav must be accepted")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], session.ScreenFile) {
		t.Fatalf("the missing screen.mp4 must be the only problem reported: %v", problems)
	}

	// An empty audio.wav must be treated as no output.
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, session.AudioFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mic2 := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGINT), &lockedBuffer{})
	ready, probs := finaliseOutputs(empty, []*liveChild{mic2})
	if ready || len(probs) != 1 {
		t.Fatalf("an empty audio.wav must count as no output: ready=%v problems=%v", ready, probs)
	}

	// Reap the still-live fakes so no goroutine lingers.
	for _, c := range []*liveChild{mic, scr, mic2} {
		stopChild(c, time.Second)
	}
}

// TestRunInstallsSignalHandlerBeforeSpawning proves the interrupt handler is
// installed before any recorder subprocess is spawned. Each recorder runs in
// its own process group, so the terminal's Ctrl+C never reaches ffmpeg — only
// record's stopAll does. If the handler were installed after the spawn (the
// original defect), a Ctrl+C in the startup window would kill record under
// Go's default handler and orphan the already-started ffmpeg children.
// Hermetic: no real signal handler, no ffmpeg — the notify and spawn seams are
// stubbed and their order recorded.
func TestRunInstallsSignalHandlerBeforeSpawning(t *testing.T) {
	var mu sync.Mutex
	var order []string
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	origNotify, origStart := notifyContext, startRecordersFn
	t.Cleanup(func() { notifyContext = origNotify; startRecordersFn = origStart })

	var cancel context.CancelFunc
	notifyContext = func() (context.Context, context.CancelFunc) {
		note("notify")
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		return ctx, c
	}
	startRecordersFn = func(dir string, streams []string, _ io.Writer) ([]*liveChild, error) {
		note("spawn")
		// A well-behaved recorder that finalises a real audio.wav and is reaped on
		// SIGINT, so the lifecycle stops cleanly. cancel is non-nil only if notify
		// already ran — which is exactly the ordering under test — so unblock the
		// wait here.
		if err := os.WriteFile(filepath.Join(dir, session.AudioFile), []byte("RIFF...."), 0o644); err != nil {
			t.Fatal(err)
		}
		child := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGINT), &lockedBuffer{})
		cancel()
		return []*liveChild{child}, nil
	}

	if err := Run(Options{Out: t.TempDir(), GOOS: "darwin", Log: io.Discard}); err != nil {
		t.Fatalf("Run must stop cleanly on interrupt: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"notify", "spawn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signal handler must be installed before recorders spawn: got %v, want %v", got, want)
	}
}

// TestRunClassifiesStartupExitDespiteSlowStop proves that a recorder which dies
// inside the start-up window is still diagnosed as a permissions denial even
// when the stop path that follows outlasts that window. The pre-fix code
// computed the classification AFTER stopAll had run, so the elapsed time it
// measured included the shutdown: stopAll blocks up to stopGrace per remaining
// child, which alone equals startupWindow, and a genuine TCC denial that failed
// in the first second was therefore reported as an unexpected mid-session stop.
// The operator was sent hunting a device fault instead of granting the
// microphone permission. Hermetic: no ffmpeg, no TTY, no real signal; the
// timings are shrunk so the slow stop costs milliseconds.
func TestRunClassifiesStartupExitDespiteSlowStop(t *testing.T) {
	origNotify, origStart := notifyContext, startRecordersFn
	origGrace, origWindow := stopGrace, startupWindow
	t.Cleanup(func() {
		notifyContext, startRecordersFn = origNotify, origStart
		stopGrace, startupWindow = origGrace, origWindow
	})

	// The stop path must outlast the start-up window, which is the whole point:
	// the stubborn screen recorder below ignores SIGINT and so burns the full
	// grace before it is escalated.
	startupWindow = 20 * time.Millisecond
	stopGrace = 200 * time.Millisecond

	// Never cancelled: Run must reach the recorder-exit branch, not the Ctrl+C one.
	notifyContext = func() (context.Context, context.CancelFunc) {
		return context.WithCancel(context.Background())
	}

	var stubborn *fakeProc
	startRecordersFn = func(dir string, streams []string, _ io.Writer) ([]*liveChild, error) {
		buf := &lockedBuffer{}
		_, _ = buf.Write([]byte("[AVFoundation indev @ 0x0] Failed to open device\nInput/output error"))
		// The microphone recorder is denied by TCC and dies at once — well inside
		// the start-up window.
		mic := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGINT), buf)
		_ = mic.p.Signal(syscall.SIGINT)

		// A second recorder that ignores SIGINT, so stopAll blocks the full grace
		// before escalating to SIGKILL — the delay that used to be charged against
		// the microphone's lifetime.
		stubborn = newFakeProc(syscall.SIGKILL)
		return []*liveChild{mic, newLiveChild(streamScreen, stubborn, &lockedBuffer{})}, nil
	}

	err := Run(Options{Out: t.TempDir(), Participant: "Alice", GOOS: "darwin", Log: io.Discard})
	if err == nil {
		t.Fatal("a recorder exiting on its own must make Run exit non-zero")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Microphone") || !strings.Contains(msg, "permissions") {
		t.Fatalf("an exit inside the start-up window must be diagnosed as a permissions denial, not a mid-session stop: %q", msg)
	}
	if !strings.Contains(msg, "Privacy & Security") {
		t.Fatalf("the diagnosis must point at the settings pane: %q", msg)
	}

	// The stop path really did outlast the start-up window: the stubborn child was
	// escalated, which only happens once the grace expired.
	if got := stubborn.sent(); len(got) != 2 || got[1] != syscall.SIGKILL {
		t.Fatalf("the stubborn recorder must have been escalated to SIGKILL, making the stop outlast the window: %v", got)
	}
}

// TestCheckPlainOutputRefusesSymlink proves the recorder output guard. ffmpeg is
// handed audio.wav/screen.mp4 as a path string with -y, so unlike every other
// write in this codebase it cannot go through session.OpenFileNoFollow and will
// happily follow a symlink at the final component. Pre-fix there was no guard at
// all: a symlink pre-planted at sessions/<ts>/audio.wav redirected the entire
// recording out of the session directory, overwriting a file the operator never
// named. An absent path — the ordinary case — must still be allowed.
func TestCheckPlainOutputRefusesSymlink(t *testing.T) {
	dir := t.TempDir()

	// The ordinary case: nothing there yet, ffmpeg creates it.
	if err := checkPlainOutput(filepath.Join(dir, session.AudioFile)); err != nil {
		t.Fatalf("an absent output path must be allowed: %v", err)
	}

	// Re-recording over a previous plain audio.wav stays allowed.
	plain := filepath.Join(dir, session.ScreenFile)
	if err := os.WriteFile(plain, []byte("previous take"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkPlainOutput(plain); err != nil {
		t.Fatalf("an existing regular file must be allowed: %v", err)
	}

	// A symlink planted at the output path must be refused, and refused by name,
	// even though its target does not exist — os.Lstat must not resolve it.
	outside := filepath.Join(t.TempDir(), "elsewhere.wav")
	planted := filepath.Join(dir, "planted.wav")
	if err := os.Symlink(outside, planted); err != nil {
		t.Fatal(err)
	}
	err := checkPlainOutput(planted)
	if err == nil {
		t.Fatal("a symlink at the recorder output path must be refused, not followed")
	}
	if !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), planted) {
		t.Fatalf("the refusal must name the path and the reason: %v", err)
	}
	if _, statErr := os.Lstat(outside); statErr == nil {
		t.Fatal("the guard must not have created the symlink target outside the session")
	}

	// A directory (or any other non-regular file) at the output path is refused too.
	sub := filepath.Join(dir, "subdir.wav")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkPlainOutput(sub); err == nil {
		t.Fatal("a non-regular file at the recorder output path must be refused")
	}
}

// TestStartRecordersRefusesSymlinkedOutput proves the guard is wired into the
// spawn path itself: startRecorders must refuse before any ffmpeg subprocess is
// started when a symlink sits at the audio output path. Skipped where ffmpeg is
// absent, since startRecorders resolves the binary first.
func TestStartRecordersRefusesSymlinkedOutput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("device probing is darwin-only")
	}

	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.wav"), filepath.Join(dir, session.AudioFile)); err != nil {
		t.Fatal(err)
	}

	children, err := startRecorders(dir, []string{streamMicrophone}, io.Discard)
	if err == nil {
		stopAll(children)
		t.Fatal("startRecorders must refuse a symlinked audio output rather than spawn ffmpeg on it")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("the refusal must name the reason: %v", err)
	}
}

// --- next commands ---

func TestNextCommands(t *testing.T) {
	dir := "sessions/2026-07-17_153045"

	withAudio := nextCommands(dir, true)
	if !strings.Contains(withAudio, dir) {
		t.Fatalf("next commands must carry the real dir: %q", withAudio)
	}
	if strings.Contains(withAudio, "-audio") {
		t.Fatalf("with in-place audio there must be no -audio flag: %q", withAudio)
	}
	for _, verb := range []string{"transcribe", "merge", "report"} {
		if !strings.Contains(withAudio, verb) {
			t.Fatalf("missing %s command: %q", verb, withAudio)
		}
	}

	// Without audio.wav, transcribe cannot reuse a session recording, so the
	// bare `transcribe -session DIR` command must NOT be offered as a next step.
	// The merge/report commands still stand (interactions may be captured), plus
	// a line explaining transcribe needs audio and pointing at the two ways to
	// get it: re-run record after granting the permission, or bring an external
	// recording via `transcribe -audio FILE`.
	noAudio := nextCommands(dir, false)
	if strings.Contains(noAudio, "  testimony transcribe -session "+dir+"\n") {
		t.Fatalf("without audio.wav the bare transcribe reuse line must not be offered: %q", noAudio)
	}
	for _, verb := range []string{"merge", "report"} {
		if !strings.Contains(noAudio, "testimony "+verb) || !strings.Contains(noAudio, "-session "+dir) {
			t.Fatalf("without audio the %s command must still be offered: %q", verb, noAudio)
		}
	}
	if !strings.Contains(noAudio, "-audio") || !strings.Contains(noAudio, "transcribe") {
		t.Fatalf("without audio the operator needs guidance mentioning transcribe -audio: %q", noAudio)
	}
	if !strings.Contains(strings.ToLower(noAudio), "re-run record") {
		t.Fatalf("without audio the guidance must suggest re-running record: %q", noAudio)
	}
}

// TestRunReportsRecorderStoppedWithNoOutput proves the finalise-time validation:
// a recorder that blocked on the TCC permission prompt for the whole session
// (so it wrote no audio.wav) and was reaped only when we SIGINT'd it at stop
// must NOT be silently exited — Run must print an actionable explanation naming
// the missing artefact, the likely permission cause, and the recorder's stderr
// tail, adjust the Next block to drop the transcribe reuse line, and exit
// non-zero. This is exactly the case the mid-session classifier misses: the
// recorder never exited on its own, so anyExit never fired. Hermetic: no
// ffmpeg, no TTY, no real signal — the notify and spawn seams are stubbed.
func TestRunReportsRecorderStoppedWithNoOutput(t *testing.T) {
	origNotify, origStart := notifyContext, startRecordersFn
	t.Cleanup(func() { notifyContext = origNotify; startRecordersFn = origStart })

	var cancel context.CancelFunc
	notifyContext = func() (context.Context, context.CancelFunc) {
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		return ctx, c
	}
	startRecordersFn = func(dir string, streams []string, _ io.Writer) ([]*liveChild, error) {
		buf := &lockedBuffer{}
		_, _ = buf.Write([]byte("[AVFoundation indev @ 0x0] Failed to open device\nInput/output error"))
		// A recorder that blocked on the TCC prompt all session: it never wrote
		// audio.wav and exits only when SIGINT'd at stop.
		child := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGINT), buf)
		cancel() // drive the stop path
		return []*liveChild{child}, nil
	}

	var log bytes.Buffer
	root := t.TempDir()
	err := Run(Options{Out: root, GOOS: "darwin", Log: &log})
	if err == nil {
		t.Fatal("a recorder that produced no output must make Run exit non-zero")
	}

	out := log.String()
	if !strings.Contains(out, "audio.wav") {
		t.Fatalf("explanation must name the missing artefact audio.wav: %q", out)
	}
	if !strings.Contains(out, "Microphone") || !strings.Contains(out, "Privacy & Security") {
		t.Fatalf("explanation must point at the Microphone permission pane: %q", out)
	}
	if !strings.Contains(out, "Failed to open device") {
		t.Fatalf("explanation must include the recorder's stderr tail: %q", out)
	}

	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("expected one session dir, got %d", len(entries))
	}
	dir := filepath.Join(root, entries[0].Name())
	if strings.Contains(out, "  testimony transcribe -session "+dir+"\n") {
		t.Fatalf("with no audio.wav the transcribe reuse line must not be offered: %q", out)
	}
	for _, verb := range []string{"merge", "report"} {
		if !strings.Contains(out, "testimony "+verb) {
			t.Fatalf("the %s command must still be offered when interactions may be captured: %q", verb, out)
		}
	}
}

// TestRunStopsDemoServerThroughBoundedShutdown proves that BOTH of Run's exit
// paths — the Ctrl+C branch and the branch a recorder taken on its own drives —
// stop the demo capture server through demo.Shutdown, the helper that carries a
// deadline and a Close fallback. Pre-fix each site called
// srv.Shutdown(context.Background()) directly, so `testimony record -demo` never
// received the bound at all: an unbounded stop waits for as long as any
// connection stays open, and one stalled browser tab left Ctrl+C hanging instead
// of finalising the session.
//
// The deadline itself belongs to demo and is proven there; what regressed, and
// what this test pins, is that record routes both call sites through it rather
// than stopping the server itself — so the assertion is deliberately the narrower
// "the bounded helper was called, once, on each path". Hermetic: the demo serve
// and shutdown seams are stubbed, so no port is bound and no ffmpeg runs.
func TestRunStopsDemoServerThroughBoundedShutdown(t *testing.T) {
	// The production seam must be the bounded helper, not some unbounded stop the
	// stub below would happily stand in for.
	if reflect.ValueOf(shutdownDemoFn).Pointer() != reflect.ValueOf(demo.Shutdown).Pointer() {
		t.Fatal("record must stop the demo server through demo.Shutdown, the bounded helper")
	}

	// interrupted drives the Ctrl+C branch; recorderDied drives the branch taken
	// when a recorder exits on its own. Both must stop the server.
	cases := []struct {
		name       string
		interrupt  bool
		wantErrors bool
	}{
		{name: "interrupted", interrupt: true, wantErrors: false},
		{name: "recorder died", interrupt: false, wantErrors: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			origNotify, origStart := notifyContext, startRecordersFn
			origServe, origShutdown := serveDemoFn, shutdownDemoFn
			t.Cleanup(func() {
				notifyContext, startRecordersFn = origNotify, origStart
				serveDemoFn, shutdownDemoFn = origServe, origShutdown
			})

			// A server value that was never listened on: stopDemo only has to reach
			// the seam, and binding a real port would make the test racy.
			stub := &http.Server{}
			serveDemoFn = func(addr, dir string) (*http.Server, error) { return stub, nil }

			var mu sync.Mutex
			var stopped []*http.Server
			shutdownDemoFn = func(srv *http.Server) error {
				mu.Lock()
				stopped = append(stopped, srv)
				mu.Unlock()
				return nil
			}

			var cancel context.CancelFunc
			notifyContext = func() (context.Context, context.CancelFunc) {
				ctx, cf := context.WithCancel(context.Background())
				cancel = cf
				return ctx, cf
			}
			startRecordersFn = func(dir string, streams []string, _ io.Writer) ([]*liveChild, error) {
				child := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGINT), &lockedBuffer{})
				if c.interrupt {
					// A well-behaved recorder that finalised a real audio.wav, so the
					// Ctrl+C path completes without any finalise problem.
					if err := os.WriteFile(filepath.Join(dir, session.AudioFile), []byte("RIFF...."), 0o644); err != nil {
						t.Fatal(err)
					}
					cancel()
				} else {
					// The recorder exits on its own, sending Run down the other branch.
					_ = child.p.Signal(syscall.SIGINT)
				}
				return []*liveChild{child}, nil
			}

			err := Run(Options{
				Out:         t.TempDir(),
				Participant: "Bob",
				Demo:        true,
				Addr:        "127.0.0.1:8737",
				GOOS:        "darwin",
				Log:         io.Discard,
			})
			if c.wantErrors && err == nil {
				t.Fatal("a recorder exiting on its own must make Run exit non-zero")
			}
			if !c.wantErrors && err != nil {
				t.Fatalf("Run must stop cleanly on interrupt: %v", err)
			}

			mu.Lock()
			got := append([]*http.Server(nil), stopped...)
			mu.Unlock()
			if len(got) != 1 || got[0] != stub {
				t.Fatalf("the demo server must be stopped exactly once through the bounded helper, got %d call(s)", len(got))
			}
		})
	}
}

// --- honest degradation ---

// TestRunDegradesHonestly proves that on a platform without capture support and
// with no demo server, record still writes a valid session (dir + manifest),
// states what was skipped, prints the external-audio next step, and exits
// cleanly without blocking. Hermetic: no ffmpeg, no TTY, no signal.
func TestRunDegradesHonestly(t *testing.T) {
	var log bytes.Buffer
	root := t.TempDir()

	err := Run(Options{
		Out:   root,
		App:   "settings prototype",
		Tasks: []string{"Find the save button"},
		GOOS:  "linux",
		Log:   &log,
	})
	if err != nil {
		t.Fatalf("degraded Run must exit cleanly: %v", err)
	}

	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("expected one session dir, got %d", len(entries))
	}
	dir := filepath.Join(root, entries[0].Name())
	m, err := session.LoadManifest(dir)
	if err != nil {
		t.Fatalf("valid manifest must still be written: %v", err)
	}
	if m.T0EpochMS == 0 || m.App != "settings prototype" {
		t.Fatalf("manifest not populated: %+v", m)
	}

	out := log.String()
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("must state what was skipped: %q", out)
	}
	if !strings.Contains(out, "-audio") {
		t.Fatalf("must point at the external-audio transcribe step: %q", out)
	}
}

// --- lifecycle state machine over a fake proc ---

// fakeProc records the signals it receives and exits Wait only when it receives
// its designated exit signal.
type fakeProc struct {
	mu       sync.Mutex
	signals  []os.Signal
	exitOn   os.Signal
	exit     chan struct{}
	exitOnce sync.Once
}

func newFakeProc(exitOn os.Signal) *fakeProc {
	return &fakeProc{exitOn: exitOn, exit: make(chan struct{})}
}

func (f *fakeProc) Start() error { return nil }

func (f *fakeProc) Signal(s os.Signal) error {
	f.mu.Lock()
	f.signals = append(f.signals, s)
	f.mu.Unlock()
	if s == f.exitOn {
		f.exitOnce.Do(func() { close(f.exit) })
	}
	return nil
}

func (f *fakeProc) Wait() error {
	<-f.exit
	return nil
}

func (f *fakeProc) sent() []os.Signal {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]os.Signal, len(f.signals))
	copy(out, f.signals)
	return out
}

func TestStopChildCleanSIGINT(t *testing.T) {
	fp := newFakeProc(syscall.SIGINT)
	c := newLiveChild(streamMicrophone, fp, &lockedBuffer{})

	stopChild(c, time.Second)

	if got := fp.sent(); len(got) != 1 || got[0] != syscall.SIGINT {
		t.Fatalf("a well-behaved child must be reaped on SIGINT alone, got %v", got)
	}
}

func TestStopChildEscalatesToSIGKILL(t *testing.T) {
	// A child that ignores SIGINT must be escalated to SIGKILL after the grace.
	fp := newFakeProc(syscall.SIGKILL)
	c := newLiveChild(streamScreen, fp, &lockedBuffer{})

	start := time.Now()
	stopChild(c, 30*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("must wait the grace before escalating, only waited %v", elapsed)
	}

	got := fp.sent()
	if len(got) != 2 || got[0] != syscall.SIGINT || got[1] != syscall.SIGKILL {
		t.Fatalf("expected SIGINT then SIGKILL, got %v", got)
	}
}

// TestLockedBufferBoundsMemory is the OOM regression: a device-stall stderr flood
// over a long session used to grow lockedBuffer without bound, and an OOM parent
// orphans the ffmpeg children still recording. The buffer must retain only a bounded
// trailing window while still surfacing the most recent (diagnostic) bytes.
func TestLockedBufferBoundsMemory(t *testing.T) {
	var b lockedBuffer
	// Simulate a flood far larger than the retention window: 4 MiB in 4 KiB writes.
	chunk := bytes.Repeat([]byte("frame dropped\n"), 300) // ~4 KiB
	total := 0
	for i := 0; i < 1024; i++ {
		n, err := b.Write(chunk)
		if err != nil || n != len(chunk) {
			t.Fatalf("Write returned (%d,%v), want (%d,nil)", n, err, len(chunk))
		}
		total += len(chunk)
	}
	if total <= stderrRetain {
		t.Fatalf("test wrote %d bytes, not enough to exceed the %d retention cap", total, stderrRetain)
	}
	// Retained bytes are bounded regardless of how much was written.
	if len(b.buf) > stderrRetain {
		t.Fatalf("lockedBuffer retained %d bytes, exceeding the %d cap", len(b.buf), stderrRetain)
	}
	// The tail is still available and marks the elision, so diagnostics survive.
	tail := b.tail()
	if !strings.HasPrefix(tail, "…") {
		t.Fatalf("tail after a flood must mark the dropped prefix with an ellipsis, got %q…", tail[:min(20, len(tail))])
	}
	if !strings.Contains(tail, "frame dropped") {
		t.Fatalf("tail must retain the most recent stderr content, got %q", tail)
	}

	// A small total (under the cap) is retained verbatim with no ellipsis.
	var s lockedBuffer
	s.Write([]byte("short output"))
	if got := s.tail(); got != "short output" {
		t.Fatalf("under-cap tail must be verbatim, got %q", got)
	}
}

// TestFinaliseOutputsFlagsSIGKILLedRecorder is the truncated-artefact regression: a
// recorder force-stopped after missing the finalisation grace can leave a non-empty
// but unplayable file (an MP4 with no moov atom), which the size-only check used to
// bless as good. finaliseOutputs must flag a killed recorder even when its file has
// bytes; a killed PCM audio.wav (which survives a kill) is still offered.
func TestFinaliseOutputsFlagsSIGKILLedRecorder(t *testing.T) {
	dir := t.TempDir()
	// Both streams left a non-empty file, but both were SIGKILLed at stop.
	if err := os.WriteFile(filepath.Join(dir, session.AudioFile), []byte("RIFF....partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, session.ScreenFile), []byte("\x00\x00\x00\x18ftyp....truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	mic := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGKILL), &lockedBuffer{})
	scr := newLiveChild(streamScreen, newFakeProc(syscall.SIGKILL), &lockedBuffer{})
	// Force the SIGKILL escalation (short grace) so both are marked killed.
	stopChild(mic, 5*time.Millisecond)
	stopChild(scr, 5*time.Millisecond)
	if !mic.killed || !scr.killed {
		t.Fatalf("stopChild must mark a SIGKILLed recorder killed: mic=%v scr=%v", mic.killed, scr.killed)
	}

	audioReady, problems := finaliseOutputs(dir, []*liveChild{mic, scr})
	// Both killed recorders are flagged despite non-empty files.
	if len(problems) != 2 {
		t.Fatalf("both SIGKILLed recorders must be flagged, got %d problems: %v", len(problems), problems)
	}
	var sawAudio, sawScreen bool
	for _, p := range problems {
		if strings.Contains(p, session.AudioFile) && strings.Contains(p, "force-stopped") {
			sawAudio = true
		}
		if strings.Contains(p, session.ScreenFile) && strings.Contains(p, "truncated") {
			sawScreen = true
		}
	}
	if !sawAudio || !sawScreen {
		t.Fatalf("expected force-stop warnings for both streams, got %v", problems)
	}
	// A killed WAV that has bytes is still offered for transcription (PCM survives a kill).
	if !audioReady {
		t.Fatalf("a present (if kill-truncated) audio.wav should still be offered for transcription")
	}
}

func TestAnyExitReportsDeadChild(t *testing.T) {
	live := newLiveChild(streamMicrophone, newFakeProc(syscall.SIGINT), &lockedBuffer{})
	dead := newLiveChild(streamScreen, newFakeProc(syscall.SIGINT), &lockedBuffer{})

	// Kill the "dead" child by delivering its exit signal out of band.
	_ = dead.p.Signal(syscall.SIGINT)

	select {
	case c := <-anyExit([]*liveChild{live, dead}):
		if c.stream != streamScreen {
			t.Fatalf("anyExit reported %q, want the exited screen child", c.stream)
		}
	case <-time.After(time.Second):
		t.Fatal("anyExit did not report the exited child")
	}

	// Clean up the still-live child so no goroutine lingers.
	stopChild(live, time.Second)
}
