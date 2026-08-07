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
