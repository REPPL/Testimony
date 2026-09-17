package coderefs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/REPPL/Testimony/internal/analyze"
	"github.com/REPPL/Testimony/internal/session"
)

// Ingest validates the model's answer JSON from r against the reference schema
// and, only if every reference passes, writes refs.jsonl with status forced to
// "proposed". It is the sole validation boundary: a reference naming a finding
// that is not currently confirmed or carries no anchor, claiming another
// session, naming a path that does not exist as a regular file under repo or
// escapes it, giving a line past the file's end, carrying a role outside the
// set, or bearing a stray field is rejected here, transactionally (all errors
// reported, nothing written on any failure).
//
// It reads manifest.json, findings.jsonl, and the repository — the repository
// only for the existence and line-count checks. The repository is never written
// to.
//
// To protect the retained human record it refuses to overwrite a refs.jsonl
// that already holds decision records.
func Ingest(dir, repo string, r io.Reader) ([]Ref, error) {
	man, err := session.LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	findings, verdicts, err := loadFindings(dir)
	if err != nil {
		return nil, err
	}
	mappable := eligible(findings, verdicts)
	// Refused before a byte of the answer is read: with no eligible finding there
	// is nothing a reference could legally name, so every reference in the answer
	// would fail the same rule and the operator would read a wall of errors
	// instead of the one fact that explains them.
	if len(mappable) == 0 {
		return nil, noMappableFindings(dir, findings, verdicts)
	}
	// Keyed in SafeText form, the only form of the finding the answering agent is
	// ever shown.
	sources := make(map[string]analyze.Finding, len(mappable))
	for _, f := range mappable {
		sources[session.SafeText(f.ID)] = f
	}
	root, err := newRepoRoot(repo)
	if err != nil {
		return nil, err
	}

	// The answer is untrusted model output and -ingest reads it from stdin or a
	// file, so cap the read: a multi-gigabyte answer must not OOM the process
	// before validation runs.
	data, err := io.ReadAll(io.LimitReader(r, session.MaxAnswerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > session.MaxAnswerBytes {
		return nil, fmt.Errorf("answer exceeds %d bytes: refusing to ingest", session.MaxAnswerBytes)
	}
	raws, rubric, err := parseContainer(data)
	if err != nil {
		return nil, err
	}
	if rubric != "" && !knownRubrics[rubric] {
		return nil, fmt.Errorf("unknown rubric %q (expected %s)", rubric, RubricVersion)
	}
	// An empty refs array is a no-op, not a truncating write: the commit below
	// replaces the file whole, so proceeding would erase a prior good refs.jsonl
	// and report success. A model that found nothing for a finding omits it.
	if len(raws) == 0 {
		return nil, fmt.Errorf("answer contains no references; refusing to overwrite %s", session.RefsFile)
	}
	if len(raws) > maxRefs {
		return nil, fmt.Errorf("answer carries %d references, exceeding the limit of %d; the repository is read per reference, so the count is bounded before any is checked", len(raws), maxRefs)
	}

	var (
		decoded []positioned
		errs    []error
	)
	for i, raw := range raws {
		p, derr := decodeRef(raw)
		if derr != nil {
			errs = append(errs, fmt.Errorf("reference #%d: %v", i+1, derr))
			continue
		}
		p.at = i + 1
		decoded = append(decoded, p)
	}
	errs = append(errs, validate(decoded, sources, man.Session, root)...)

	// The model is never trusted: every reference lands proposed, and carries the
	// clean path the check actually ran on. Laundering both here, before the size
	// check below, is what makes that check measure the line actually written
	// rather than the one the answer proposed.
	refs := make([]Ref, len(decoded))
	for i, p := range decoded {
		refs[i] = p.ref
		refs[i].Status = "proposed"
		if p.clean != "" {
			refs[i].Path = p.clean
		}
	}
	errs = append(errs, oversizedRefs(refs, decoded)...)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if err := commitRefs(dir, refs); err != nil {
		return nil, err
	}
	return refs, nil
}

// repoRoot is the repository as the path checks see it: the absolute path the
// operator gave, and that path with every symlink resolved, so a reference that
// passes through a symlinked directory can be checked against the real tree.
type repoRoot struct {
	abs      string
	resolved string
}

// newRepoRoot resolves repo once for the whole answer. The CLI already refuses a
// -repo that is not a directory; the check is repeated here so the rule is a
// property of the API rather than of one caller's invariants.
func newRepoRoot(repo string) (repoRoot, error) {
	// An empty path would resolve to the current directory, which is never what
	// a caller meant by "the application's repository".
	if repo == "" {
		return repoRoot{}, errors.New("repository path must be non-empty")
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return repoRoot{}, fmt.Errorf("repository %s: %w", repo, err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return repoRoot{}, fmt.Errorf("repository: %w", err)
	}
	if !fi.IsDir() {
		return repoRoot{}, fmt.Errorf("repository %s is not a directory", repo)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return repoRoot{}, fmt.Errorf("repository %s: %w", repo, err)
	}
	return repoRoot{abs: abs, resolved: resolved}, nil
}

// checkPath holds a reference's path to the containment rule and returns the
// file's full path, and the clean repo-relative form that was checked, when it
// names an existing regular file under the repository. The clean form is what
// ingest records: the check runs on the SafeText-trimmed path, so storing the
// raw one would put a path in refs.jsonl (with stray whitespace or invisible
// characters) that no editor opens and that the check never saw.
//
// A model answer is untrusted input naming a filesystem path the CLI will open,
// so the rule is a containment check, not a string check. The lexical rules
// (forward slashes, no leading slash, no volume, no `.` or `..` segment, no
// empty segment) are what actually keep the join inside the repository; the
// join-and-Rel rule after them is belt and braces, not load-bearing. Resolving
// the parent directory's symlinks and checking it against the resolved
// repository catches a path that walks through a symlinked directory pointing
// outside, which no lexical rule can see; and Lstat rather than Stat means a
// symlink at the final component is refused as "not a regular file" rather than
// followed.
//
// Accepted residual: the parent directory is resolved here and re-resolved by
// the kernel when countLines or the review snippet opens the file, so a local
// attacker who can swap a directory inside the repository for a symlink between
// the two could point the open elsewhere. The final component is opened under
// the no-follow guard either way; closing the parent window portably needs
// openat-style traversal the standard library does not offer.
func (root repoRoot) checkPath(raw string) (full, clean string, err error) {
	if strings.ContainsRune(raw, 0) {
		return "", "", errors.New("path contains a NUL byte")
	}
	p := strings.TrimSpace(session.SafeText(raw))
	if p == "" {
		return "", "", errors.New("path must be non-empty")
	}
	if len(p) > maxPathBytes {
		return "", "", fmt.Errorf("path is %d bytes, exceeding the limit of %d", len(p), maxPathBytes)
	}
	if strings.Contains(p, `\`) {
		return "", "", errors.New("path must use forward slashes")
	}
	if strings.HasPrefix(p, "/") || hasDriveLetter(p) || filepath.VolumeName(p) != "" {
		return "", "", errors.New("path must be repo-relative, not absolute")
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "":
			return "", "", errors.New("path has an empty segment")
		case ".", "..":
			return "", "", errors.New("path must not contain a . or .. segment")
		}
	}
	full = filepath.Join(root.abs, filepath.FromSlash(p))
	if escapes(root.abs, full) {
		return "", "", errors.New("path resolves outside the repository")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", errors.New("path does not exist under the repository")
		}
		return "", "", fmt.Errorf("path: %v", err)
	}
	if escapes(root.resolved, parent) {
		return "", "", errors.New("path resolves outside the repository (through a symlinked directory)")
	}
	fi, err := os.Lstat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", errors.New("path does not exist under the repository")
		}
		return "", "", fmt.Errorf("path: %v", err)
	}
	if !fi.Mode().IsRegular() {
		return "", "", errors.New("path is not a regular file (a directory or a symlink is refused)")
	}
	return full, p, nil
}

// hasDriveLetter reports a Windows drive prefix (`C:`) on any platform, so an
// answer written on one is refused as absolute here too. Only an ASCII letter
// before the colon counts: `a:b.ts` is a legal POSIX file name.
func hasDriveLetter(p string) bool {
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	c := p[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// escapes reports whether target, made relative to root, lands outside it.
func escapes(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// countLines counts the lines of the regular file at full — `\n`-terminated
// lines plus an unterminated last line — reading at most session.MaxJSONLBytes,
// streamed through a fixed buffer so nothing near that size is held. A file
// larger than the bound is refused for a line check rather than counted: the
// bound is the one every session reader already scans to, and a reference into
// a file that size can name the file without a line.
func countLines(full string) (int, error) {
	f, err := session.OpenFileNoFollowRead(full)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, session.MaxJSONLBytes+1))
	var (
		n     int
		total int64
		last  byte = '\n'
		buf        = make([]byte, 64*1024)
	)
	for {
		k, rerr := r.Read(buf)
		if k > 0 {
			total += int64(k)
			n += bytes.Count(buf[:k], []byte{'\n'})
			last = buf[k-1]
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, rerr
		}
	}
	if total > session.MaxJSONLBytes {
		return 0, fmt.Errorf("file exceeds %d bytes, the bound for a line check; omit line to reference the file alone", session.MaxJSONLBytes)
	}
	if last != '\n' {
		n++
	}
	return n, nil
}

// lineCounter memoises countLines per file across one answer, so an answer
// naming the same file in many references reads it once rather than once per
// reference; the answer is untrusted, and re-reading a large file per line is
// the cheap amplification it would otherwise buy.
type lineCounter map[string]struct {
	n   int
	err error
}

func (c lineCounter) count(full string) (int, error) {
	if r, ok := c[full]; ok {
		return r.n, r.err
	}
	n, err := countLines(full)
	c[full] = struct {
		n   int
		err error
	}{n, err}
	return n, err
}

// commitRefs runs the decision guard and the whole-file replacement as one
// locked step, through session.CommitRecords — the same primitive
// analyze.commitFindings and drafttests.commitDrafts use, so a concurrent
// `testimony review -kind refs` appending a decision cannot slip between the
// probe and the rewrite and have its record destroyed.
func commitRefs(dir string, refs []Ref) error {
	path := filepath.Join(dir, session.RefsFile)
	records := make([][]byte, 0, len(refs))
	for _, r := range refs {
		b, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("write %s: %w", session.RefsFile, err)
		}
		records = append(records, b)
	}
	return session.CommitRecords(session.Commit{
		Path:    path,
		Records: records,
		Guard: func(current io.Reader) error {
			held, err := holdsDecisions(current, path)
			if err != nil {
				return err
			}
			if held {
				return fmt.Errorf("refusing to overwrite %s: it already holds decision records (the retained human record)", session.RefsFile)
			}
			return nil
		},
	})
}

// holdsDecisions reports whether the refs.jsonl open on r already contains any
// decision record. It scans for raw kind:"decision" lines rather than reusing
// Load, whose decision slice is filtered to the closed enum: a hand-edited file
// whose only decision lines carry a foreign value would otherwise slip past the
// guard and have its human-decision records truncated by a re-ingest.
func holdsDecisions(r io.Reader, path string) (bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), session.MaxJSONLLine)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		if probe.Kind == "decision" {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return false, nil
}

// oversizedRefs reports any reference whose refs.jsonl line would exceed
// session.MaxJSONLLine, and refuses an answer whose references would together
// exceed session.MaxJSONLBytes once written. maxPathBytes bounds the one free
// field, but nothing bounds the number of references in an answer, so a set of
// individually valid references can still serialise to a refs.jsonl that
// ParseRecords refuses to read back.
func oversizedRefs(refs []Ref, decoded []positioned) []error {
	var errs []error
	var total int64
	var counted int
	for i, r := range refs {
		label := refLabel(r, decoded[i].at)
		line, err := json.Marshal(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: cannot encode as JSON: %w", label, err))
			continue
		}
		lineLen := int64(len(line) + 1)
		if lineLen > session.MaxJSONLLine {
			errs = append(errs, fmt.Errorf("%s: encodes to %d bytes, exceeding the %d-byte %s line limit", label, lineLen, session.MaxJSONLLine, session.RefsFile))
			continue
		}
		total += lineLen
		counted++
	}
	if total > session.MaxJSONLBytes {
		errs = append(errs, fmt.Errorf("references encode to %d bytes across %d references, exceeding the %d-byte %s file limit; refusing to write a file review and render could not read back", total, counted, session.MaxJSONLBytes, session.RefsFile))
	}
	return errs
}

// parseContainer accepts either a top-level object with a "refs" array (the
// preferred container, optionally carrying a "rubric") or a bare array of
// references. It returns the raw elements and the rubric string (empty for the
// bare-array form).
func parseContainer(data []byte) ([]json.RawMessage, string, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, "", fmt.Errorf("empty answer: expected a JSON object or array of references")
	}
	switch trimmed[0] {
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, "", fmt.Errorf("parse references array: %w", err)
		}
		return arr, "", nil
	case '{':
		var doc struct {
			Rubric string            `json:"rubric"`
			Refs   []json.RawMessage `json:"refs"`
		}
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, "", fmt.Errorf("parse answer: %w", err)
		}
		if doc.Refs == nil {
			return nil, "", fmt.Errorf("answer object has no \"refs\" array")
		}
		return doc.Refs, doc.Rubric, nil
	default:
		return nil, "", fmt.Errorf("expected a JSON object or array of references")
	}
}

// rawRef is how one element of the untrusted answer is decoded before it is
// trusted. Its Line is a pointer so that an absent "line" stays distinguishable
// from a present 0: an absent line means the reference names the file alone and
// is checked for existence only, while a present 0 is out of range and is
// reported as such. Everything else mirrors Ref, which is the shape
// DisallowUnknownFields is closed against — a `confidence`, a `snippet`, or any
// other field the model adds is a hard error rather than a silently dropped key.
type rawRef struct {
	ID      string `json:"id"`
	Finding string `json:"finding"`
	Session string `json:"session"`
	Path    string `json:"path"`
	Line    *int   `json:"line"`
	Role    string `json:"role"`
	Status  string `json:"status"`
}

// positioned pairs a decoded reference with the line the answer gave (nil when
// absent) and at: its 1-based position in the answer the operator actually
// wrote, so an error can say "reference #3" and mean the third one in the
// answer even when an earlier element failed to decode.
type positioned struct {
	ref   Ref
	line  *int
	at    int
	clean string // the checked repo-relative path, set by validate when the path passes
}

// decodeRef strictly decodes one reference element.
func decodeRef(raw json.RawMessage) (positioned, error) {
	var rr rawRef
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rr); err != nil {
		return positioned{}, err
	}
	p := positioned{ref: Ref{
		ID:      rr.ID,
		Finding: rr.Finding,
		Session: rr.Session,
		Path:    rr.Path,
		Role:    rr.Role,
		Status:  rr.Status,
	}, line: rr.Line}
	if rr.Line != nil {
		p.ref.Line = *rr.Line
	}
	return p, nil
}

// refLabel names a reference in an error message: its own id when that id is
// well-formed, and otherwise its position in the answer.
func refLabel(r Ref, at int) string {
	if IsRefID(r.ID) {
		return r.ID
	}
	return fmt.Sprintf("reference #%d", at)
}

// validate runs every schema rule against the decoded references and returns
// all errors (transactional and exhaustive), each naming the reference, the
// field, and the offending value. The path and line checks are the only reads
// of the repository this package ever makes.
func validate(refs []positioned, sources map[string]analyze.Finding, wantSession string, root repoRoot) []error {
	var errs []error
	seen := map[string]int{}
	lines := lineCounter{}

	for i := range refs {
		p := &refs[i]
		r := p.ref
		label := refLabel(r, p.at)
		if !IsRefID(r.ID) {
			errs = append(errs, fmt.Errorf("%s: id %q must match ^R-\\d{3}$", label, r.ID))
		} else if prev, dup := seen[r.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate id (first seen at reference #%d)", r.ID, prev))
		} else {
			seen[r.ID] = p.at
		}

		// finding: must name a currently-confirmed, anchored, non-Mode-B finding.
		// Enforced here as well as in the request (emit omits every non-eligible
		// finding), so a hand-written or stale answer cannot smuggle a reference
		// to an unverified, rejected, duplicate, or anchorless finding past this
		// boundary.
		if _, ok := sources[session.SafeText(r.Finding)]; !ok {
			errs = append(errs, fmt.Errorf("%s: finding %q is not a confirmed finding with a selector or route in %s", label, r.Finding, session.FindingsFile))
		}

		if session.SafeText(r.Session) != session.SafeText(wantSession) {
			errs = append(errs, fmt.Errorf("%s: session %q is not this session (%q in %s)", label, r.Session, wantSession, session.ManifestFile))
		}

		if !roleSet[r.Role] {
			errs = append(errs, fmt.Errorf("%s: role %q must be one of owner|handler|route|test", label, r.Role))
		}

		full, clean, perr := root.checkPath(r.Path)
		if perr != nil {
			errs = append(errs, fmt.Errorf("%s: path %q: %v", label, session.SafeText(r.Path), perr))
		} else {
			p.clean = clean
		}
		if perr == nil && p.line != nil {
			// The line is checked only once the path is known to be a regular file
			// inside the repository: there is nothing to count otherwise, and a
			// second error would only restate the first.
			l := *p.line
			if l < 1 {
				errs = append(errs, fmt.Errorf("%s: line %d must be at least 1", label, l))
			} else if n, cerr := lines.count(full); cerr != nil {
				errs = append(errs, fmt.Errorf("%s: line %d: %v", label, l, cerr))
			} else if l > n {
				errs = append(errs, fmt.Errorf("%s: line %d is past the end of %s (%d lines)", label, l, session.SafeText(r.Path), n))
			}
		}
	}
	return errs
}
