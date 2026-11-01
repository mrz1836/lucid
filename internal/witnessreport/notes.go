package witnessreport

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/mrz1836/lucid/internal/storage"
)

// This file owns the optional week notes (witness_report.notes_file, ADR-0015):
// the second deliberate, opt-in exception to the package's by-construction
// privacy. The notes are one operator-approved file, opened on exactly the
// configured path and never dir-walked, and refused unread when the path
// resolves into the Lucid home — so the package still has no reader for
// observations, journal entries, or raw log content. The read is deterministic
// and model-free: it takes an explicit now, never a file's modification time,
// and every problem is a status on the report, never an error, so a notes
// problem can never block or delay the report.

// The week-notes statuses (ADR-0015 §4). The zero value "" is off — no
// notes_file is configured — and adds nothing to the report. Only included
// notes are rendered or handed to the model; invalid and unreadable alert the
// operator privately after delivery; stale and empty are normal states.
const (
	NotesIncluded   = "included"
	NotesStale      = "stale"
	NotesEmpty      = "empty"
	NotesInvalid    = "invalid"
	NotesUnreadable = "unreadable"
)

// maxWeekNotesRunes caps the trimmed notes body, counted in Unicode code
// points (ADR-0015 §7). Over the cap the notes are invalid and left out whole —
// never truncated — which also keeps a wrongly configured file from flooding
// the witness channel.
const maxWeekNotesRunes = 4000

// notesDateLayout is the calendar-date form of the frontmatter's written key
// and of the report's notes_written field.
const notesDateLayout = "2006-01-02"

// The fixed reasons a notes problem carries (ADR-0015 §4). They name what is
// wrong and never embed any of the file's content, so the private alert and
// the dry-run note can never leak the notes.
const (
	notesProblemNoFrontmatter = "no frontmatter"
	notesProblemNotClosed     = "frontmatter is not closed"
	notesProblemBadYAML       = "frontmatter is not valid YAML"
	notesProblemNoWritten     = "written is missing"
	notesProblemBadWritten    = "written is not a YYYY-MM-DD date"
	notesProblemFuture        = "written is in the future"
	notesProblemOverCap       = "body is over the 4,000-code-point cap"
	notesProblemUnpackable    = "body cannot be packed losslessly into Discord fields"
	notesProblemInsideHome    = "notes_file is inside the Lucid home"
	notesProblemHomeUnset     = "the Lucid home is not set"
	notesProblemMissing       = "the path does not exist"
	notesProblemDirectory     = "the path is a directory"
	notesProblemCannotRead    = "the file cannot be read"
)

// WeekNotes is the outcome of reading the week-notes file for one report.
// Status is "" (off) or one of the Notes* statuses. Body is the verbatim notes
// body and is set only when Status is NotesIncluded — an unusable body is never
// returned, so it cannot reach the render or the model. Written is the parsed
// written date (YYYY-MM-DD), set whenever it parsed. Problem is the fixed
// reason for NotesInvalid and NotesUnreadable.
type WeekNotes struct {
	Status  string
	Body    string
	Written string
	Problem string
}

// notesSeam is the file-system seam the notes read goes through, so a test can
// prove exactly which paths are touched (the configured path and the home,
// nothing else) and that a refused file's bytes are never read.
type notesSeam struct {
	readFile     func(name string) ([]byte, error)
	stat         func(name string) (fs.FileInfo, error)
	evalSymlinks func(path string) (string, error)
}

// osNotesSeam is the real file system.
func osNotesSeam() notesSeam {
	return notesSeam{readFile: os.ReadFile, stat: os.Stat, evalSymlinks: filepath.EvalSymlinks}
}

// readWeekNotes reads the operator's week-notes file at exactly path for the
// report at now. home is the Lucid home: a path that resolves to or under it —
// lexically, or through a symlink — is refused unread, and an empty home fails
// closed, because the package never reads a Ledger file (P3). The order is
// fixed: blank path (off), empty home, lexical inside-home, stat, the
// symlink-resolved inside-home check, the read, then the parse. It never
// returns an error: every problem is a status.
func readWeekNotes(path, home string, now time.Time, seam notesSeam) WeekNotes {
	if strings.TrimSpace(path) == "" {
		return WeekNotes{}
	}
	if refused, ok := checkNotesPath(path, home, seam); !ok {
		return refused
	}
	b, err := seam.readFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return notesProblem(NotesUnreadable, notesProblemMissing)
	case err != nil:
		return notesProblem(NotesUnreadable, notesProblemCannotRead)
	}
	return parseWeekNotes(b, now)
}

// checkNotesPath runs every check that comes before the notes file's bytes are
// read: the home is set, the path is not inside it lexically, the path is an
// existing file, and its symlink-resolved form is not inside the home either.
// It returns the status to report and false when the read must not happen.
func checkNotesPath(path, home string, seam notesSeam) (WeekNotes, bool) {
	if strings.TrimSpace(home) == "" {
		return notesProblem(NotesInvalid, notesProblemHomeUnset), false
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		// The home cannot be resolved, so the inside-home check cannot run:
		// fail closed exactly as for an unset home.
		return notesProblem(NotesInvalid, notesProblemHomeUnset), false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return notesProblem(NotesUnreadable, notesProblemCannotRead), false
	}
	if pathWithin(absPath, absHome) {
		return notesProblem(NotesInvalid, notesProblemInsideHome), false
	}

	info, err := seam.stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return notesProblem(NotesUnreadable, notesProblemMissing), false
	case err != nil:
		return notesProblem(NotesUnreadable, notesProblemCannotRead), false
	case info.IsDir():
		return notesProblem(NotesUnreadable, notesProblemDirectory), false
	}

	inside, err := resolvesInsideHome(path, absHome, seam)
	switch {
	case err != nil:
		return notesProblem(NotesUnreadable, notesProblemCannotRead), false
	case inside:
		return notesProblem(NotesInvalid, notesProblemInsideHome), false
	}
	return WeekNotes{}, true
}

// resolvesInsideHome reports whether path, with every symlink resolved, lands
// in the home — compared against the resolved home and against the home as
// configured — so a symlink (or a symlinked parent) pointing into the Ledger
// is refused even when the configured path is outside it.
func resolvesInsideHome(path, absHome string, seam notesSeam) (bool, error) {
	resolved, err := seam.evalSymlinks(path)
	if err != nil {
		return false, err
	}
	if resolved, err = filepath.Abs(resolved); err != nil {
		return false, err
	}
	resolvedHome := absHome
	if rh, herr := seam.evalSymlinks(absHome); herr == nil {
		resolvedHome = rh
	}
	return pathWithin(resolved, resolvedHome) || pathWithin(resolved, absHome), nil
}

// parseWeekNotes parses a week-notes file's bytes for the report at now (ADR-0015
// §2–§4, §7). A leading UTF-8 BOM is stripped and CRLF normalized to LF; the
// frontmatter must open on the first line and close; written must be a
// calendar date no later than the report's day; the body — everything after
// the closing fence, leading and trailing blank lines trimmed, otherwise byte
// for byte — must be within the cap and packable losslessly into Discord
// fields. Only then does freshness decide stale, empty, or included, so empty
// and stale always mean a valid file.
func parseWeekNotes(content []byte, now time.Time) WeekNotes {
	ref, loc := weekRef(now)
	written, rawBody, problem := splitWeekNotes(content, loc)
	if problem != "" {
		return notesProblem(NotesInvalid, problem)
	}

	notes := WeekNotes{Written: written.Format(notesDateLayout)}
	body := trimBlankLines(string(rawBody))
	switch {
	case written.After(ref):
		// A future date is invalid, so a file cannot be pre-dated to stay fresh.
		notes.Status, notes.Problem = NotesInvalid, notesProblemFuture
	case utf8.RuneCountInString(body) > maxWeekNotesRunes:
		notes.Status, notes.Problem = NotesInvalid, notesProblemOverCap
	case body != "" && !rawBodyPackable(body):
		notes.Status, notes.Problem = NotesInvalid, notesProblemUnpackable
	case written.Before(ref.AddDate(0, 0, -6)):
		// Calendar days, not a 6×24h duration, so a DST change inside the
		// window never shifts its first day.
		notes.Status = NotesStale
	case body == "":
		notes.Status = NotesEmpty
	default:
		notes.Status, notes.Body = NotesIncluded, body
	}
	return notes
}

// splitWeekNotes normalizes a notes file (BOM stripped, CRLF to LF), splits its
// frontmatter with the pure storage splitter, and parses written strictly as a
// calendar date in loc. written is decoded as a raw YAML node, so an unquoted
// date arrives as its text; unknown keys are ignored. problem is the fixed
// reason when the frontmatter is unusable, "" otherwise.
func splitWeekNotes(content []byte, loc *time.Location) (written time.Time, body []byte, problem string) {
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))

	front, body, err := storage.SplitFrontmatter(content)
	if err != nil {
		first, _, _ := bytes.Cut(content, []byte("\n"))
		if string(bytes.TrimRight(first, "\r")) != "---" {
			return time.Time{}, nil, notesProblemNoFrontmatter
		}
		return time.Time{}, nil, notesProblemNotClosed
	}

	var fm struct {
		Written yaml.Node `yaml:"written"`
	}
	if err = yaml.Unmarshal(front, &fm); err != nil {
		return time.Time{}, nil, notesProblemBadYAML
	}
	w := fm.Written
	switch {
	case w.Kind == 0, w.Kind == yaml.ScalarNode && (w.Tag == "!!null" || strings.TrimSpace(w.Value) == ""):
		return time.Time{}, nil, notesProblemNoWritten
	case w.Kind != yaml.ScalarNode:
		return time.Time{}, nil, notesProblemBadWritten
	}
	written, err = time.ParseInLocation(notesDateLayout, w.Value, loc)
	if err != nil {
		return time.Time{}, nil, notesProblemBadWritten
	}
	return written, body, ""
}

// notesProblem is a WeekNotes carrying only a status and its fixed reason.
func notesProblem(status, problem string) WeekNotes {
	return WeekNotes{Status: status, Problem: problem}
}

// trimBlankLines drops the leading and trailing lines that are empty or
// whitespace-only and keeps everything between them byte for byte — inner
// blank lines, indentation, and trailing spaces on a kept line included.
func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// pathWithin reports whether path is root or lies under it. Both are absolute;
// an unrelatable pair (e.g. distinct volumes) is outside.
func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// rawBodyPackable reports whether a notes body has a lossless packing into
// Discord field values (ADR-0015 §4, V1).
func rawBodyPackable(body string) bool {
	_, ok := planRawChunks(body)
	return ok
}

// planRawChunks is the pure chunk planner for a verbatim raw-text section. It
// partitions body into field values of at most maxFieldValueLen UTF-16 code
// units, each holding at least one non-whitespace character, because Discord
// rejects an empty or whitespace-only field value. A chunk boundary either
// consumes one line break — the next chunk starts the next line, unmarked — or
// falls mid-line at a character boundary, and the next chunk is marked cont.
// Joining the chunks in order — a line break before an unmarked chunk, nothing
// before a continued one — reproduces body byte for byte.
//
// Split points shift so whitespace rides with a neighboring non-whitespace
// character: a greedy split that would strand a whitespace-only piece is not
// proof the body cannot be packed. The planner first finds, back to front,
// every position a valid packing of the rest can start at, then packs front to
// back: each chunk takes as many whole lines as fit, and only a line longer
// than a field is split mid-line, as late as a valid packing of the rest
// allows. ok is false when no valid packing exists at all (or body is empty,
// or not valid UTF-8, which cannot be sent byte for byte).
func planRawChunks(body string) (chunks []detailChunk, ok bool) {
	if body == "" || !utf8.ValidString(body) {
		return nil, false
	}
	t := newRawText(body)
	startable := t.startable()
	if !startable[0] {
		return nil, false
	}
	for k, cont := 0, false; ; {
		end := t.chunkEnd(k, startable)
		chunks = append(chunks, detailChunk{text: body[t.offs[k]:t.offs[end]], cont: cont})
		if end == t.n {
			return chunks, true
		}
		k, cont = t.next(end)
	}
}

// rawText is a body indexed by character for the chunk planner: each
// character's byte offset (then len(body)), UTF-16 width, and whether it is
// non-whitespace or a line break.
type rawText struct {
	n     int
	offs  []int
	width []int
	solid []bool
	isNL  []bool
}

// newRawText indexes body by character.
func newRawText(body string) rawText {
	var t rawText
	for i, r := range body {
		t.offs = append(t.offs, i)
		t.width = append(t.width, runeUnits(r))
		t.solid = append(t.solid, !unicode.IsSpace(r))
		t.isNL = append(t.isNL, r == '\n')
	}
	t.n = len(t.width)
	t.offs = append(t.offs, len(body))
	return t
}

// next is where the chunk after one ending before character e starts, and
// whether that boundary is mid-line. A boundary at a line break consumes it.
func (t rawText) next(e int) (start int, cont bool) {
	if t.isNL[e] {
		return e + 1, false
	}
	return e, true
}

// validEnds calls visit for every e such that characters [k, e) form a valid
// chunk — within the field budget and holding a non-whitespace character — in
// increasing order, until visit returns false.
func (t rawText) validEnds(k int, visit func(e int) bool) {
	units, hasSolid := 0, false
	for e := k + 1; e <= t.n; e++ {
		units += t.width[e-1]
		if units > maxFieldValueLen {
			return
		}
		hasSolid = hasSolid || t.solid[e-1]
		if hasSolid && !visit(e) {
			return
		}
	}
}

// startable reports, for each character k (and the end, which is never
// startable — a chunk is never empty), whether the text from k to the end
// packs validly with a chunk starting at k. It is filled back to front.
func (t rawText) startable() []bool {
	ok := make([]bool, t.n+1)
	for k := t.n - 1; k >= 0; k-- {
		t.validEnds(k, func(e int) bool {
			ok[k] = t.endsCleanly(e, ok)
			return !ok[k]
		})
	}
	return ok
}

// endsCleanly reports whether a chunk ending before character e leaves a rest
// that packs: nothing at all, or a startable next chunk.
func (t rawText) endsCleanly(e int, startable []bool) bool {
	if e == t.n {
		return true
	}
	s, _ := t.next(e)
	return startable[s]
}

// chunkEnd picks where the chunk starting at k ends: the last whole-line end
// that fits and leaves a packable rest, else — a line longer than a field —
// the latest mid-line end that does. startable[k] guarantees one exists.
func (t rawText) chunkEnd(k int, startable []bool) int {
	lineEnd, midEnd := -1, -1
	t.validEnds(k, func(e int) bool {
		if !t.endsCleanly(e, startable) {
			return true
		}
		if e == t.n || t.isNL[e] {
			lineEnd = e
		} else {
			midEnd = e
		}
		return true
	})
	if lineEnd >= 0 {
		return lineEnd
	}
	return midEnd
}
