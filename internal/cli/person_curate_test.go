package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPersonCLI_Merge_Prose seeds two records and merges them: prose ack on
// stdout, exit 0, and a later `person <merged form>` resolves to one canonical
// record (no §P-2).
func TestPersonCLI_Merge_Prose(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())
	writePersonRecord(t, home, "person_a-andy", "Andy", []string{"Andy"}, []string{"raw_2"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "merge", "person_a-andy", "person_a-alex")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "Merged")

	// The merged-away form now resolves to the one canonical record.
	look, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Andy")
	require.NoError(t, err)
	assert.NotContains(t, look, "more than one person")
	assert.Contains(t, look, "Alex")
}

// TestPersonCLI_Set_JSON records the durable fields and checks the --json
// projection shape (never the raw storage struct).
func TestPersonCLI_Set_JSON(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "set", "person_a-alex", "--dob", "1990-04-12", "--relationship", "colleague", "--json")
	require.NoError(t, err)

	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "person_a-alex", view.PersonKey)
	require.NotNil(t, view.Dob)
	assert.Equal(t, "1990-04-12", *view.Dob)
	require.NotNil(t, view.Relationship)
	assert.Equal(t, "colleague", *view.Relationship)
	assert.NotEmpty(t, view.Ack)
	assert.NotNil(t, view.Aka, "aka is always an array, never null")
}

// TestPersonCLI_Alias_JSON records another written form and checks the ack +
// projected aka.
func TestPersonCLI_Alias_JSON(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "alias", "person_a-alex", "Ali", "--json")
	require.NoError(t, err)

	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Contains(t, view.Aka, "Ali")
}

// TestPersonCLI_Rename_KeyStable renames and confirms the key is unchanged.
func TestPersonCLI_Rename_KeyStable(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "rename", "person_a-alex", "Alexandra", "--json")
	require.NoError(t, err)

	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "person_a-alex", view.PersonKey)
	assert.Equal(t, "Alexandra", view.DisplayName)
	assert.Contains(t, view.Aka, "Alex")
}

// TestPersonCLI_OffLimits_Toggle sets and restores redaction, checking the acks
// and the off_limits field in --json.
func TestPersonCLI_OffLimits_Toggle(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "off-limits", "person_a-alex", "--json")
	require.NoError(t, err)
	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.NotNil(t, view.OffLimits)
	assert.True(t, *view.OffLimits)

	// The read surface now renders the raw-record-only header.
	look, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Alex")
	require.NoError(t, err)
	assert.Contains(t, look, "off-limits to inference")

	restored, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "off-limits", "person_a-alex", "--restore")
	require.NoError(t, err)
	assert.Contains(t, restored, "no longer off-limits")
}

// TestPersonCLI_Rejection_ExitErr proves a deterministic rejection prints the
// fixed reason on stderr and maps to a non-zero exit, writing nothing.
func TestPersonCLI_Rejection_ExitErr(t *testing.T) {
	isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "merge", "Nobody", "AlsoNobody")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out, "a rejected write prints nothing on stdout")
	assert.Contains(t, errOut, "no one matches")
	assert.Contains(t, errOut, "nothing was saved")
}

// TestPersonCLI_Set_BadDob_ExitErr proves the §P-9 dob rejection.
func TestPersonCLI_Set_BadDob_ExitErr(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	_, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "set", "person_a-alex", "--dob", "04/12/1990")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Contains(t, errOut, "date of birth")
}

// TestPersonCLI_Reconcile_JSON seeds a duplicate pair and checks the --json
// candidate shape (including the suggested merge command) and prose exit 0.
func TestPersonCLI_Reconcile_JSON(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_s-sam", "Sam", []string{"Sam"}, []string{"raw_1"}, personSeed())
	writePersonRecord(t, home, "person_s-sammy", "Sammy", []string{"Sammy"}, []string{"raw_2"}, personSeed())

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "reconcile", "--json")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))

	var view personReconcileView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	require.Len(t, view.Candidates, 1)
	assert.Equal(t, "person_s-sam", view.Candidates[0].TargetKey)
	assert.Equal(t, "person_s-sammy", view.Candidates[0].SourceKey)
	assert.Equal(t, "lucid person merge person_s-sammy person_s-sam", view.Candidates[0].SuggestedMerge)
}

// TestPersonCLI_Reconcile_EmptyHonest: a clean store returns the honest empty
// prose and exit 0, with an empty (never null) candidates array under --json.
func TestPersonCLI_Reconcile_EmptyHonest(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "reconcile")
	require.NoError(t, err)
	assert.Contains(t, out, "No likely-duplicate people")

	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "reconcile", "--json")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, `"candidates": []`)
}

// TestPersonCLI_Reconcile_NoArgs: reconcile takes no positional args.
func TestPersonCLI_Reconcile_NoArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "reconcile", "extra")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestPersonCreateCmd_NameOnly proves a bare create joins the trailing words
// into one display name, mints a fresh record, and exits 0 with a "Recorded"
// prose ack (no enrichment flags).
func TestPersonCreateCmd_NameOnly(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "create", "Sam", "Rivera")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, `Recorded "Sam Rivera"`)

	// The minted record is now readable by its joined display name.
	look, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Sam", "Rivera")
	require.NoError(t, err)
	assert.Contains(t, look, "Sam Rivera")
	assert.Contains(t, look, "Mentioned in 0 entries")
}

// TestPersonCreateCmd_CreatesThenEnriches records the durable fields on the
// fresh record in one call and checks the --json projection shape (the
// deliberately-created record: aka[display_name], empty entry_refs, the
// enriched fields, and a "Recorded" ack).
func TestPersonCreateCmd_CreatesThenEnriches(t *testing.T) {
	isolatedHome(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"person", "create", "Sam Rivera",
		"--dob", "1990-04-12", "--relationship", "colleague", "--note", "met at the co-op", "--json")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))

	var view personWriteView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, "Sam Rivera", view.DisplayName)
	assert.Contains(t, view.Aka, "Sam Rivera")
	assert.NotNil(t, view.EntryRefs)
	assert.Empty(t, view.EntryRefs, "a never-mentioned record carries no entry_refs")
	require.NotNil(t, view.Dob)
	assert.Equal(t, "1990-04-12", *view.Dob)
	require.NotNil(t, view.Relationship)
	assert.Equal(t, "colleague", *view.Relationship)
	require.NotNil(t, view.Notes)
	assert.Equal(t, "met at the co-op", *view.Notes)
	assert.Contains(t, view.Ack, "Recorded")
	assert.Equal(t, view.FirstSeenAt, view.LastSeenAt, "seen-window is a single creation instant")
}

// TestPersonCreateCmd_IdempotentNoOp proves a repeat create of the same name
// exits 0 with the "already recorded" ack — never a rejection, never a fork.
func TestPersonCreateCmd_IdempotentNoOp(t *testing.T) {
	isolatedHome(t)

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "create", "Sam Rivera")
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "create", "Sam Rivera")
	require.NoError(t, err)
	assert.Equal(t, ExitOK, exitCodeForError(err))
	assert.Contains(t, out, "already recorded")

	// The no-op is visible under --json as created=false-equivalent: the ack, not
	// a second record. Re-reading still resolves to one record.
	look, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Sam Rivera")
	require.NoError(t, err)
	assert.NotContains(t, look, "more than one person")
}

// TestPersonCreateCmd_BadDob_ExitErr proves the §P-9 dob rejection: a non-civil
// --dob prints the fixed reason on stderr, maps to a non-zero exit, and writes
// nothing.
func TestPersonCreateCmd_BadDob_ExitErr(t *testing.T) {
	isolatedHome(t)

	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "create", "Sam Rivera", "--dob", "04/12/1990")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Empty(t, out, "a rejected create prints nothing on stdout")
	assert.Contains(t, errOut, "date of birth")
	assert.Contains(t, errOut, "nothing was saved")

	// Nothing was written: the name does not resolve to a record.
	look, _, lerr := runRoot(t, BuildInfo{Version: "dev"}, "person", "Sam Rivera")
	require.NoError(t, lerr)
	assert.Contains(t, look, "No one by that name yet")
}

// TestPersonCreateCmd_NoArgUsage: a bare `lucid person create` with no name is a
// cobra usage error (exit 2), distinct from the §P-9 rejection path.
func TestPersonCreateCmd_NoArgUsage(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "create")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestPersonCLI_ReadContractIntact re-confirms the read leaf is unchanged now
// that write children hang beneath it: a bare `person` still exits usage, and a
// name lookup still works.
func TestPersonCLI_ReadContractIntact(t *testing.T) {
	home := isolatedHome(t)
	writePersonRecord(t, home, "person_a-alex", "Alex", []string{"Alex"}, []string{"raw_1"}, personSeed())

	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Alex")
	require.NoError(t, err)
	assert.Contains(t, out, "Alex")
}
