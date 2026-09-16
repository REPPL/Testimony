package coderefs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/REPPL/Testimony/internal/session"
)

// TestEmitCarriesAnchorQuoteWindowAndRepo is AC1: the request carries each
// eligible finding's anchor, its record (and so its quote), its event window,
// and the repository path — and no ineligible finding.
func TestEmitCarriesAnchorQuoteWindowAndRepo(t *testing.T) {
	dir := writeSession(t)
	req, err := EmitRequest(dir, repoPath, DefaultWindow)
	if err != nil {
		t.Fatalf("EmitRequest: %v", err)
	}
	abs, _ := filepath.Abs(repoPath)
	for _, want := range []string{
		"Testimony code-mapping rubric: testimony-coderefs/v1",
		"- Path: " + session.SafeInline(abs),
		"Finding F-001 — bug, severity 3, at [00:22], selector `[data-testid=save-btn]` on route `#general`, confirmed by human verdict on 2026-09-12:",
		`"quote":"I clicked save and nothing happened"`,
		"Finding F-007 — friction, severity 1, at [00:32], route `#appearance`, confirmed by human verdict on 2026-09-12:",
		`"id":"ev-003"`,
		`{"rubric":"testimony-coderefs/v1","refs":[`,
	} {
		if !strings.Contains(req, want) {
			t.Errorf("request lacks %q", want)
		}
	}
	for _, absent := range []string{"Finding F-002", "Finding F-003", "Finding F-004", "Finding F-005", "Finding F-006"} {
		if strings.Contains(req, absent) {
			t.Errorf("request carries ineligible %q", absent)
		}
	}
}

// Emit checks the repository as ingest does, so a request can never tell the
// host to resolve anchors against the current directory or a file.
func TestEmitRefusesBadRepo(t *testing.T) {
	dir := writeSession(t)
	if _, err := EmitRequest(dir, "", 10); err == nil {
		t.Fatal("an empty repository path was accepted")
	}
	if _, err := EmitRequest(dir, filepath.Join(repoPath, "src/routes.ts"), 10); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("a file as repository: %v", err)
	}
}

func TestEmitIsDeterministic(t *testing.T) {
	dir := writeSession(t)
	a, err := EmitRequest(dir, repoPath, 10)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EmitRequest(dir, repoPath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("two emits differ")
	}
}

// TestEmitRefusesWithNoMappableFinding is AC5's emit half: the refusal names the
// tally with the anchored count and is the sentinel the CLI maps to exit 1.
func TestEmitRefusesWithNoMappableFinding(t *testing.T) {
	// F-006 confirmed but anchorless is the only confirmed finding left.
	fnd := `{"id":"F-006","t":57,"type":"idea","severity":2,"mode":"A","quote":"A toast would do","evidence":["utt-008"],"status":"unverified"}` + "\n" +
		`{"id":"F-002","t":38,"type":"preference","severity":2,"quote":"x","evidence":["utt-006"],"ui":{"route":"#a"},"status":"unverified"}` + "\n" +
		`{"kind":"verdict","finding":"F-006","verdict":"confirmed","at":"2026-09-12"}` + "\n"
	dir := writeSession(t, session.FindingsFile, fnd)
	_, err := EmitRequest(dir, repoPath, 10)
	if !errors.Is(err, ErrNoMappableFindings) {
		t.Fatalf("err = %v, want ErrNoMappableFindings", err)
	}
	if want := "2 findings: 1 confirmed (0 with a selector or route), 1 unverified, 0 duplicate, 0 rejected"; !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal %q lacks %q", err, want)
	}
}

// The refusal precedes the timeline read, so a session with nothing to map is
// not sent to run merge first.
func TestEmitRefusalPrecedesTheTimelineRead(t *testing.T) {
	fnd := `{"id":"F-001","t":22,"type":"bug","severity":3,"quote":"x","evidence":["utt-004"],"ui":{"route":"#g"},"status":"unverified"}` + "\n"
	dir := writeSession(t, session.FindingsFile, fnd, session.TimelineFile, "")
	if _, err := EmitRequest(dir, repoPath, 10); !errors.Is(err, ErrNoMappableFindings) {
		t.Fatalf("err = %v, want the eligibility refusal, not a missing-timeline error", err)
	}
}

func TestEmitHintsMissingArtefacts(t *testing.T) {
	dir := writeSession(t, session.TimelineFile, "")
	if _, err := EmitRequest(dir, repoPath, 10); err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("missing timeline: err = %v, want a merge hint", err)
	}
	dir = writeSession(t, session.FindingsFile, "")
	if _, err := EmitRequest(dir, repoPath, 10); err == nil || !strings.Contains(err.Error(), "analyze -ingest") {
		t.Fatalf("missing findings: err = %v, want an analyze -ingest hint", err)
	}
}

func TestEmitMutatesNothing(t *testing.T) {
	dir := writeSession(t)
	before := dirDigest(t, dir)
	if _, err := EmitRequest(dir, repoPath, 10); err != nil {
		t.Fatal(err)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatal("emit changed the session directory")
	}
	if _, err := os.Stat(filepath.Join(dir, session.RefsFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("emit wrote refs.jsonl")
	}
}

// TestEmitAnchorRendersAsCodeSpan: the selector is a literal string the host
// searches for, so the header must not backslash-escape its brackets.
func TestEmitAnchorRendersAsCodeSpan(t *testing.T) {
	dir := writeSession(t)
	req, err := EmitRequest(dir, repoPath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req, `\[data-testid`) {
		t.Fatal("the selector is backslash-escaped inside its code span")
	}
}

func TestEmitEscapesInlineMarkdownInManifestFields(t *testing.T) {
	man := `{"session":"fixture-session","app":"![x](http://h/b.png)","participant":"P1","t0_epoch_ms":1784300400000}`
	dir := writeSession(t, session.ManifestFile, man)
	req, err := EmitRequest(dir, repoPath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req, "![x](http://h/b.png)") {
		t.Fatal("an image beacon survived into the request")
	}
}

func dirDigest(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}
