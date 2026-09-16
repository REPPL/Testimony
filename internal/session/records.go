package session

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Append is one record appended to a session JSONL file.
//
// Three session artefacts hold a machine record plus appended human records —
// findings.jsonl (findings plus verdicts), tests.jsonl (test drafts plus
// decisions), and refs.jsonl (code references plus decisions) — and all are
// appended to under the same hazards: a planted
// symlink or FIFO at the path, a concurrent writer racing the measure-then-write
// sequence, an unterminated last line that a blind append would fuse onto, a
// short write leaving a newline-less fragment, and the read-side size invariants
// every reader scans to. AppendRecord holds that logic once so the callers
// cannot drift apart; the vocabulary of each record family stays with its own
// package.
type Append struct {
	Path   string                        // the file to append to
	Record []byte                        // the encoded record, without its newline
	Label  string                        // names the record in the line-limit error, e.g. "verdict for F-001"
	Kind   string                        // names the record kind in the file-limit error, e.g. "verdict"
	Verify func(current io.Reader) error // optional re-check of the file's contents, run under the lock
}

// AppendRecord appends a.Record as its own physical line without touching any
// existing line.
//
// It opens a.Path under the no-follow guard (O_APPEND|O_RDWR, so a planted
// symlink or non-regular file is refused rather than followed or blocked on) and
// takes an exclusive advisory lock across the whole probe → verify → write →
// rollback sequence. Two appenders to one session's file would otherwise race: A
// measures the end, B appends a full record past it, A's write fails part-way
// (ENOSPC — the case the rollback exists for), and A's truncate then cuts the
// file back below B's committed record, deleting it. The lock closes that
// window, so the length A rolls back to is the true end before A's own bytes.
//
// It pre-flights the record against MaxJSONLLine before the open (an unwritable
// record is a fact about the record alone) and, under the lock, the file against
// MaxJSONLBytes — the two read-side invariants every reader enforces, so
// a record is never persisted that would durably brick the file it lands in —
// runs a.Verify over the file's current contents (see Verify's own comment),
// frames the record with a leading newline when the file does not already end in
// one, writes it, truncates back to the pre-write length on a short write, and
// returns the Close error so a record is never reported written when its bytes
// did not reach disk (write-back deferred to close on NFS or a full device).
//
// a.Label and a.Kind name the record in the two size errors, so each caller's
// message reads in its own vocabulary; the file is named by a.Path's base.
func AppendRecord(a Append) error {
	name := filepath.Base(a.Path)
	// Hold the record to MaxJSONLLine, the shared read-side invariant every JSONL
	// writer respects. A record carries caller-supplied text verbatim, and in an
	// exchanged or hand-edited file that text can be just under the scanner cap —
	// small enough that the existing lines load, large enough that this record's
	// own framing tips its line over it. Appending it would durably brick the
	// human-decision history these files exist to protect: every later load would
	// fail with "token too long".
	//
	// Checked before the open, not after it: an unwritable record is a fact about
	// the record alone, so the refusal must name it whether or not the file exists
	// yet. Ordering it after the open reported a missing file — and its joined
	// filesystem path — in place of the over-limit refusal the caller can act on.
	if len(a.Record)+1 > MaxJSONLLine {
		return fmt.Errorf("%s encodes to %d bytes, over the %d-byte %s line limit",
			a.Label, len(a.Record)+1, MaxJSONLLine, name)
	}
	// O_RDWR rather than O_WRONLY because the record cannot be framed correctly
	// without first reading the byte already at the end of the file.
	f, err := OpenFileNoFollow(a.Path, os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return err
	}
	// Hold the file to MaxJSONLBytes, the total-size invariant the readers
	// enforce. A file written by an ingest step can legally sit right at the cap;
	// without this check the next appended record would land it past the cap, and
	// every later load would refuse it — including the record just appended, and
	// any recorded before it. Measured under the lock, after the file is open, so
	// a concurrent append cannot land between this check and the write below. The
	// budget is len(Record)+2, not +1: writeRecord prepends a second leading
	// newline when the file is non-empty and its last byte is not already one (an
	// exchanged or hand-edited file can end unterminated), so budgeting only +1
	// let a file at exactly the cap minus (len+1) pass and still land one byte
	// over.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if info.Size()+int64(len(a.Record))+2 > MaxJSONLBytes {
		f.Close()
		return fmt.Errorf("%s is %d bytes; appending this %s would push it past the %d-byte JSONL file limit; refusing to write a session no command could read back",
			name, info.Size(), a.Kind, MaxJSONLBytes)
	}
	// Verify re-reads the file the caller is about to append to, under the same
	// lock, so the record is still the one the operator decided on: a caller that
	// snapshots the file and then blocks on a human can have a concurrent
	// truncate-and-rewrite slide different content under the same ids in that gap.
	// The reader is a SectionReader over ReadAt, which leaves the file offset
	// untouched, and the descriptor is O_APPEND, so the write below still lands at
	// the true end of file.
	if a.Verify != nil {
		if err := a.Verify(io.NewSectionReader(f, 0, info.Size())); err != nil {
			f.Close()
			return err
		}
	}
	rec := make([]byte, 0, len(a.Record)+2)
	rec = append(rec, a.Record...)
	rec = append(rec, '\n')
	if err := writeRecord(f, rec); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// appendFile is the subset of *os.File that writeRecord needs; a fake satisfies
// it in tests to exercise the partial-write rollback.
type appendFile interface {
	io.Writer
	io.ReaderAt
	Seek(offset int64, whence int) (int64, error)
	Truncate(size int64) error
}

// writeRecord frames rec so it lands as its own physical line and writes it,
// rolling the file back if the write only partly lands.
//
// A session JSONL file need not end in a newline: it may have been hand edited,
// produced by another tool, or left short by a crash part-way through an earlier
// write. Appending blindly would fuse the record onto that unterminated final
// line, producing one physical line holding two JSON objects — which makes not
// just those two records but the entire file unparseable to every reader. So
// probe the last byte and open a fresh line when it is not already one.
// (O_APPEND still puts the write at the end, whatever the seek offset; the seek
// is only to learn the size.)
//
// The write itself needs a rollback: os.File.Write gives no atomicity guarantee,
// so a full disk fills the remaining space, returns a short count, and leaves a
// truncated, newline-less fragment behind. That fragment would fuse with the next
// successful write into one malformed physical line and make the whole file
// unparseable. So on any write error the file is truncated back to the length it
// had immediately before the write. The size is re-measured at that point rather
// than reusing the offset the newline probe learned: the descriptor is O_APPEND,
// so the write lands at the true end of file, which a concurrent appender may
// have moved on since the probe. Truncating to the stale offset would delete that
// other writer's record instead of only our own partial bytes.
func writeRecord(f appendFile, rec []byte) error {
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if end > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], end-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			rec = append([]byte{'\n'}, rec...)
		}
	}
	before, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := f.Write(rec); err != nil {
		// Best-effort roll back any partial bytes; surface the original error.
		f.Truncate(before)
		return err
	}
	return nil
}

// Commit is a whole-file replacement of a session JSONL file.
type Commit struct {
	Path    string                        // the file to replace
	Records [][]byte                      // each encoded record, without its newline
	Guard   func(current io.Reader) error // refuse the replacement by returning an error
}

// CommitRecords replaces Path's contents with Records under the same no-follow
// guard and exclusive lock AppendRecord takes.
//
// Guard runs over the file's current contents before anything is truncated, and
// returning an error from it refuses the whole replacement: that is where a
// caller protects an appended human record from a re-ingest. Probing in one open
// and rewriting in another left a TOCTOU window — a concurrent appender commits
// a record (under AppendRecord's own lock) between the probe and the truncate,
// and the rewrite destroys it, precisely the record the guard exists to protect.
// Holding one exclusive lock across probe, truncate, and write forecloses the
// interleaving: an appender blocks until the commit completes, so its record is
// either visible to the guard (and the commit refused) or appended after the new
// contents.
//
// The whole set is encoded into one buffer before the file is truncated, so the
// truncate and the write are a single Write of pre-built bytes rather than a
// streamed series that a mid-way I/O error (ENOSPC) could leave half-flushed.
// That matters because the truncate has already destroyed the prior bytes:
// without the rollback a short write leaves a truncated, newline-less JSON
// fragment, which not only breaks every reader but blocks the tool's own
// recovery — the next ingest's guard parses every line and errors on the fragment
// before it can conclude the file is free to rewrite. On any write error the file
// is therefore truncated back to empty: an empty JSONL file is parseable (zero
// records) and re-ingestable, so the failure state does not foreclose its own
// repair. The Close error is returned for the same reason AppendRecord returns
// it. Callers pre-flight Records against MaxJSONLLine and MaxJSONLBytes before
// calling, because only they can name a record by its own id or its position in
// an answer.
func CommitRecords(c Commit) error {
	f, err := OpenFileNoFollow(c.Path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return err
	}
	if c.Guard != nil {
		if err := c.Guard(f); err != nil {
			f.Close()
			return err
		}
	}
	if err := writeRecords(f, c.Records); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(c.Path), err)
	}
	// Close releases the lock with the descriptor.
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(c.Path), err)
	}
	return nil
}

// commitFile is the subset of *os.File that writeRecords needs; a fake satisfies
// it in tests to exercise the truncate-then-write rollback.
type commitFile interface {
	io.Writer
	Truncate(size int64) error
	Seek(offset int64, whence int) (int64, error)
}

// writeRecords replaces f's contents with records, one per line, rolling the file
// back to empty if the write only partly lands. See CommitRecords for why the set
// is buffered whole and why the failure state is an empty file.
func writeRecords(f commitFile, records [][]byte) error {
	var buf bytes.Buffer
	for _, r := range records {
		buf.Write(r)
		buf.WriteByte('\n')
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		// Best-effort roll back to an empty (parseable, re-ingestable) file, then
		// surface the original error.
		f.Truncate(0)
		return err
	}
	return nil
}
