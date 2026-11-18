package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
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

// identityMention is one scripted structuring mention.
type identityMention struct {
	DisplayName string   `json:"display_name"`
	Aka         []string `json:"aka,omitempty"`
}

// identityCLIStructure captures one raw entry at the given instant through
// `lucid log`, then structures it through `lucid structure` with a scripted
// extraction naming the mentions, and returns the raw id. The package's
// buildProvider seam is restored before it returns.
func identityCLIStructure(t *testing.T, at time.Time, mentions ...identityMention) string {
	t.Helper()
	bi := BuildInfo{Version: "dev"}
	withClock(t, at)
	out, stderr, err := runRoot(t, bi, "log", "A synthetic entry.", "--json")
	require.NoErrorf(t, err, "lucid log: %s", stderr)
	var receipt map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &receipt))
	raw, ok := receipt["receipt_id"].(string)
	require.Truef(t, ok, "log receipt: %s", out)

	reply, err := json.Marshal(map[string]any{
		"emotions": []any{}, "themes": []any{}, "people": mentions, "notes": "a synthetic entry",
	})
	require.NoError(t, err)
	fake := &provider.Fake{Script: []provider.Exchange{{Content: string(reply)}}}
	prev := buildProvider
	buildProvider = func(config.ProviderConfig) (provider.Provider, error) { return fake, nil }
	defer func() { buildProvider = prev }()
	_, stderr, err = runRoot(t, bi, "structure", raw)
	require.NoErrorf(t, err, "lucid structure: %s", stderr)
	require.Equal(t, 1, fake.Calls())
	return raw
}

// identityCLIKey reads a person's key through `lucid person <name> --json`.
func identityCLIKey(t *testing.T, name string) string {
	t.Helper()
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", name, "--json")
	require.NoError(t, err)
	var view personView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.Truef(t, view.Matched, "person %q", name)
	return view.PersonKey
}

// identityCLIWrite runs one identity verb under --json and decodes its
// receipt view, returning the raw output too.
func identityCLIWrite(t *testing.T, args ...string) (personIdentityWriteView, string) {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, append(args, "--json")...)
	require.NoErrorf(t, err, "lucid %s: %s", strings.Join(args, " "), stderr)
	var view personIdentityWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	return view, out
}

// identityTreeHash hashes every file under home, keyed by relative path, so a
// refusal can be proven to have left the Ledger byte-identical.
func identityTreeHash(t *testing.T, home string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(home, path)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(b)
		files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	require.NoError(t, err)
	return files
}

// identityReceiptID is the identity_YYYY_MM_DD_<slot> receipt grammar.
var identityReceiptID = regexp.MustCompile(`^identity_\d{4}_\d{2}_\d{2}_[a-z]+$`)

// heldSamCLIHome is the identity CLI home plus a structured C3 state: "Sam
// Rivera, aka Sam" and then two bare "Sam" mentions, each held (§P-11), and
// one plain "Sam Rivera" mention. It returns both keys and the raw ids.
func heldSamCLIHome(t *testing.T) (home, sam, river string, raws []string) {
	t.Helper()
	home = identityCLIHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
	sam = identityCLIKey(t, "Sam")
	river = identityCLIKey(t, "Sam Rivera")
	base := identityCLIAt()
	raws = []string{
		identityCLIStructure(t, base.Add(1*time.Minute), identityMention{DisplayName: "Sam Rivera", Aka: []string{"Sam"}}),
		identityCLIStructure(t, base.Add(2*time.Minute), identityMention{DisplayName: "Sam"}),
		identityCLIStructure(t, base.Add(3*time.Minute), identityMention{DisplayName: "Sam"}),
		identityCLIStructure(t, base.Add(4*time.Minute), identityMention{DisplayName: "Sam Rivera"}),
	}
	withClock(t, base.Add(10*time.Minute))
	return home, sam, river, raws
}

// TestPersonIdentityCLI_JSONReceipts: all five identity writes — dormant,
// restore, claim, release, and move-ref (with and without --from) — return an
// identity_YYYY_MM_DD_<slot> receipt in one --json shape: pinned always an
// array listing every held mention the write settled, still_held always a
// number, status only on dormant/restore, form only on claim/release,
// displaced only on a forced claim, raw_id/from/to only on move-ref (from left
// out without --from). A no-op carries no receipt.
func TestPersonIdentityCLI_JSONReceipts(t *testing.T) {
	_, sam, river, raws := heldSamCLIHome(t)
	held1, held2, plain := raws[1], raws[2], raws[3]

	// move-ref without --from pins the entry's one held mention.
	v, out := identityCLIWrite(t, "person", "move-ref", held1, "--to", river)
	assert.Regexp(t, identityReceiptID, v.ReceiptID)
	assert.Equal(t, "identity_2026_11_17_c", v.ReceiptID, "the two holds took slots a and b")
	assert.Equal(t, river, v.PersonKey)
	assert.Equal(t, "Sam Rivera", v.DisplayName)
	assert.Equal(t, held1, v.RawID)
	assert.Equal(t, river, v.To)
	assert.NotContains(t, out, `"from"`, "from is left out without --from")
	assert.NotContains(t, topLevelKeys(t, out), "form", "form is on claim/release and on each pin only")
	assert.NotContains(t, out, `"status"`)
	assert.Equal(t, []personIdentityPinView{{RawID: held1, Form: "Sam", PersonKey: river}}, v.Pinned)
	assert.Contains(t, out, `"still_held": 0`)
	assert.Equal(t, `Pinned the held "Sam" in `+held1+` to "Sam Rivera" (`+river+`) (identity_2026_11_17_c).`, v.Ack)

	// move-ref --from moves a filed mention.
	v, out = identityCLIWrite(t, "person", "move-ref", plain, "--from", river, "--to", sam)
	assert.Equal(t, "identity_2026_11_17_d", v.ReceiptID)
	assert.Equal(t, sam, v.PersonKey)
	assert.Equal(t, plain, v.RawID)
	assert.Equal(t, river, v.From)
	assert.Equal(t, sam, v.To)
	assert.Contains(t, out, `"pinned": []`)
	assert.Equal(t, `Moved the mention in `+plain+` from "Sam Rivera" (`+river+`) to "Sam" (`+sam+`) (identity_2026_11_17_d).`, v.Ack)

	// A forced claim names the displaced person and pins the other hold.
	v, out = identityCLIWrite(t, "person", "claim", river, "Sam", "--force")
	assert.Equal(t, "identity_2026_11_17_e", v.ReceiptID)
	assert.Equal(t, "Sam", v.Form)
	assert.Equal(t, []personIdentityRefView{{PersonKey: sam, DisplayName: "Sam"}}, v.Displaced)
	assert.Equal(t, []personIdentityPinView{{RawID: held2, Form: "Sam", PersonKey: river}}, v.Pinned)
	assert.Zero(t, v.StillHeld)
	assert.NotContains(t, topLevelKeys(t, out), "raw_id", "raw_id is on move-ref and on each pin only")
	assert.Contains(t, v.Ack, "Pinned 1 held mention:\n- "+held2+` "Sam" → "Sam Rivera" (`+river+`)`)

	v, out = identityCLIWrite(t, "person", "claim", river, "Sam")
	assert.Empty(t, v.ReceiptID, "a no-op carries no receipt")
	assert.NotContains(t, out, `"receipt_id"`)
	assert.Contains(t, out, `"pinned": []`)

	v, _ = identityCLIWrite(t, "person", "release", river, "Sam")
	assert.Equal(t, "identity_2026_11_17_g", v.ReceiptID, "the claim's pin took slot f")
	assert.Equal(t, "Sam", v.Form)

	v, out = identityCLIWrite(t, "person", "dormant", sam)
	assert.Equal(t, "identity_2026_11_17_h", v.ReceiptID)
	assert.Equal(t, "dormant", v.Status)
	assert.NotContains(t, topLevelKeys(t, out), "form")
	assert.NotContains(t, out, `"displaced"`)
	v, out = identityCLIWrite(t, "person", "dormant", sam)
	assert.Empty(t, v.ReceiptID)
	assert.NotContains(t, out, `"receipt_id"`)
	v, _ = identityCLIWrite(t, "person", "dormant", sam, "--restore")
	assert.Equal(t, "identity_2026_11_17_i", v.ReceiptID)
	assert.Equal(t, "active", v.Status)

	// The human ack is the same prose, without the JSON envelope.
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "move-ref", plain, "--from", sam, "--to", river)
	require.NoError(t, err)
	assert.Equal(t, `Moved the mention in `+plain+` from "Sam" (`+sam+`) to "Sam Rivera" (`+river+`) (identity_2026_11_17_j).`+"\n", out)
}

// TestPersonIdentityCLI_RejectionsWriteNothing: every identity-verb refusal —
// §P-4, §P-5, §P-9, §P-10, and §P-12 — exits non-zero with its fixed reason on
// stderr ending "nothing was saved.", and leaves every Ledger byte as it was;
// on a home with no identity log, no refusal creates one. A move-ref without
// --to is a usage error (exit 2).
func TestPersonIdentityCLI_RejectionsWriteNothing(t *testing.T) {
	home := identityCLIHome(t)
	t.Setenv("LUCID_NO_UPDATE_CHECK", "1")
	bi := BuildInfo{Version: "dev"}
	river := identityCLIKey(t, "Sam Rivera")
	sam := identityCLIKey(t, "Sam")
	base := identityCLIAt()
	// "Sam Rivera, aka Sam" makes a bare "Sam" name two records (§P-5) without
	// writing an identity event.
	aka := identityCLIStructure(t, base.Add(time.Minute), identityMention{DisplayName: "Sam Rivera", Aka: []string{"Sam"}})
	withClock(t, base.Add(10*time.Minute))

	refuse := func(t *testing.T, code int, want string, args ...string) {
		t.Helper()
		before := identityTreeHash(t, home)
		_, stderr, err := runRoot(t, bi, args...)
		require.Error(t, err)
		assert.Equal(t, code, exitCodeForError(err))
		assert.Contains(t, stderr, want)
		if code == ExitErr {
			assert.True(t, strings.HasSuffix(stderr, "nothing was saved.\n"), stderr)
		}
		assert.Equal(t, before, identityTreeHash(t, home), "a refusal leaves the Ledger byte-identical")
	}
	cases := []struct {
		name string
		code int
		want string
		args []string
	}{
		{"P-4 dormant", ExitErr, `no one matches "Robin Nobody"`, []string{"person", "dormant", "Robin Nobody"}},
		{"P-4 claim", ExitErr, `no one matches "Robin Nobody"`, []string{"person", "claim", "Robin Nobody", "Sam"}},
		{
			"P-4 move-ref to", ExitErr, `no one matches "Robin Nobody" — add them first with lucid person create <name>; nothing was saved.`,
			[]string{"person", "move-ref", aka, "--to", "Robin Nobody"},
		},
		{"P-5 dormant", ExitErr, `"Sam" matches more than one person — disambiguate with a person_key (`, []string{"person", "dormant", "Sam"}},
		{"P-5 release", ExitErr, `"Sam" matches more than one person`, []string{"person", "release", "Sam", "Rivera"}},
		{"P-9 blank form", ExitErr, "a name form is required; nothing was saved.", []string{"person", "claim", river, "  "}},
		{
			"P-9 reason", ExitErr, "a dormant reason is one line; nothing was saved.",
			[]string{"person", "dormant", sam, "--reason-file", writeReasonFile(t, "one\ntwo\n")},
		},
		{"P-10 claim", ExitErr, `the form "Sam" is held by "Sam" (` + sam + `), who is active`, []string{"person", "claim", river, "Sam"}},
		{
			"P-12 unknown raw", ExitErr, "there is no entry raw_2026_01_01_00_00 — check the raw id; nothing was saved.",
			[]string{"person", "move-ref", "raw_2026_01_01_00_00", "--to", river},
		},
		{
			"P-12 same person", ExitErr, `--from and --to are the same person ("Sam Rivera"); nothing was saved.`,
			[]string{"person", "move-ref", aka, "--from", river, "--to", river},
		},
		{
			"P-12 no mention", ExitErr, aka + ` has no mention under "Sam" (` + sam + `) to move; nothing was saved.`,
			[]string{"person", "move-ref", aka, "--from", sam, "--to", river},
		},
		{
			"P-12 no held", ExitErr, aka + " has no held mention — name the person it is filed under with --from; nothing was saved.",
			[]string{"person", "move-ref", aka, "--to", river},
		},
		{"usage: move-ref without --to", ExitUsage, `required flag(s) "to" not set`, []string{"person", "move-ref", aka}},
		{"usage: move-ref without raw", ExitUsage, "accepts 1 arg(s)", []string{"person", "move-ref", "--to", river}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { refuse(t, tc.code, tc.want, tc.args...) })
	}
	_, err := os.Stat(filepath.Join(home, "identity"))
	require.ErrorIs(t, err, os.ErrNotExist, "no refusal creates the identity tree")

	// With a log present, a refusal leaves its bytes as they were too.
	held := identityCLIStructure(t, base.Add(2*time.Minute), identityMention{DisplayName: "Sam"})
	identityCLIStructure(t, base.Add(3*time.Minute), identityMention{DisplayName: "Jordan Lee", Aka: []string{"Sam"}})
	withClock(t, base.Add(10*time.Minute))
	require.NotEmpty(t, identityLog(t, home))
	refuse(t, ExitErr, held+` has no mention under "Sam" (`+sam+`) to move; nothing was saved.`,
		"person", "move-ref", held, "--from", sam, "--to", river)
	refuse(t, ExitErr, `the form "Sam" is held by other active people`, "person", "claim", river, "Sam")
}

// topLevelKeys returns the keys of a --json object's top level.
func topLevelKeys(t *testing.T, out string) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &obj))
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	return keys
}
