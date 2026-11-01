package witnessreport

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notesNow is the fixed Monday-morning instant the notes tests read at. Its
// report window is 2027-02-02 → 2027-02-08 (invented dates).
func notesNow() time.Time { return time.Date(2027, 2, 8, 9, 0, 0, 0, time.UTC) }

// synthNotes is a synthetic notes body: an anonymous user's week, no names.
const synthNotes = "A heavy stretch at work; he still closed out every night. " +
	"A friend's call on Thursday helped. Ask him how the new routine is landing."

// notesFile is a week-notes file with the given written value and body.
func notesFile(written, body string) string {
	return "---\nwritten: " + written + "\n---\n" + body
}

// TestParseWeekNotes is the parser table (ADR-0015 §2, §4, §7): the frontmatter
// rules, the body trim, the cap, and lossless packability. Every result that is
// not included carries no body.
func TestParseWeekNotes(t *testing.T) {
	atCap := strings.Repeat("é", 1000) + strings.Repeat("🙂", 1000) + strings.Repeat("a", 2000)
	require.Equal(t, maxWeekNotesRunes, utf8.RuneCountInString(atCap))
	require.Greater(t, discordLen(atCap), maxWeekNotesRunes, "the cap counts code points, not UTF-16 units")
	unpackable := "a" + strings.Repeat(" ", 3998) + "b"
	shifted := strings.Repeat("x", 100) + "a" + strings.Repeat(" ", 2046) + "b"

	included := func(body string) WeekNotes {
		return WeekNotes{Status: NotesIncluded, Body: body, Written: "2027-02-07"}
	}
	invalid := func(problem string) WeekNotes {
		return WeekNotes{Status: NotesInvalid, Problem: problem}
	}
	invalidWritten := func(problem string) WeekNotes {
		return WeekNotes{Status: NotesInvalid, Problem: problem, Written: "2027-02-07"}
	}

	cases := []struct {
		name    string
		content string
		want    WeekNotes
	}{
		{"valid", notesFile("2027-02-07", synthNotes+"\n"), included(synthNotes)},
		{"bom", "\xef\xbb\xbf" + notesFile("2027-02-07", synthNotes+"\n"), included(synthNotes)},
		{
			"crlf",
			strings.ReplaceAll(notesFile("2027-02-07", "Line one.\n\nLine two.\n"), "\n", "\r\n"),
			included("Line one.\n\nLine two."),
		},
		{"no_frontmatter", synthNotes + "\n", invalid(notesProblemNoFrontmatter)},
		{"leading_space_before_fence", " ---\nwritten: 2027-02-07\n---\nBody.\n", invalid(notesProblemNoFrontmatter)},
		{"empty_file", "", invalid(notesProblemNoFrontmatter)},
		{"unclosed_frontmatter", "---\nwritten: 2027-02-07\n" + synthNotes + "\n", invalid(notesProblemNotClosed)},
		{"bad_yaml", "---\nwritten: [2027-02-07\n---\nBody.\n", invalid(notesProblemBadYAML)},
		{"frontmatter_not_mapping", "---\njust a line\n---\nBody.\n", invalid(notesProblemBadYAML)},
		{"missing_written", "---\nby: a harness\n---\n" + synthNotes + "\n", invalid(notesProblemNoWritten)},
		{"null_written", "---\nwritten:\n---\nBody.\n", invalid(notesProblemNoWritten)},
		{"bad_date", notesFile("2027-2-7", synthNotes), invalid(notesProblemBadWritten)},
		{"date_with_time", notesFile("2027-02-07T08:00:00Z", synthNotes), invalid(notesProblemBadWritten)},
		{"non_scalar_written", "---\nwritten:\n  day: 7\n---\nBody.\n", invalid(notesProblemBadWritten)},
		{"quoted_date", notesFile(`"2027-02-07"`, synthNotes), included(synthNotes)},
		{
			"extra_keys_ignored",
			"---\nwritten: 2027-02-07\nby: a harness\ntags: [weekly]\n---\n" + synthNotes + "\n",
			included(synthNotes),
		},
		{
			"body_trimmed_inner_kept",
			notesFile("2027-02-07", "\n \n\t\n  Indented first line.\n\n\nSecond  paragraph,\tspaced.  \n\n   \n"),
			included("  Indented first line.\n\n\nSecond  paragraph,\tspaced.  "),
		},
		{"empty_body", notesFile("2027-02-07", "\n  \n\t\n"), WeekNotes{Status: NotesEmpty, Written: "2027-02-07"}},
		{"no_body", "---\nwritten: 2027-02-07\n---", WeekNotes{Status: NotesEmpty, Written: "2027-02-07"}},
		{"at_cap", notesFile("2027-02-07", atCap+"\n"), included(atCap)},
		{"cap_plus_one", notesFile("2027-02-07", atCap+"b\n"), invalidWritten(notesProblemOverCap)},
		{"unpackable_body", notesFile("2027-02-07", unpackable+"\n"), invalidWritten(notesProblemUnpackable)},
		{"packable_whitespace_boundary", notesFile("2027-02-07", shifted+"\n"), included(shifted)},
		{"invalid_utf8", notesFile("2027-02-07", "Steady week \xff\n"), invalidWritten(notesProblemUnpackable)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseWeekNotes([]byte(tc.content), notesNow())
			assert.Equal(t, tc.want, got)
			if got.Status != NotesIncluded {
				assert.Empty(t, got.Body, "an unusable body is never returned")
			}
		})
	}

	t.Run("cap_phrase_names_the_cap", func(t *testing.T) {
		assert.Equal(t, 4000, maxWeekNotesRunes)
		assert.Contains(t, notesProblemOverCap, "4,000")
	})
}

// TestWeekNotesFreshness pins the freshness window (ADR-0015 §3): written must
// fall in [ref−6, ref] in calendar days of the report's location — the same
// window as the card's "This week" — and a future date is invalid.
func TestWeekNotesFreshness(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	check := func(t *testing.T, now time.Time, written, status, problem string) {
		t.Helper()
		got := parseWeekNotes([]byte(notesFile(written, synthNotes)), now)
		assert.Equal(t, status, got.Status, "written %s at %s", written, now)
		assert.Equal(t, problem, got.Problem, "written %s at %s", written, now)
		assert.Equal(t, written, got.Written, "written parsed and kept for every status")
		if status == NotesIncluded {
			assert.Equal(t, synthNotes, got.Body)
		} else {
			assert.Empty(t, got.Body)
		}
	}

	t.Run("ref_minus_6_included", func(t *testing.T) {
		check(t, notesNow(), "2027-02-02", NotesIncluded, "")
	})
	t.Run("ref_included", func(t *testing.T) {
		check(t, notesNow(), "2027-02-08", NotesIncluded, "")
		// A late-evening fire still reads its own calendar day.
		check(t, time.Date(2027, 2, 8, 23, 59, 0, 0, time.UTC), "2027-02-08", NotesIncluded, "")
	})
	t.Run("ref_minus_7_stale", func(t *testing.T) {
		check(t, notesNow(), "2027-02-01", NotesStale, "")
		// The next Monday's fire no longer uses last Sunday's notes.
		check(t, time.Date(2027, 2, 15, 9, 0, 0, 0, time.UTC), "2027-02-07", NotesStale, "")
	})
	t.Run("ref_plus_1_invalid", func(t *testing.T) {
		check(t, notesNow(), "2027-02-09", NotesInvalid, notesProblemFuture)
	})
	t.Run("dst_calendar_dates", func(t *testing.T) {
		// Fall back (Sunday 2027-11-07): the window 2027-11-02 → 2027-11-08 is
		// 7 calendar days but 145 hours, so a 6×24h duration would drop its
		// first day.
		fallBack := time.Date(2027, 11, 8, 9, 0, 0, 0, ny)
		check(t, fallBack, "2027-11-02", NotesIncluded, "")
		check(t, fallBack, "2027-11-01", NotesStale, "")
		check(t, fallBack, "2027-11-08", NotesIncluded, "")
		check(t, fallBack, "2027-11-09", NotesInvalid, notesProblemFuture)
		// Spring forward (Sunday 2027-03-14): the window 2027-03-09 → 2027-03-15.
		springForward := time.Date(2027, 3, 15, 0, 30, 0, 0, ny)
		check(t, springForward, "2027-03-09", NotesIncluded, "")
		check(t, springForward, "2027-03-08", NotesStale, "")
		check(t, springForward, "2027-03-15", NotesIncluded, "")
		check(t, springForward, "2027-03-16", NotesInvalid, notesProblemFuture)
		// The calendar day is the report location's, not UTC's: late on
		// Sunday evening in New York is already Monday in UTC.
		sundayNight := time.Date(2027, 2, 7, 22, 0, 0, 0, ny)
		check(t, sundayNight, "2027-02-08", NotesInvalid, notesProblemFuture)
		check(t, sundayNight, "2027-02-01", NotesIncluded, "")
	})
	t.Run("empty_stale_is_stale", func(t *testing.T) {
		// empty means valid AND fresh; an old blank file is simply stale, and a
		// future-dated blank file is still invalid.
		got := parseWeekNotes([]byte(notesFile("2027-01-25", "\n")), notesNow())
		assert.Equal(t, NotesStale, got.Status)
		got = parseWeekNotes([]byte(notesFile("2027-02-10", "\n")), notesNow())
		assert.Equal(t, NotesInvalid, got.Status)
		assert.Equal(t, notesProblemFuture, got.Problem)
	})
}

// recordingSeam wraps the real file system and records every path the notes
// read touches, so a test proves exactly what was stat'ed, resolved and read.
type recordingSeam struct {
	reads, stats, evals []string
	statErr, evalErr    error
	readErr             error
}

func (r *recordingSeam) seam() notesSeam {
	osSeam := osNotesSeam()
	return notesSeam{
		readFile: func(name string) ([]byte, error) {
			r.reads = append(r.reads, name)
			if r.readErr != nil {
				return nil, r.readErr
			}
			return osSeam.readFile(name)
		},
		stat: func(name string) (fs.FileInfo, error) {
			r.stats = append(r.stats, name)
			if r.statErr != nil {
				return nil, r.statErr
			}
			return osSeam.stat(name)
		},
		evalSymlinks: func(path string) (string, error) {
			r.evals = append(r.evals, path)
			if r.evalErr != nil {
				return "", r.evalErr
			}
			return osSeam.evalSymlinks(path)
		},
	}
}

// touched is every path the read handed to the file system.
func (r *recordingSeam) touched() []string {
	all := append(append(append([]string{}, r.reads...), r.stats...), r.evals...)
	return all
}

// TestReadWeekNotes covers the reader (ADR-0015 §1, §4, §12): it opens exactly
// the configured path, maps every file-system problem to a status, and refuses
// a path that resolves into the Lucid home without reading its bytes.
func TestReadWeekNotes(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	prompts := filepath.Join(root, "prompts")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.MkdirAll(prompts, 0o700))
	write := func(t *testing.T, path, content string) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	fresh := notesFile("2027-02-07", synthNotes+"\n")
	notesPath := filepath.Join(prompts, "witness-week-notes.md")
	write(t, notesPath, fresh)

	read := func(path, home string) (WeekNotes, *recordingSeam) {
		rec := &recordingSeam{}
		return readWeekNotes(path, home, notesNow(), rec.seam()), rec
	}

	t.Run("unset_off", func(t *testing.T) {
		for _, path := range []string{"", "   "} {
			got, rec := read(path, home)
			assert.Equal(t, WeekNotes{}, got, "no notes_file is off")
			assert.Empty(t, rec.touched(), "off touches nothing")
		}
	})

	t.Run("missing_unreadable", func(t *testing.T) {
		got, rec := read(filepath.Join(prompts, "absent.md"), home)
		assert.Equal(t, notesProblem(NotesUnreadable, notesProblemMissing), got)
		assert.Empty(t, rec.reads)
	})

	t.Run("directory_unreadable", func(t *testing.T) {
		got, rec := read(prompts, home)
		assert.Equal(t, notesProblem(NotesUnreadable, notesProblemDirectory), got)
		assert.Empty(t, rec.reads)
	})

	t.Run("unreadable_file", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads a mode-000 file")
		}
		locked := filepath.Join(prompts, "locked.md")
		write(t, locked, fresh)
		require.NoError(t, os.Chmod(locked, 0o000))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })

		got, rec := read(locked, home)
		assert.Equal(t, notesProblem(NotesUnreadable, notesProblemCannotRead), got)
		assert.Equal(t, []string{locked}, rec.reads)
	})

	t.Run("never_reads_sibling", func(t *testing.T) {
		sibling := filepath.Join(prompts, "other-week-notes.md")
		write(t, sibling, notesFile("2027-02-06", "A different, also valid file.\n"))
		absHome, err := filepath.Abs(home)
		require.NoError(t, err)

		got, rec := read(notesPath, home)
		assert.Equal(t, WeekNotes{Status: NotesIncluded, Body: synthNotes, Written: "2027-02-07"}, got)
		assert.Equal(t, []string{notesPath}, rec.reads, "exactly the configured path is read")
		for _, p := range rec.touched() {
			assert.NotEqual(t, sibling, p, "a sibling file is never touched")
			assert.Contains(t, []string{notesPath, absHome}, p, "only the configured path and the home are touched")
		}
	})

	t.Run("inside_home_invalid", func(t *testing.T) {
		inHome := filepath.Join(home, "notes.md")
		write(t, inHome, fresh)
		for _, path := range []string{inHome, home, filepath.Join(prompts, "..", "home", "notes.md")} {
			got, rec := read(path, home)
			assert.Equal(t, notesProblem(NotesInvalid, notesProblemInsideHome), got, "path %s", path)
			assert.Empty(t, rec.reads, "a file inside the home is never read")
			assert.Empty(t, rec.stats, "the lexical refusal runs before any stat")
		}
	})

	t.Run("symlink_into_home_invalid", func(t *testing.T) {
		target := filepath.Join(home, "real.md")
		write(t, target, fresh)
		link := filepath.Join(prompts, "link.md")
		require.NoError(t, os.Symlink(target, link))
		homeLink := filepath.Join(prompts, "home-link")
		require.NoError(t, os.Symlink(home, homeLink))

		for _, path := range []string{link, filepath.Join(homeLink, "real.md")} {
			got, rec := read(path, home)
			assert.Equal(t, notesProblem(NotesInvalid, notesProblemInsideHome), got, "path %s", path)
			assert.Empty(t, rec.reads, "a symlink into the home is refused unread")
		}

		// A symlink that points outside the home is an ordinary file.
		outLink := filepath.Join(prompts, "out-link.md")
		require.NoError(t, os.Symlink(notesPath, outLink))
		got, _ := read(outLink, home)
		assert.Equal(t, NotesIncluded, got.Status)
	})

	t.Run("empty_home_invalid", func(t *testing.T) {
		for _, h := range []string{"", "  "} {
			got, rec := read(notesPath, h)
			assert.Equal(t, notesProblem(NotesInvalid, notesProblemHomeUnset), got)
			assert.Empty(t, rec.touched(), "an unset home fails closed before any file access")
		}
	})

	t.Run("seam_errors", func(t *testing.T) {
		boom := errors.New("synthetic failure")
		rec := &recordingSeam{statErr: boom}
		got := readWeekNotes(notesPath, home, notesNow(), rec.seam())
		assert.Equal(t, notesProblem(NotesUnreadable, notesProblemCannotRead), got, "a stat failure")

		rec = &recordingSeam{evalErr: boom}
		got = readWeekNotes(notesPath, home, notesNow(), rec.seam())
		assert.Equal(t, notesProblem(NotesUnreadable, notesProblemCannotRead), got, "a resolve failure")
		assert.Empty(t, rec.reads)

		rec = &recordingSeam{readErr: fs.ErrNotExist}
		got = readWeekNotes(notesPath, home, notesNow(), rec.seam())
		assert.Equal(t, notesProblem(NotesUnreadable, notesProblemMissing), got, "removed between stat and read")
	})

	t.Run("home_not_resolvable_still_checked", func(t *testing.T) {
		// A home that does not exist cannot be resolved; the lexical check
		// against it still refuses a path under it.
		ghostHome := filepath.Join(root, "ghost-home")
		got, rec := read(filepath.Join(ghostHome, "notes.md"), ghostHome)
		assert.Equal(t, notesProblem(NotesInvalid, notesProblemInsideHome), got)
		assert.Empty(t, rec.reads)

		got, _ = read(notesPath, ghostHome)
		assert.Equal(t, NotesIncluded, got.Status)
	})
}

// joinChunks reassembles planned chunks: a line break before an unmarked
// chunk, nothing before a continued one.
func joinChunks(chunks []detailChunk) string {
	var b strings.Builder
	for i, c := range chunks {
		if i > 0 && !c.cont {
			b.WriteByte('\n')
		}
		b.WriteString(c.text)
	}
	return b.String()
}

// TestPlanRawChunks pins the pure chunk planner the reader validates with
// (ADR-0015 §4, V1): every chunk is within the field budget and holds a
// non-whitespace character, whole lines share a chunk while they fit,
// boundaries shift so no chunk is whitespace-only, and the chunks reassemble
// to the body byte for byte.
func TestPlanRawChunks(t *testing.T) {
	long := strings.Repeat("word ", 300) // 1,500 units on one line
	bodies := map[string]string{
		"short":                 synthNotes,
		"paragraphs":            "First paragraph.\n\nSecond paragraph.\n\n\nThird, after two blank lines.",
		"long_line":             "Opening line.\n" + strings.TrimSpace(long) + "\nClosing line.",
		"emoji":                 strings.Repeat("🙂", 1500),
		"blank_lines_at_border": strings.Repeat("x", 1020) + "\n\n\n\n" + strings.Repeat("y", 10),
		"shifted_boundary":      strings.Repeat("x", 100) + "a" + strings.Repeat(" ", 2046) + "b",
		"indented_lines":        "  one\n    two\n\tthree",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			chunks, ok := planRawChunks(body)
			require.True(t, ok)
			require.NotEmpty(t, chunks)
			assert.False(t, chunks[0].cont, "the first chunk never continues anything")
			for i, c := range chunks {
				assert.LessOrEqualf(t, discordLen(c.text), maxFieldValueLen, "chunk %d over the field budget", i)
				assert.NotEmptyf(t, strings.TrimSpace(c.text), "chunk %d is whitespace-only", i)
			}
			assert.Equal(t, body, joinChunks(chunks), "reassembly is lossless")
		})
	}

	t.Run("whole_lines_share_a_chunk", func(t *testing.T) {
		chunks, ok := planRawChunks("one\ntwo\n\nthree")
		require.True(t, ok)
		assert.Equal(t, []detailChunk{{text: "one\ntwo\n\nthree"}}, chunks)
	})

	t.Run("prefers_a_line_break_over_a_mid_line_split", func(t *testing.T) {
		body := "short line\n" + strings.Repeat("z", 1500)
		chunks, ok := planRawChunks(body)
		require.True(t, ok)
		require.Len(t, chunks, 3)
		assert.Equal(t, detailChunk{text: "short line"}, chunks[0])
		assert.False(t, chunks[1].cont, "the long line starts its own unmarked chunk")
		assert.True(t, chunks[2].cont, "its overflow continues mid-line")
	})

	t.Run("shifted_boundary_layout", func(t *testing.T) {
		body := strings.Repeat("x", 100) + "a" + strings.Repeat(" ", 2046) + "b"
		chunks, ok := planRawChunks(body)
		require.True(t, ok)
		assert.Equal(t, []detailChunk{
			{text: strings.Repeat("x", 100)},
			{text: "a" + strings.Repeat(" ", 1023), cont: true},
			{text: strings.Repeat(" ", 1023) + "b", cont: true},
		}, chunks, "a greedy split would strand a whitespace-only field; the planner shifts it")
	})

	t.Run("no_valid_packing", func(t *testing.T) {
		for name, body := range map[string]string{
			"too_much_whitespace": "a" + strings.Repeat(" ", 3998) + "b",
			"empty":               "",
			"whitespace_only":     " \n\t ",
			"invalid_utf8":        "ok \xff",
			"only_line_breaks":    "\n\n\n",
		} {
			chunks, ok := planRawChunks(body)
			assert.Falsef(t, ok, "%s must not pack", name)
			assert.Emptyf(t, chunks, "%s returns no chunks", name)
		}
	})
}
