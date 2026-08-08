// Package demo serves a small instrumented web app so a think-aloud session
// can be captured end-to-end before any real application is wired up. It
// persists two streams into a fresh session directory: raw rrweb events
// (archival) and normalised interactions (what merge consumes).
package demo

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/REPPL/Testimony/internal/session"
	"github.com/REPPL/Testimony/internal/timeline"
)

//go:embed assets/index.html
var assets embed.FS

type server struct {
	mu           sync.Mutex
	interactions *os.File
	rawEvents    *os.File
	t0           int64 // manifest t0_epoch_ms, anchoring the interaction shape check
	// entryBytes is the running total of accepted interactions' *wrapped*
	// timeline-entry size, id growth included (see idGrowth) — a lower-bound
	// estimate of what those records contribute to timeline.jsonl once merge's
	// WriteJSONL writes it, not their raw interactions.jsonl bytes and not the
	// transcript-derived speech entries WriteJSONL also writes into the same
	// file, which this endpoint cannot see at capture time. Every real caller
	// opens interactions.jsonl empty (session.Create always precedes
	// serveOn), so starting this at zero tracks the file exactly.
	// entryCount is the running count backing it, needed to charge each
	// record the same real-id growth eventIDGrowthMargin bounds per record
	// (see idGrowth): merge assigns "ev-%03d" by an interaction's position
	// among every interaction in interactions.jsonl, one-to-one with capture
	// order, so entryCount+1 is always the ordinal the next accepted record
	// would get.
	entryBytes int64
	entryCount int64
}

// DefaultApp is the app-under-test name a demo session records.
const DefaultApp = "testimony demo"

// DefaultTask is the seeded task for a demo session.
const DefaultTask = "Explore the settings prototype and think aloud"

// The capture server serves one operator over loopback, so every phase of a
// request has a generous but finite budget. Without these an http.Server waits
// for ever: a single connection that opens and then stalls before sending its
// request headers — a browser tab suspended by the OS, a half-closed socket a
// sleeping laptop left behind — keeps a connection alive indefinitely, and
// Shutdown waits for it, so Ctrl+C hangs instead of finalising the session.
// readTimeout covers the whole request including a maxBatchBody rrweb batch,
// which crosses loopback in milliseconds; idleTimeout reaps keep-alive
// connections the page will not reuse.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	idleTimeout       = 120 * time.Second
)

// shutdownTimeout bounds how long a caller waits for in-flight capture writes
// to finish before the server is closed out from under them. Finalising the
// session promptly matters more than the last few bytes of one stalled
// connection, and an operator who has pressed Ctrl+C is waiting.
const shutdownTimeout = 5 * time.Second

// maxBatchBody caps a raw-event batch body. A batch carries many records, so it
// may legitimately exceed the limit that applies to any one of them; each line
// it produces is still checked individually against session.MaxJSONLLine.
const maxBatchBody = 8 << 20

// Run starts the demo capture server on addr, creating a new session
// directory under outRoot. It blocks until the process is interrupted.
func Run(addr, outRoot string) error {
	// Bind before the session directory exists. The CLI's CheckAddr already
	// refuses a malformed -addr up front, but a well-formed address can still
	// fail to bind (the port is taken, or the host cannot be listened on), and
	// creating the directory first left a stray session behind — a manifest
	// plus two empty stream files — for a server that never served. Binding is
	// the only way to learn the answer, so it goes first; a directory-creation
	// failure after it just closes the listener, leaving nothing behind either
	// way.
	ln, err := Bind(addr)
	if err != nil {
		return err
	}
	dir, err := session.Create(outRoot, time.Now(), session.Manifest{
		App:         DefaultApp,
		Participant: "P1",
		Tasks:       []string{DefaultTask},
	})
	if err != nil {
		ln.Close()
		return err
	}

	srv, err := serveOrCleanup(ln, addr, dir)
	if err != nil {
		return err
	}

	fmt.Printf(`testimony demo — capture session started

  session dir : %s
  url         : %s

  1. Start your voice/screen recorder NOW (QuickTime → File → New Audio Recording).
  2. Say “session start” aloud, open the URL, and think aloud while you explore.
  3. When done: stop the recorder, press Ctrl+C here, then:

       testimony transcribe -session %s -audio <your-recording.m4a>
       testimony merge      -session %s
       testimony report     -session %s

`, dir, DisplayURL(displayAddr(srv, addr)), dir, dir, dir)

	// Block until interrupted, then shut the server down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return Shutdown(srv)
}

// Shutdown stops a capture server returned by Serve, under a deadline. It is
// how every caller should stop the server: srv.Shutdown with a context that
// never expires blocks for as long as any connection stays open, so one stalled
// client left Ctrl+C hanging for ever instead of finalising the session. When
// the graceful drain misses the deadline the remaining connections are closed
// outright — the two stream files use direct O_APPEND writes, so records already
// accepted are durable either way.
func Shutdown(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return srv.Close()
	}
	return nil
}

// Serve starts the demo capture server on addr, appending its two interaction
// streams into the existing session directory dir. It binds synchronously (so
// a bind failure is returned) and then serves in the background, returning the
// running *http.Server for the caller to Shutdown. Prefer Bind followed by
// ServeListener when the bind must happen before dir is created — record does,
// so a refused bind never leaves a stray session directory behind; demo.Run
// follows the same ordering itself and enters at serveOrCleanup, so it also
// removes dir on a failure this leaves to the caller.
func Serve(addr, dir string) (*http.Server, error) {
	ln, err := Bind(addr)
	if err != nil {
		return nil, err
	}
	return serveOn(ln, addr, dir)
}

// Bind resolves and binds a capture server address, without touching a
// session directory. A well-formed address can still fail to bind (the port
// is taken, or the host cannot be listened on), and binding is the only way
// to learn that — splitting it out from Serve lets a caller reserve the port
// before deciding whether to create anything else that a refused bind would
// otherwise leave stranded.
func Bind(addr string) (net.Listener, error) {
	bind, err := listenAddr(addr)
	if err != nil {
		return nil, err
	}
	return net.Listen("tcp", bind)
}

// ServeListener serves the demo capture app on an already-bound listener ln
// (as returned by Bind), appending its two interaction streams into the
// existing session directory dir. addr is the address the operator
// requested, kept only to surface the requested host alongside the bound
// port on the returned server.
func ServeListener(ln net.Listener, addr, dir string) (*http.Server, error) {
	return serveOn(ln, addr, dir)
}

// serveOrCleanup calls serveOn and, on failure, also removes dir. It exists
// for Run, the one caller that both creates dir itself immediately beforehand
// and owns its whole lifecycle: a serveOn failure this rare — reloading the
// manifest session.Create just wrote, or a stream file's own open, failing
// (e.g. a full disk) — must not leave the directory behind for a server that
// never served, the same guarantee Run's bind-first ordering already gives
// the far more common case of a taken port.
// ServeListener and Serve do not use this: dir there may be a session a
// caller (e.g. record) is already managing across other recorders, so a
// serve failure must not delete it out from under them.
func serveOrCleanup(ln net.Listener, addr, dir string) (*http.Server, error) {
	srv, err := serveOn(ln, addr, dir)
	if err != nil {
		os.RemoveAll(dir)
	}
	return srv, err
}

// serveOn serves the demo capture app on an already-bound listener, taking
// ownership of it: on any error the listener is closed. addr is the address
// the operator requested, kept only to surface the requested host alongside
// the bound port on the returned server.
func serveOn(ln net.Listener, addr, dir string) (*http.Server, error) {
	// The interaction shape check needs the manifest's t0 anchor, and every
	// caller creates the session (and so the manifest) before serving into it.
	// Loading it here also refuses a session whose anchor merge could never
	// use, before any capture is accepted against it.
	man, err := session.LoadManifest(dir)
	if err != nil {
		ln.Close()
		return nil, err
	}
	t0, err := man.T0()
	if err != nil {
		ln.Close()
		return nil, err
	}
	open := func(name string) (*os.File, error) {
		return session.OpenFileNoFollow(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	inter, err := open(session.InteractionsFile)
	if err != nil {
		ln.Close()
		return nil, err
	}
	raw, err := open(session.RawEventsFile)
	if err != nil {
		inter.Close()
		ln.Close()
		return nil, err
	}
	s := &server{interactions: inter, rawEvents: raw, t0: t0}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := assets.ReadFile("assets/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("/api/interactions", s.handleInteraction)
	mux.HandleFunc("/api/events", s.handleRawEvents)

	// A deliberately wider bind serves the page to other devices, but allowWrite
	// still pins capture posts to loopback clients — lifting that pin would
	// reopen the CSRF/DNS-rebinding surface the guard exists for. The operator
	// must hear the consequence up front: their clients post via sendBeacon,
	// which surfaces no status to the page, so without this line the first
	// signal that a remote participant's session recorded nothing was merge
	// counting 0 events.
	if !loopbackHost(ln.Addr().String()) {
		fmt.Fprintf(os.Stderr, "testimony demo: warning: bound to %s, but capture posts are accepted from loopback clients only — a page opened from another device is served yet records nothing\n", ln.Addr().String())
	}
	// The two stream files use direct O_APPEND writes (no buffering), so their
	// data is durable without an explicit Close; the OS reclaims them on exit,
	// as before. Not closing them on Shutdown avoids racing an in-flight write.
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
	}
	// Surface the actually-bound address on the returned server, keeping the
	// operator's requested host but taking the port from the listener: ":0"
	// binds an ephemeral port only net.Listen knows, and printing the requested
	// addr rendered the unopenable http://localhost:0 while the real port was
	// discoverable only outside the tool. For any explicit port the two agree
	// and srv.Addr equals the requested addr.
	if _, boundPort, perr := net.SplitHostPort(ln.Addr().String()); perr == nil {
		if reqHost, _, herr := net.SplitHostPort(addr); herr == nil {
			srv.Addr = net.JoinHostPort(reqHost, boundPort)
		}
	}
	go srv.Serve(ln)
	return srv, nil
}

// handleInteraction appends one normalised interaction (a single JSON object)
// as one line of interactions.jsonl.
func (s *server) handleInteraction(w http.ResponseWriter, r *http.Request) {
	s.appendLines(w, r, s.interactions, false)
}

// handleRawEvents appends a batch (JSON array) of raw rrweb events, one per
// line, to events.rrweb.jsonl.
func (s *server) handleRawEvents(w http.ResponseWriter, r *http.Request) {
	s.appendLines(w, r, s.rawEvents, true)
}

func (s *server) appendLines(w http.ResponseWriter, r *http.Request, f *os.File, batch bool) {
	if r.Method != http.MethodPost {
		refuseWrite(w, r, "method "+r.Method, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !allowWrite(w, r) {
		return
	}
	// The write side must respect the read side's invariant: every JSONL reader
	// stops at session.MaxJSONLLine, so a longer line accepted here would be
	// durably persisted and permanently unreadable, breaking merge, report and
	// analyze for the whole session. A single interaction can therefore never be
	// larger than one readable line; a batch may be, because it becomes many. Read
	// one byte past the cap so an over-long body is refused as too large rather
	// than silently truncated and then rejected as invalid JSON, which would tell
	// the operator the page sent nonsense when it sent too much.
	maxBody := int64(session.MaxJSONLLine)
	if batch {
		maxBody = maxBatchBody
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		refuseWrite(w, r, fmt.Sprintf("unreadable body: %v", err), err.Error(), http.StatusBadRequest)
		return
	}
	if int64(len(body)) > maxBody {
		refuseWrite(w, r, "body over the size limit", "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var lines [][]byte
	var entryLen int // this request's wrapped timeline-entry size; unset (0) for a batch
	if batch {
		var msgs []json.RawMessage
		if err := json.Unmarshal(body, &msgs); err != nil || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
			refuseWrite(w, r, "body is not a JSON array", "expected JSON array", http.StatusBadRequest)
			return
		}
		for _, m := range msgs {
			line, err := compactLine(m)
			if err != nil {
				refuseWrite(w, r, "invalid JSON in a batch element", "invalid JSON", http.StatusBadRequest)
				return
			}
			if tooLongForJSONL(line) {
				refuseWrite(w, r, "batch element over the JSONL line limit", "record exceeds the readable JSONL line limit", http.StatusRequestEntityTooLarge)
				return
			}
			lines = append(lines, line)
		}
	} else {
		line, err := compactLine(body)
		if err != nil {
			refuseWrite(w, r, "invalid JSON", "invalid JSON", http.StatusBadRequest)
			return
		}
		// The write side must respect the read side's shape invariant too, not
		// just its line-length one: merge refuses an interactions.jsonl record
		// that is not an object carrying the required t and kind, so accepting
		// one here (204) would durably persist a line that fails the whole
		// session's merge after the participant has gone. This is the single-
		// record interaction path only; a raw-event batch is archival and no
		// reader constrains its element shape.
		if tooLongForJSONL(line) {
			refuseWrite(w, r, "record over the JSONL line limit", "record exceeds the readable JSONL line limit", http.StatusRequestEntityTooLarge)
			return
		}
		if err := timeline.CheckInteraction(line, s.t0); err != nil {
			msg := fmt.Sprintf("interaction %v", err)
			refuseWrite(w, r, msg, msg, http.StatusBadRequest)
			return
		}
		// Fitting the JSONL line limit as received is not enough: merge re-frames
		// this record into a timeline entry (rebased t, a src/id/payload envelope)
		// that session.WriteJSONL checks against the same limit, and that entry can
		// be larger than the record itself — so check the entry a record accepted
		// here will become, not just the record, or a record this endpoint answers
		// 204 to could still be one merge permanently refuses.
		var rec timeline.Interaction
		if err := json.Unmarshal(line, &rec); err != nil {
			refuseWrite(w, r, "invalid JSON", "invalid JSON", http.StatusBadRequest)
			return
		}
		n, err := session.EncodedLen(timeline.EventEntry(rec, s.t0))
		if err != nil {
			refuseWrite(w, r, "invalid JSON", "invalid JSON", http.StatusBadRequest)
			return
		}
		entryLen = n
		if tooLongOnceWrapped(entryLen) {
			refuseWrite(w, r, "interaction's timeline entry over the JSONL line limit", "record exceeds the readable JSONL line limit", http.StatusRequestEntityTooLarge)
			return
		}
		lines = append(lines, line)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// interactions.jsonl is one of ReadJSONL's total-size-capped artefacts, unlike
	// events.rrweb.jsonl, which session.MaxJSONLBytes' own doc comment exempts as
	// archival — merge reads interactions.jsonl through session.ReadJSONL, which
	// refuses a file over MaxJSONLBytes outright. Each record above already fits
	// MaxJSONLLine on its own, but nothing bounded how many accumulate: a sequence
	// of individually-valid captures could each answer 204 and still leave the
	// session's interactions.jsonl durably unmergeable — discovered only after the
	// participant is gone, with no in-tool repair (merge refuses the file outright,
	// so timeline.jsonl is never produced and report/analyze then fail for lack of
	// it; nothing here can retroactively split interactions.jsonl back under the
	// cap). Check the total this write would reach before committing it, the
	// same write-before-read stance WriteJSONL and oversizedFindings take for
	// their own artefacts. Batched raw-event uploads (events.rrweb.jsonl) skip
	// this: they are archival and read by nothing that enforces a total.
	//
	// Fitting under interactions.jsonl's own raw-byte cap is not enough either:
	// merge re-frames each record into a timeline entry (rebased t, a
	// src/id/payload envelope) before session.WriteJSONL checks *that* total
	// against the same cap, and the wrapped form runs larger than the raw one —
	// the same gap the per-record tooLongOnceWrapped check above closes for one
	// record at a time. Tracking only the raw total let a run of individually-
	// valid, 204-accepted records cross the wrapped cap tens of thousands of
	// records before the raw one caught up, durably bricking the session with no
	// warning. entryBytes tracks the running wrapped event total the same way
	// size tracks the running raw one, charged with idGrowth so it, like
	// tooLongOnceWrapped, accounts for the real id merge assigns rather than
	// EventEntry's fixed-width placeholder. It narrows this gap rather than
	// closing it outright: timeline.jsonl also carries transcript's speech
	// entries, written into the same file by the same merge call, which this
	// endpoint has no way to see at capture time.
	var grown int64
	if !batch {
		size, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "testimony demo: capture write failed, event(s) dropped: %v\n", err)
			http.Error(w, "capture write failed", http.StatusInternalServerError)
			return
		}
		for _, l := range lines {
			size += int64(len(l)) + 1
		}
		if size > session.MaxJSONLBytes {
			refuseWrite(w, r, "capture would push interactions.jsonl over the session's JSONL file limit",
				"session's captured interactions are at the file size limit; start a new session", http.StatusRequestEntityTooLarge)
			return
		}
		grown = idGrowth(s.entryCount + 1)
		if s.entryBytes+int64(entryLen)+grown > session.MaxJSONLBytes {
			refuseWrite(w, r, "capture's timeline entry would push the session's merged timeline over the JSONL file limit",
				"session's captured interactions are at the file size limit; start a new session", http.StatusRequestEntityTooLarge)
			return
		}
	}
	if err := appendRecords(f, lines); err != nil {
		// The capture was not persisted. Tell the client so it does not treat a
		// dropped event as recorded (it answers the 500 rather than a 204), and log
		// to the operator's terminal: the client uses sendBeacon, which cannot report
		// a server status back to the page, so this line is the only signal the person
		// running the session gets that their evidence stream has started dropping.
		fmt.Fprintf(os.Stderr, "testimony demo: capture write failed, event(s) dropped: %v\n", err)
		http.Error(w, "capture write failed", http.StatusInternalServerError)
		return
	}
	if !batch {
		s.entryBytes += int64(entryLen) + grown
		s.entryCount++
	}
	w.WriteHeader(http.StatusNoContent)
}

// appendFile is the subset of *os.File that appendRecords needs; a fake
// satisfies it in tests to exercise the partial-write rollback.
type appendFile interface {
	io.Writer
	Seek(offset int64, whence int) (int64, error)
	Truncate(size int64) error
}

// appendRecords writes each line and its terminating newline to f. os.File.Write
// gives no atomicity guarantee: a full disk fills the remaining space and returns
// a short count, so a bare Write can persist a truncated, newline-less prefix
// (e.g. `{"t":123,"kind":"cl`) before ENOSPC surfaces. That partial line would
// join the next successful write into one malformed physical record and break
// the JSONL reader for the whole file. So on any write error appendRecords
// truncates f back to the length it had before the failing write, so no partial
// line survives — the caller only reports the drop, the file stays clean.
func appendRecords(f appendFile, lines [][]byte) error {
	for _, l := range lines {
		before, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(l, '\n')); err != nil {
			// Best-effort roll back any partial bytes; surface the original error.
			f.Truncate(before)
			return err
		}
	}
	return nil
}

// compactLine canonicalises one accepted JSON value into a single newline-free
// physical line. json.Compact strips insignificant whitespace — including the
// raw newlines JSON permits between tokens — so one accepted value maps to
// exactly one JSONL line and cannot be split across lines, which would corrupt
// interactions.jsonl / events.rrweb.jsonl and break merge's line-by-line
// reader. It also rejects invalid JSON (replacing the previous json.Valid gate).
func compactLine(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// tooLongForJSONL reports whether line, plus the newline appendRecords adds,
// would exceed what session.ReadJSONL and analyze.Load can scan back. It is
// checked before anything is written, so a batch carrying one over-long record
// is refused whole rather than leaving the records before it on disk followed by
// a line no reader can reach past.
func tooLongForJSONL(line []byte) bool {
	return len(line)+1 > session.MaxJSONLLine
}

// eventIDGrowthMargin bounds tooLongOnceWrapped's blind spot: timeline.EventEntry
// sizes an interaction's entry with the placeholder id "ev-001" (6 bytes), but
// the id a merged session actually assigns grows with the interaction's
// position among every interaction in the session, which this single record
// does not know. 32 spare bytes cover an "ev-%03d" ordinal up to 32 digits —
// past 10^32 interactions — headroom no real session comes remotely close to
// needing.
const eventIDGrowthMargin = 32

// tooLongOnceWrapped reports whether an interaction's timeline entry, sized at
// entryLen by session.EncodedLen(timeline.EventEntry(...)), could exceed the
// JSONL line limit once merge assigns its real id — the entry this record
// becomes, not the record itself, is what session.WriteJSONL checks when merge
// writes timeline.jsonl.
func tooLongOnceWrapped(entryLen int) bool {
	return entryLen+eventIDGrowthMargin > session.MaxJSONLLine
}

// idGrowth is the total-size guard's twin of eventIDGrowthMargin: how many
// bytes longer nth — the real, 1-based ordinal merge's "ev-%03d" would give
// an interaction at this position among every interaction in the session —
// runs than the "ev-001" placeholder EventEntry sizes it with. A flat
// eventIDGrowthMargin is cheap paid once per record; charging that same 32
// bytes into a running total for every one of a session's records would waste
// a real fraction of the file's capacity for growth that stays 0 until the
// 1000th interaction (up to a third of it, on the smallest legal entries), so
// this charges only what nth's own digit count actually adds.
func idGrowth(nth int64) int64 {
	if nth < 1000 {
		return 0
	}
	return int64(len(strconv.FormatInt(nth, 10))) - 3
}

// allowWrite guards the capture write endpoints against cross-origin forgery
// (CSRF), DNS-rebinding of the loopback server, and — on a deliberately wide
// bind — any non-browser client that simply forges the Host header, since
// nothing else about the request has to originate from a browser. It requires
// a loopback RemoteAddr (the actual TCP peer, which a client cannot forge), a
// loopback Host (a rebinding page still sends the attacker hostname), a
// loopback Origin when present (any loopback host, whatever its port), and a
// JSON Content-Type — a non-CORS-safelisted type that forces a preflight the
// server never answers permissively, so a cross-origin no-cors "simple
// request" POST cannot reach the write. It writes the error response and
// returns false when the request must be refused.
func allowWrite(w http.ResponseWriter, r *http.Request) bool {
	if !loopbackHost(r.RemoteAddr) {
		refuseWrite(w, r, fmt.Sprintf("non-loopback remote address %q", r.RemoteAddr), "unexpected remote address", http.StatusForbidden)
		return false
	}
	if !loopbackHost(r.Host) {
		refuseWrite(w, r, fmt.Sprintf("unexpected Host %q", r.Host), "unexpected Host", http.StatusForbidden)
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || !loopbackHost(u.Host) {
			refuseWrite(w, r, fmt.Sprintf("cross-origin Origin %q", o), "cross-origin request rejected", http.StatusForbidden)
			return false
		}
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		refuseWrite(w, r, fmt.Sprintf("Content-Type %q", r.Header.Get("Content-Type")), "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

// refuseWrite answers a refused capture request and logs the refusal to the
// operator's terminal, for the same reason a failed persist is logged: the
// page posts via sendBeacon, which cannot report a status back, so stderr is
// the only signal that capture posts are being refused — by the forgery
// guard, a malformed or mis-shaped record, or an over-long body. Every
// refusal path answers through this one helper so none can go silent again.
func refuseWrite(w http.ResponseWriter, r *http.Request, reason, status string, code int) {
	fmt.Fprintf(os.Stderr, "testimony demo: capture write refused (%s) from %s\n", reason, r.RemoteAddr)
	http.Error(w, status, code)
}

// loopbackHost reports whether hostport names the local machine: the literal
// "localhost" or any loopback IP. Used to pin the RemoteAddr/Host/Origin of a
// capture write to loopback.
func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isJSONContentType reports whether ct is an application/json media type,
// tolerating a charset or other parameter.
func isJSONContentType(ct string) bool {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}

// displayAddr returns the address to print for a running capture server: the
// bound address Serve recorded on the server when available (which differs
// from the request only when an ephemeral ":0" port was asked for), else the
// requested one.
func displayAddr(srv *http.Server, requested string) string {
	if srv != nil && srv.Addr != "" {
		return srv.Addr
	}
	return requested
}

// DisplayURL renders the human-facing URL an operator opens for a capture
// server bound to addr. It shows "localhost" only for the host-less default
// (":8737" -> http://localhost:8737); when an operator passes an explicit host
// for a wider bind (e.g. "0.0.0.0:8737") it shows that host, so the printed URL
// is never the broken "http://localhost0.0.0.0:8737" that raw concatenation of
// addr after the "localhost" literal produced. Shared by demo.Run and
// record.printStatus.
func DisplayURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://localhost" + addr // malformed addr: preserve the old form
	}
	if host == "" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// CheckAddr validates a capture listen address without binding it, so the CLI
// can refuse a malformed -addr as a usage error before any session directory
// is created. Serve keeps applying the same rule when it binds.
func CheckAddr(addr string) error {
	_, err := listenAddr(addr)
	return err
}

// listenAddr binds the capture server to loopback by default: a bare ":8737"
// (empty host) becomes "127.0.0.1:8737", so the unauthenticated write endpoints
// are not published to the LAN even though the banner prints "localhost". An
// operator who deliberately passes an explicit host (e.g. "0.0.0.0:8737") gets
// the wider bind, but it serves the PAGE only: allowWrite keeps refusing
// capture posts from non-loopback clients, and Serve warns about exactly that
// at startup.
//
// An addr that does not parse into host and port is refused outright rather
// than passed through to net.Listen. Passing it through defeated the very
// defaulting above: net.Listen("tcp", "") binds every interface, so an empty
// -addr published the unauthenticated capture write endpoints to the whole LAN
// on an arbitrary port, silently and with the banner still saying "localhost".
// Refusing names the expected form instead, and leaves the deliberate host-less
// ":8737" -> loopback behaviour untouched.
//
// A numeric port outside 0-65535 (e.g. ":99999") is refused here too, as a
// usage error rather than a runtime one: net.Listen already rejects it during
// address resolution ("listen tcp: address 99999: invalid port"), but only at
// exit 1, indistinguishable from a genuine bind failure such as a taken port.
// A non-numeric port (e.g. ":http") is left to net.Listen's own /etc/services
// lookup, which this function cannot replicate.
func listenAddr(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid capture address %q: want host:port or :port, e.g. \":8737\"", addr)
	}
	if n, numErr := strconv.Atoi(port); numErr == nil && (n < 0 || n > 65535) {
		return "", fmt.Errorf("invalid capture address %q: port %d is out of range (0-65535)", addr, n)
	} else if errors.Is(numErr, strconv.ErrRange) {
		return "", fmt.Errorf("invalid capture address %q: port %s is out of range (0-65535)", addr, port)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
