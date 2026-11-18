package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The identity-verb CLI fixtures use synthetic people only: two people named
// "Sam" ("Sam" and "Sam Rivera").

// identityCLIAt is the pinned instant the identity-verb CLI tests run at.
func identityCLIAt() time.Time {
	return time.Date(2026, time.November, 17, 9, 0, 0, 0, time.FixedZone("EST", -5*3600))
}

// identityCLIHome returns an isolated home with "Sam" and "Sam Rivera"
// created through the CLI and the clock pinned.
func identityCLIHome(t *testing.T) string {
	t.Helper()
	home := isolatedHome(t)
	withClock(t, identityCLIAt())
	for _, name := range []string{"Sam", "Sam Rivera"} {
		_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "create", name)
		require.NoError(t, err)
	}
	return home
}

// identityLog returns the identity log's bytes ("" when absent).
func identityLog(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "identity", "identity.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

// writeReasonFile writes a --reason-file body and returns its path.
func writeReasonFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reason.txt")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// TestPersonDormant_CLIReasonFileJSONAndUsage: `person dormant` reads a
// one-line --reason-file, emits the identity receipt under --json (pinned
// always an array), refuses a multi-line reason (exit 1) and --reason-file
// with --restore (usage, exit 2) before any write, and --restore reaches the
// dormant record by name.
func TestPersonDormant_CLIReasonFileJSONAndUsage(t *testing.T) {
	home := identityCLIHome(t)
	bi := BuildInfo{Version: "dev"}

	_, stderr, err := runRoot(t, bi, "person", "dormant", "Sam", "--reason-file", writeReasonFile(t, "one\ntwo\n"))
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Contains(t, stderr, "a dormant reason is one line; nothing was saved.")
	_, stderr, err = runRoot(t, bi, "person", "dormant", "Sam", "--reason-file", writeReasonFile(t, ""))
	require.Error(t, err)
	assert.Contains(t, stderr, "--reason-file: file is empty")
	assert.Empty(t, identityLog(t, home), "a refusal writes nothing")

	out, _, err := runRoot(t, bi, "person", "dormant", "Sam", "--reason-file", writeReasonFile(t, "an old contact\n"), "--json")
	require.NoError(t, err)
	var view personIdentityWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "identity_2026_11_17_a", view.ReceiptID)
	assert.Equal(t, "Sam", view.DisplayName)
	assert.Equal(t, "dormant", view.Status)
	assert.Empty(t, view.Form)
	assert.NotNil(t, view.Pinned)
	assert.Contains(t, out, `"pinned": []`)
	assert.Contains(t, out, `"still_held": 0`)
	assert.NotContains(t, out, `"displaced"`)
	assert.Contains(t, identityLog(t, home), `"reason":"an old contact"`)

	logBefore := identityLog(t, home)
	_, stderr, err = runRoot(t, bi, "person", "dormant", "Sam", "--restore", "--reason-file", writeReasonFile(t, "why\n"))
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
	assert.Contains(t, stderr, "--reason-file only applies when marking someone dormant, not with --restore; nothing was saved.")
	assert.Equal(t, logBefore, identityLog(t, home))

	out, _, err = runRoot(t, bi, "person", "dormant", "Sam", "--restore")
	require.NoError(t, err)
	assert.Equal(t, "\"Sam\" is active again (identity_2026_11_17_b).\n", out)

	out, _, err = runRoot(t, bi, "person", "dormant", "Sam", "--restore", "--json")
	require.NoError(t, err)
	var noop personIdentityWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &noop))
	assert.Empty(t, noop.ReceiptID, "a no-op carries no receipt")
	assert.NotContains(t, out, `"receipt_id"`)
	assert.Equal(t, `"Sam" is already active — nothing to change.`, noop.Ack)
}

// TestPersonClaim_CLIRefusalForceAndRelease: `person claim` refuses a form an
// active person holds (exit 1, the §P-10 copy, nothing written), --force
// writes it and names the displaced person under --json, and `person release`
// ends it — a second release is a no-op with no receipt.
func TestPersonClaim_CLIRefusalForceAndRelease(t *testing.T) {
	home := identityCLIHome(t)
	bi := BuildInfo{Version: "dev"}

	_, stderr, err := runRoot(t, bi, "person", "claim", "Sam Rivera", "Sam")
	require.ErrorIs(t, err, errPersonNotRecorded)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Contains(t, stderr, `the form "Sam" is held by "Sam" (`)
	assert.Contains(t, stderr, "or re-run with --force; nothing was saved.")
	assert.Empty(t, identityLog(t, home))
	_, err = os.Stat(filepath.Join(home, "identity"))
	require.ErrorIs(t, err, os.ErrNotExist, "a refusal creates no identity tree")

	out, _, err := runRoot(t, bi, "person", "claim", "Sam Rivera", "Sam", "--force", "--json")
	require.NoError(t, err)
	var view personIdentityWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "identity_2026_11_17_a", view.ReceiptID)
	assert.Equal(t, "Sam Rivera", view.DisplayName)
	assert.Equal(t, "Sam", view.Form)
	assert.Empty(t, view.Status)
	require.Len(t, view.Displaced, 1)
	assert.Equal(t, "Sam", view.Displaced[0].DisplayName)
	assert.Contains(t, view.Ack, "Overrode \"Sam\" (")

	out, _, err = runRoot(t, bi, "person", "release", "Sam Rivera", "Sam")
	require.NoError(t, err)
	assert.Equal(t, "Released \"Sam\" from \"Sam Rivera\" — a bare \"Sam\" no longer means them by claim (identity_2026_11_17_b).\n", out)

	out, _, err = runRoot(t, bi, "person", "release", "Sam Rivera", "Sam", "--json")
	require.NoError(t, err)
	var noop personIdentityWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &noop))
	assert.Empty(t, noop.ReceiptID)
	assert.NotContains(t, out, `"receipt_id"`)
	assert.Equal(t, `"Sam Rivera" does not claim "Sam" — nothing to release.`, noop.Ack)

	out, _, err = runRoot(t, bi, "person", "claim", "Sam Rivera", "Rivera")
	require.NoError(t, err)
	assert.Contains(t, out, "A bare \"Rivera\" now means \"Sam Rivera\"")
}
