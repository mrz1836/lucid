package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contactShameWords is the alphabetic half of the contact ethos deny-list
// (ADR-0014 §10), matched as whole words (an optional plural "s" included),
// case-insensitively, so "template" or "latest" never trip it. It mirrors the
// core's copy test; this literal lives only in tests.
var contactShameWords = regexp.MustCompile(`(?i)\b(overdue|late|behind|missed|failed|neglect|streak|score|forgot)s?\b`)

// contactShameHits returns every deny-list entry found in s: the whole-word
// entries, plus "%" and the multi-word phrases matched as substrings.
func contactShameHits(s string) []string {
	hits := contactShameWords.FindAllString(s, -1)
	lower := strings.ToLower(s)
	for _, p := range []string{"%", "days ago", "days past", "should have"} {
		if strings.Contains(lower, p) {
			hits = append(hits, p)
		}
	}
	return hits
}

// ctClock is 10:00 on a 2026 civil day at a fixed -04:00 offset: past the
// 04:00 rollover, so the logical day is the civil day. 2026-10-29 is a
// Thursday in ISO week 2026-W44; 2026-11-04 is a Wednesday in 2026-W45
// (Mon Nov 2 – Sun Nov 8).
func ctClock(month time.Month, day int) time.Time {
	return time.Date(2026, month, day, 10, 0, 0, 0, time.FixedZone("EDT", -4*3600))
}

// runContact runs one lucid invocation at a pinned instant.
func runContact(t *testing.T, at time.Time, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	withClock(t, at)
	return runRoot(t, BuildInfo{Version: "dev"}, args...)
}

// mustContact runs one lucid invocation at a pinned instant and requires it to
// succeed, returning stdout.
func mustContact(t *testing.T, at time.Time, args ...string) string {
	t.Helper()
	out, stderr, err := runContact(t, at, args...)
	require.NoError(t, err, "lucid %s: %s", strings.Join(args, " "), stderr)
	return out
}

// seedPeople records synthetic people, each optionally with a dob
// ("name=YYYY-MM-DD"), and returns their person keys by name.
func seedPeople(t *testing.T, specs ...string) map[string]string {
	t.Helper()
	keys := map[string]string{}
	for _, spec := range specs {
		name, dob, _ := strings.Cut(spec, "=")
		args := []string{"person", "create", name, "--json"}
		if dob != "" {
			args = append(args, "--dob", dob)
		}
		out := mustContact(t, ctClock(time.October, 1), args...)
		var view personWriteView
		require.NoError(t, json.Unmarshal([]byte(out), &view))
		keys[name] = view.PersonKey
	}
	return keys
}

// contactReceipt decodes a contact write's --json receipt.
type contactReceipt struct {
	ReceiptID   string         `json:"receipt_id"`
	LogicalDate string         `json:"logical_date"`
	PersonKey   string         `json:"person_key"`
	Event       map[string]any `json:"event"`
}

// decodeReceipt decodes a receipt and asserts its key set is exactly the
// documented one.
func decodeReceipt(t *testing.T, out string) contactReceipt {
	t.Helper()
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &keys), out)
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	slices.Sort(got)
	assert.Equal(t, []string{"event", "logical_date", "person_key", "receipt_id"}, got)
	var rc contactReceipt
	require.NoError(t, json.Unmarshal([]byte(out), &rc))
	return rc
}

// contactEventLines returns every stored contact event line under home, so a
// refusal can be proven to have written nothing.
func contactEventLines(t *testing.T, home string) []string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(filepath.Join(home, "contacts"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) != "" {
				lines = append(lines, sc.Text())
			}
		}
		return sc.Err()
	})
	require.NoError(t, err)
	return lines
}

// TestContact_CLI_CaptureReturnsReceipt: the bare form joins a multi-word
// subject, logs the contact on the current logical day, and prints the
// inventory-only ack — or, under --json, the documented receipt with the
// stored event verbatim.
func TestContact_CLI_CaptureReturnsReceipt(t *testing.T) {
	home := isolatedHome(t)
	keys := seedPeople(t, "Sam Rivera")
	oct29 := ctClock(time.October, 29)

	out := mustContact(t, oct29, "contact", "Sam", "Rivera", "--via", "call", "--note", "  coffee after the game  ")
	assert.Equal(t, "Logged contact with Sam Rivera on 2026-10-29 (contact_2026_10_29_a).\n", out)

	rc := decodeReceipt(t, mustContact(t, oct29, "contact", "Sam Rivera", "--json"))
	assert.Equal(t, "contact_2026_10_29_b", rc.ReceiptID)
	assert.Equal(t, "2026-10-29", rc.LogicalDate)
	assert.Equal(t, keys["Sam Rivera"], rc.PersonKey)
	assert.Equal(t, "contact_2026_10_29_b", rc.Event["id"])
	assert.Equal(t, "contact", rc.Event["type"])
	assert.Equal(t, keys["Sam Rivera"], rc.Event["person_key"])
	for _, field := range []string{"via", "note"} {
		require.Contains(t, rc.Event, field, "a contact always carries %s", field)
		assert.Empty(t, rc.Event[field], field)
	}
	for _, banned := range []string{"direction", "initiator", "initiated_by", "content", "score", "streak"} {
		assert.NotContains(t, rc.Event, banned)
	}

	lines := contactEventLines(t, home)
	require.Len(t, lines, 2, "one line per write")
	assert.Contains(t, lines[0], `"via":"call","note":"coffee after the game"`, "the note is trimmed")
}

// TestContact_CLI_ViaClosedSet: --via takes call, text, video or in-person;
// anything else is a usage error (exit 2) whose stderr line lists the four,
// checked before anything is booted, so nothing is written.
func TestContact_CLI_ViaClosedSet(t *testing.T) {
	home := isolatedHome(t)
	seedPeople(t, "Sam Rivera")
	oct29 := ctClock(time.October, 29)

	stdout, stderr, err := runContact(t, oct29, "contact", "Sam Rivera", "--via", "email")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Empty(t, stdout)
	assertUsageErrorOnce(t, stderr, "lucid contact")
	assert.Contains(t, stderr, "lucid contact: --via is one of call, text, video or in-person. Nothing was saved.\n")
	assert.Empty(t, contactEventLines(t, home))
	assert.NoDirExists(t, filepath.Join(home, "contacts"), "the refusal is decided before any boot")

	for _, via := range []string{"call", "text", "video", "in-person"} {
		rc := decodeReceipt(t, mustContact(t, oct29, "contact", "Sam Rivera", "--via", via, "--json"))
		assert.Equal(t, via, rc.Event["via"])
	}
	assert.Len(t, contactEventLines(t, home), 4)
}

// TestContact_CLI_DayBackdateAndFutureRefused: --day backdates on the strict
// tier — @yesterday files under the previous logical day with its own slot —
// and a future or unreadable day is refused on stderr with nothing written.
func TestContact_CLI_DayBackdateAndFutureRefused(t *testing.T) {
	home := isolatedHome(t)
	seedPeople(t, "Sam Rivera")
	oct29 := ctClock(time.October, 29)

	rc := decodeReceipt(t, mustContact(t, oct29, "contact", "Sam Rivera", "--day", "@yesterday", "--json"))
	assert.Equal(t, "contact_2026_10_28_a", rc.ReceiptID)
	assert.Equal(t, "2026-10-28", rc.LogicalDate)
	assert.FileExists(t, filepath.Join(home, "contacts", "2026", "10", "contact_2026_10_28.jsonl"))
	assert.Contains(t, rc.Event["recorded_at"], "2026-10-29T10:00:00", "recorded_at is the real write time")

	before := contactEventLines(t, home)
	for _, day := range []string{"2026-11-05", "@tomorrow", "someday-ish"} {
		stdout, stderr, err := runContact(t, oct29, "contact", "Sam Rivera", "--day", day)
		require.Error(t, err, day)
		assert.Equal(t, ExitErr, exitCodeForError(err), day)
		assert.Empty(t, stdout, day)
		assert.Contains(t, strings.ToLower(stderr), "nothing was saved", day)
		assert.Equal(t, before, contactEventLines(t, home), "%s writes nothing", day)
	}
	_, stderr, _ := runContact(t, oct29, "contact", "Sam Rivera", "--day", "2026-11-05")
	assert.Contains(t, stderr, "has not happened yet")
}

// TestContact_CLI_EveryParsesTrailingWindow: `every` takes the last argument
// as the window and joins the rest into the subject; a window outside the
// Nd / Nw grammar is a usage error that carries the grammar, with nothing
// written.
func TestContact_CLI_EveryParsesTrailingWindow(t *testing.T) {
	home := isolatedHome(t)
	keys := seedPeople(t, "Jordan Lee")
	oct29 := ctClock(time.October, 29)

	rc := decodeReceipt(t, mustContact(t, oct29, "contact", "every", "Jordan", "Lee", "3w", "--json"))
	assert.Equal(t, keys["Jordan Lee"], rc.PersonKey)
	assert.Equal(t, "cadence", rc.Event["type"])
	assert.Equal(t, "set", rc.Event["action"])
	assert.Equal(t, "3w", rc.Event["window"])
	assert.InDelta(t, 21, rc.Event["window_days"], 0)

	out := mustContact(t, oct29, "contact", "every", "Jordan Lee", "28d")
	assert.Equal(t, "Jordan Lee: every 28 days — next due by Nov 26 (contact_2026_10_29_b).\n", out,
		"changing the window keeps the anchor")

	before := contactEventLines(t, home)
	for _, bad := range []string{"three", "0d", "1.5w", "4"} {
		stdout, stderr, err := runContact(t, oct29, "contact", "every", "Jordan", "Lee", bad)
		require.Error(t, err, bad)
		assert.Equal(t, ExitUsage, exitCodeForError(err), bad)
		assert.Empty(t, stdout, bad)
		assertUsageErrorOnce(t, stderr, "lucid contact every")
		assert.Contains(t, stderr, "a window is Nd or Nw — a whole number ≥ 1 of days or weeks, e.g. 28d or 4w. Nothing was saved.", bad)
	}
	// A negative window reads as a flag, a usage error before the grammar is
	// reached; after "--" it is a positional, and the grammar refuses it.
	_, stderr, err := runContact(t, oct29, "contact", "every", "Jordan", "Lee", "-2w")
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assertUsageErrorOnce(t, stderr, "lucid contact every")
	_, stderr, err = runContact(t, oct29, "contact", "every", "Jordan", "Lee", "--", "-2w")
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Contains(t, stderr, "a window is Nd or Nw")
	_, _, err = runContact(t, oct29, "contact", "every", "Jordan")
	assert.Equal(t, ExitUsage, exitCodeForError(err), "a subject and a window are both required")
	assert.Equal(t, before, contactEventLines(t, home))
}

// TestContact_CLI_LifecycleVerbs: pause, resume and retire each append one
// cadence event with a receipt; snooze and void round-trip; a transition from
// the wrong state is refused on stderr, non-zero, with nothing written.
func TestContact_CLI_LifecycleVerbs(t *testing.T) {
	home := isolatedHome(t)
	seedPeople(t, "Sam Rivera")
	oct29 := ctClock(time.October, 29)

	mustContact(t, oct29, "contact", "every", "Sam Rivera", "4w")
	snooze := decodeReceipt(t, mustContact(t, oct29, "contact", "snooze", "Sam", "Rivera", "--json"))
	assert.Equal(t, "snooze", snooze.Event["type"])

	_, stderr, err := runContact(t, oct29, "contact", "snooze", "Sam Rivera")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Equal(t, "Sam Rivera is already snoozed this week ("+snooze.ReceiptID+"). Nothing was saved.\n", stderr)

	void := decodeReceipt(t, mustContact(t, oct29, "contact", "void", snooze.ReceiptID, "--json"))
	assert.Equal(t, "void", void.Event["type"])
	assert.Equal(t, map[string]any{"voids": snooze.ReceiptID}, void.Event["refs"])
	mustContact(t, oct29, "contact", "snooze", "Sam Rivera") // the voided snooze no longer counts

	assert.Equal(t, "Sam Rivera: check-ins paused (contact_2026_10_29_e).\n",
		mustContact(t, oct29, "contact", "pause", "Sam Rivera"))
	assert.Equal(t, "Sam Rivera: check-ins resumed, every 4 weeks — next due by Nov 26 (contact_2026_10_29_f).\n",
		mustContact(t, oct29, "contact", "resume", "Sam Rivera"))
	retire := decodeReceipt(t, mustContact(t, oct29, "contact", "retire", "Sam Rivera", "--json"))
	assert.Equal(t, "retire", retire.Event["action"])
	assert.NotContains(t, retire.Event, "window", "only a set carries a window")

	before := contactEventLines(t, home)
	for _, tc := range []struct {
		args   []string
		stderr string
	}{
		{[]string{"contact", "pause", "Sam Rivera"}, "Sam Rivera's check-ins are retired — nothing to pause. Nothing was saved.\n"},
		{[]string{"contact", "resume", "Sam Rivera"}, "Sam Rivera's check-ins are retired — nothing to resume. Nothing was saved.\n"},
		{[]string{"contact", "retire", "Sam Rivera"}, "Sam Rivera has no check-in cadence to retire. Nothing was saved.\n"},
		{[]string{"contact", "void", retire.ReceiptID}, "a cadence event is never voided — change it with lucid contact every, pause, resume or retire. Nothing was saved.\n"},
		{[]string{"contact", "void", "contact_2026_10_29_z"}, `no contact event "contact_2026_10_29_z". Nothing was saved.` + "\n"},
	} {
		stdout, stderr, refused := runContact(t, oct29, tc.args...)
		require.Error(t, refused, tc.args)
		assert.Equal(t, ExitErr, exitCodeForError(refused), tc.args)
		assert.Empty(t, stdout, tc.args)
		assert.Equal(t, tc.stderr, stderr, tc.args)
	}
	assert.Equal(t, before, contactEventLines(t, home), "every refusal writes nothing")

	_, _, err = runContact(t, oct29, "contact", "void")
	assert.Equal(t, ExitUsage, exitCodeForError(err), "void needs exactly one id")
	_, _, err = runContact(t, oct29, "contact", "pause")
	assert.Equal(t, ExitUsage, exitCodeForError(err), "pause needs a subject")
}

// seedDueWeek writes the usage doc's week, read on Wednesday 2026-11-04: Sam
// Rivera due this week (4w set Oct 8, closes Nov 5), Alex Kim still open (2w
// set Oct 14, closed Oct 28), Jordan Lee coming up (3w, last caught up Oct 20,
// closes Nov 10) with a birthday on Wed Nov 4.
func seedDueWeek(t *testing.T) map[string]string {
	t.Helper()
	keys := seedPeople(t, "Sam Rivera", "Alex Kim", "Jordan Lee=1990-11-04")
	mustContact(t, ctClock(time.October, 8), "contact", "every", "Sam Rivera", "4w")
	mustContact(t, ctClock(time.October, 14), "contact", "every", "Alex Kim", "2w")
	mustContact(t, ctClock(time.October, 20), "contact", "Jordan Lee", "--via", "text")
	mustContact(t, ctClock(time.October, 20), "contact", "every", "Jordan Lee", "3w")
	return keys
}

// TestContact_CLI_DueHumanAndJSON: `due` prints the fixed human view — the
// week, the three sections and the birthdays — and the documented,
// byte-deterministic JSON under --json.
func TestContact_CLI_DueHumanAndJSON(t *testing.T) {
	isolatedHome(t)
	keys := seedDueWeek(t)
	nov4 := ctClock(time.November, 4)

	assert.Equal(t, `Week of Nov 2 – Nov 8

Due this week
- Sam Rivera (every 4 weeks), not logged yet

Still open from before
- Alex Kim (every 2 weeks), not logged yet

Coming up next week
- Jordan Lee (every 3 weeks), last caught up Oct 20

Birthdays this week
- Jordan Lee — Wed Nov 4
`, mustContact(t, nov4, "contact", "due"))

	out := mustContact(t, nov4, "contact", "due", "--json")
	assert.Equal(t, out, mustContact(t, nov4, "contact", "due", "--json"), "byte-deterministic")
	var view struct {
		Day         string `json:"day"`
		Week        string `json:"week"`
		WeekStart   string `json:"week_start"`
		WeekEnd     string `json:"week_end"`
		Wrap        bool   `json:"wrap"`
		DueThisWeek []struct {
			PersonKey    string  `json:"person_key"`
			DisplayName  string  `json:"display_name"`
			Cadence      string  `json:"cadence"`
			CadenceDays  int     `json:"cadence_days"`
			LastContact  *string `json:"last_contact"`
			WindowCloses string  `json:"window_closes"`
		} `json:"due_this_week"`
		StillOpen []map[string]any `json:"still_open"`
		ComingUp  []map[string]any `json:"coming_up"`
		Birthdays []map[string]any `json:"birthdays"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "2026-11-04", view.Day)
	assert.Equal(t, "2026-W45", view.Week)
	assert.Equal(t, "2026-11-02", view.WeekStart)
	assert.Equal(t, "2026-11-08", view.WeekEnd)
	assert.False(t, view.Wrap)
	require.Len(t, view.DueThisWeek, 1)
	assert.Equal(t, keys["Sam Rivera"], view.DueThisWeek[0].PersonKey)
	assert.Equal(t, "4w", view.DueThisWeek[0].Cadence)
	assert.Equal(t, 28, view.DueThisWeek[0].CadenceDays)
	assert.Nil(t, view.DueThisWeek[0].LastContact)
	assert.Equal(t, "2026-11-05", view.DueThisWeek[0].WindowCloses)
	require.Len(t, view.StillOpen, 1)
	assert.Equal(t, keys["Alex Kim"], view.StillOpen[0]["person_key"])
	require.Len(t, view.ComingUp, 1)
	assert.Equal(t, "2026-10-20", view.ComingUp[0]["last_contact"])
	require.Len(t, view.Birthdays, 1)
	assert.Equal(t, map[string]any{"person_key": keys["Jordan Lee"], "display_name": "Jordan Lee", "date": "2026-11-04"},
		view.Birthdays[0])

	_, _, err := runContact(t, nov4, "contact", "due", "extra")
	assert.Equal(t, ExitUsage, exitCodeForError(err), "due takes no subject")
}

// TestContact_CLI_DueWrapTwoListsJSON: `due --wrap` is the Sunday wrap — only
// the two lists, computed fresh — and its JSON carries exactly the documented
// keys with wrap true: no coming_up, no birthdays.
func TestContact_CLI_DueWrapTwoListsJSON(t *testing.T) {
	isolatedHome(t)
	seedDueWeek(t)
	nov8 := ctClock(time.November, 8) // Sunday

	out := mustContact(t, nov8, "contact", "due", "--wrap", "--json")
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	got := make([]string, 0, len(raw))
	for k := range raw {
		got = append(got, k)
	}
	slices.Sort(got)
	assert.Equal(t, []string{"day", "due_this_week", "still_open", "week", "week_end", "week_start", "wrap"}, got)
	assert.JSONEq(t, "true", string(raw["wrap"]))
	assert.Contains(t, string(raw["due_this_week"]), "Sam Rivera")
	assert.Contains(t, string(raw["still_open"]), "Alex Kim")
	assert.NotContains(t, out, "Jordan Lee", "coming up and birthdays never appear in the wrap")

	assert.Equal(t, `Still open this week — no pressure; anyone here rolls into Monday's list.

Due this week
- Sam Rivera (every 4 weeks), not logged yet

Still open from before
- Alex Kim (every 2 weeks), not logged yet
`, mustContact(t, nov8, "contact", "due", "--wrap"))

	// Computed fresh: a contact logged now drops Sam from the wrap at once.
	mustContact(t, nov8, "contact", "Sam Rivera")
	assert.NotContains(t, mustContact(t, nov8, "contact", "due", "--wrap"), "Sam Rivera")
}

// TestContact_CLI_UnknownSubjectExitsNonZero: a subject that matches nobody is
// refused on stderr with the person create pointer, exits non-zero, writes no
// event and never creates a person.
func TestContact_CLI_UnknownSubjectExitsNonZero(t *testing.T) {
	home := isolatedHome(t)
	seedPeople(t, "Sam Rivera")
	oct29 := ctClock(time.October, 29)

	for _, args := range [][]string{
		{"contact", "Riley", "Moss"},
		{"contact", "every", "Riley Moss", "4w"},
		{"contact", "snooze", "Riley Moss"},
		{"contact", "pause", "Riley Moss"},
	} {
		stdout, stderr, err := runContact(t, oct29, args...)
		require.Error(t, err, args)
		assert.Equal(t, ExitErr, exitCodeForError(err), args)
		assert.Empty(t, stdout, args)
		assert.Equal(t, `no one matches "Riley Moss" — add them first with lucid person create <name>; nothing was saved.`+"\n",
			stderr, args)
	}
	assert.Empty(t, contactEventLines(t, home))

	out := mustContact(t, oct29, "person", "Riley Moss", "--json")
	var view personView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.False(t, view.Matched, "logging contact never creates a person")
}

// TestContact_CLI_PersonViewCarriesCheckIns: `lucid person` shows the check-in
// line and, under --json, the contact object — and a person with no contact
// events projects exactly as before, with no contact key at all.
func TestContact_CLI_PersonViewCarriesCheckIns(t *testing.T) {
	isolatedHome(t)
	seedPeople(t, "Sam Rivera", "Alex Kim")
	oct29 := ctClock(time.October, 29)
	before := mustContact(t, oct29, "person", "Alex Kim", "--json")

	mustContact(t, ctClock(time.September, 1), "contact", "every", "Sam Rivera", "4w")
	mustContact(t, oct29, "contact", "Sam Rivera", "--day", "2026-09-28")

	assert.Contains(t, mustContact(t, oct29, "person", "Sam Rivera"),
		"\nCheck-ins: every 4 weeks — last caught up 2026-09-28\n")
	var view struct {
		Contact map[string]any `json:"contact"`
	}
	require.NoError(t, json.Unmarshal([]byte(mustContact(t, oct29, "person", "Sam Rivera", "--json")), &view))
	assert.Equal(t, map[string]any{
		"state": "active", "cadence": "4w", "cadence_days": float64(28), "last_contact": "2026-09-28",
	}, view.Contact)

	after := mustContact(t, oct29, "person", "Alex Kim", "--json")
	assert.Equal(t, before, after, "a person with no contact events projects as before")
	assert.NotContains(t, after, `"contact"`)

	mustContact(t, oct29, "contact", "Alex Kim")
	view.Contact = nil
	require.NoError(t, json.Unmarshal([]byte(mustContact(t, oct29, "person", "Alex Kim", "--json")), &view))
	assert.Equal(t, map[string]any{"state": "none", "last_contact": "2026-10-29"}, view.Contact,
		"no window was ever set, so cadence and cadence_days are left out")
}

// TestContactCmd_HelpHasNoShameLanguage: the group's and every subcommand's
// Short, Long, Example and flag usage pass the ethos deny-list — "due" is the
// only status word, and nothing counts days or scores anyone.
func TestContactCmd_HelpHasNoShameLanguage(t *testing.T) {
	group := newContactCmd()
	cmds := append([]*cobra.Command{group}, group.Commands()...)
	require.Len(t, cmds, 8, "the group and its seven subcommands")
	for _, c := range cmds {
		surfaces := []string{c.Short, c.Long, c.Example, c.LocalFlags().FlagUsages()}
		require.NotEmpty(t, c.Short, c.Name())
		for _, s := range surfaces {
			assert.Empty(t, contactShameHits(s), "%s: shame language in help: %q", c.CommandPath(), s)
			assert.NotContains(t, strings.ToLower(s), "overdue", c.CommandPath())
		}
	}
	assert.Equal(t, "Keep in touch — per-person check-in cadences, a who's-due view, and contact capture", group.Short)
}
