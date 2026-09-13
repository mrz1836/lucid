package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeVerbJSONCase pins one write verb's --json contract: setup prepares an
// isolated home (pinning the clock and seeding prior state where a form needs
// it) and returns the args to run *without* --json — the test appends it. prose
// is the human ack marker that must NOT appear on stdout under --json, and
// ridPrefix is the receipt id's expected family (raw_/obs_/day_) so a row also
// proves which id the verb reported, not just that one is present.
type writeVerbJSONCase struct {
	name      string
	setup     func(t *testing.T) []string
	prose     string
	ridPrefix string
}

// TestWriteVerbsJSON_Trio is the log/closeout/obs half of the write-verb --json
// contract (AC-9, AC-14). Every distinct close-out write-result tail is a row:
// the compact form, `skip`, an idempotent re-close, `backfill`, and `amend` —
// the last two exercise the separate runBackfill/runCloseoutAmend renders. Each
// row asserts stdout is valid JSON carrying non-empty receipt_id + logical_date
// with no prose ack line, so a verb that forgets emitReceipt (or prints the
// human string) fails here. The skip and idempotent rows further pin that the
// day-record id is the receipt when no new raw entry was written.
func TestWriteVerbsJSON_Trio(t *testing.T) {
	cases := []writeVerbJSONCase{
		{
			name:      "log",
			prose:     "Saved as",
			ridPrefix: "raw_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				return []string{"log", "test"}
			},
		},
		{
			name:      "obs",
			prose:     "Logged",
			ridPrefix: "obs_",
			setup: func(t *testing.T) []string {
				enableAllObsKinds(t)
				return []string{"obs", "pain", "6", "knee"}
			},
		},
		{
			name:      "closeout compact",
			prose:     "streak",
			ridPrefix: "raw_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				return []string{"closeout", "dfx", "3/wrist", "the", "chain", "ran"}
			},
		},
		{
			name:      "closeout skip (day-record id fallback)",
			prose:     "Recorded a miss",
			ridPrefix: "day_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				return []string{"closeout", "skip"}
			},
		},
		{
			name:      "closeout idempotent (day-record id fallback)",
			prose:     "Already closed out",
			ridPrefix: "day_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				// After the evening bell the close-out lands on the base
				// logical day rather than back-attributing to last night, so
				// both passes hit the same day. Seal it once; the second
				// close-out is the idempotent path with no new raw entry, so
				// receipt_id must fall back to the day-record id.
				withClock(t, afterBell())
				_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "first pass")
				require.NoError(t, err)
				return []string{"closeout", "dfx", "3/wrist", "first pass"}
			},
		},
		{
			name:      "closeout backfill",
			prose:     "Backfilled",
			ridPrefix: "raw_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				withClock(t, afternoon())
				return []string{"closeout", "backfill", "yesterday", "ddd", "4", "ran", "it"}
			},
		},
		{
			name:      "closeout amend",
			prose:     "Amended",
			ridPrefix: "raw_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				withClock(t, afternoon())
				// Seal a prior day with a stub, then amend its journal off the
				// command line — the distinct runCloseoutAmend render tail.
				_, _, err := runRoot(t, BuildInfo{Version: "dev"},
					"closeout", "--day", "2026-07-03", "dfx", "3/wrist", "stub")
				require.NoError(t, err)
				jf := writeJournalFile(t, "the corrected narrative\n")
				return []string{"closeout", "amend", "--day", "2026-07-03", "--journal-file", jf}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append(tc.setup(t), "--json")
			out, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.NoError(t, err)

			assertReceiptJSON(t, out, tc.prose, tc.ridPrefix)
		})
	}
}

// assertReceiptJSON is the shared write-verb --json contract check: stdout is
// valid JSON, unmarshals to a bare object with non-empty receipt_id +
// logical_date, carries no prose ack line, and — when ridPrefix is set — the
// receipt id belongs to the expected family. It is the regression guard: a
// write verb that forgets emitReceipt or leaks the human string fails here.
func assertReceiptJSON(t *testing.T, out, prose, ridPrefix string) {
	t.Helper()
	require.True(t, json.Valid([]byte(out)), "stdout must be valid JSON under --json, got: %q", out)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &payload))

	rid, _ := payload["receipt_id"].(string)
	ld, _ := payload["logical_date"].(string)
	assert.NotEmpty(t, rid, "receipt_id must be a non-empty string")
	assert.NotEmpty(t, ld, "logical_date must be a non-empty string")
	if ridPrefix != "" {
		assert.Truef(t, strings.HasPrefix(rid, ridPrefix),
			"receipt_id %q should be a %s* id", rid, ridPrefix)
	}
	if prose != "" {
		assert.NotContainsf(t, out, prose,
			"stdout under --json must not carry the human ack marker %q", prose)
	}
}

// TestWriteVerbsJSON_Inner is the reframe/focus half of the write-verb --json
// contract (AC-10, AC-11, AC-13). Each inner-work write verb routes through the
// same emitReceipt choke point, so under --json it emits the bare
// {receipt_id, logical_date} object with no prose ack; the focus retire row
// proves the retirement event's own id + day is the receipt.
func TestWriteVerbsJSON_Inner(t *testing.T) {
	cases := []writeVerbJSONCase{
		{
			name:      "reframe add",
			prose:     "Added reframe",
			ridPrefix: "reframe_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				return []string{"reframe", "add", "catch", "flip"}
			},
		},
		{
			name:      "focus add",
			prose:     "Added focus",
			ridPrefix: "focus_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				return []string{"focus", "add", "work-on"}
			},
		},
		{
			name:      "focus retire",
			prose:     "Retired",
			ridPrefix: "focus_",
			setup: func(t *testing.T) []string {
				isolatedHome(t)
				_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "add", "work-on")
				require.NoError(t, err)
				return []string{"focus", "retire", resolveFocusID(t)}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append(tc.setup(t), "--json")
			out, _, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.NoError(t, err)

			assertReceiptJSON(t, out, tc.prose, tc.ridPrefix)
		})
	}
}

// resolveFocusID reads back the single seeded focus item's id from
// `focus list --json`, the same way focus_test.go resolves a retire target.
func resolveFocusID(t *testing.T) string {
	t.Helper()
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "focus", "list", "--json")
	require.NoError(t, err)
	var listed struct {
		Focus []struct {
			ID string `json:"id"`
		} `json:"focus"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &listed))
	require.NotEmpty(t, listed.Focus, "expected a seeded focus item to retire")
	return listed.Focus[0].ID
}

// TestModeJSON_EmitsModeShape pins mode's minimal --json shape (AC-12, AC-13):
// mode mints no receipt, so --json emits the bare {mode: …} object with no
// receipt_id, and the human prose never leaks onto stdout.
func TestModeJSON_EmitsModeShape(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "mode", "yellow", "--json")
	require.NoError(t, err)
	require.True(t, json.Valid([]byte(out)), "stdout must be valid JSON under --json, got: %q", out)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.Equal(t, "yellow", payload["mode"])
	assert.NotContains(t, payload, "receipt_id", "mode mints no receipt")
	assert.NotContains(t, out, "Mode set to")
}

// TestModeJSON_RejectExitParity proves a rejected write under --json keeps the
// same non-zero exit and writes nothing to stdout — diagnostics stay on stderr
// (AC-15). An invalid mode name (purple) is the parity case: --json must never
// turn a rejection into an empty-but-successful JSON object.
func TestModeJSON_RejectExitParity(t *testing.T) {
	isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "mode", "purple", "--json")
	require.ErrorIs(t, err, errModeNotAccepted)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out, "a rejected write writes nothing to stdout under --json")
	assert.Contains(t, errOut, "Mode must be one of green, yellow, or red.")
}
