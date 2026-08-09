package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func syntheticSecretRefEvent(name string, op SecretRefOp, at time.Time) SecretRefEvent {
	return SecretRefEvent{Name: name, Op: op, At: at.Format(time.RFC3339), Source: "cli"}
}

func secretRefsLogPath(home string) string {
	return filepath.Join(home, secretsDirName, secretsFileName)
}

func TestSecretRefTypesAreValueFree(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)value|secret|cipher|ciphertext|token|jwt|payload|plaintext`)
	for _, typ := range []reflect.Type{reflect.TypeOf(SecretRefEvent{}), reflect.TypeOf(SecretRef{})} {
		for i := range typ.NumField() {
			field := typ.Field(i)
			assert.Falsef(t, forbidden.MatchString(field.Name), "%s.%s could carry secret material", typ.Name(), field.Name)
		}
	}
}

func TestScaffoldSecretsIsLazyAndIdempotent(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	referencesDir := filepath.Join(home, secretsDirName)
	_, err := os.Stat(referencesDir)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, a.ScaffoldSecrets())
	require.NoError(t, a.ScaffoldSecrets())
	info, err := os.Stat(referencesDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestSecretRefNameAndNoteValidation(t *testing.T) {
	for _, name := range []string{"FIXTURE_SECRET", "A", "_X1"} {
		t.Run("valid_"+name, func(t *testing.T) {
			require.NoError(t, ValidateSecretRefName(name))
		})
	}

	for _, name := range []string{"", "lower", "1ABC", strings.Repeat("A", 65), "A-B"} {
		t.Run("invalid_"+name, func(t *testing.T) {
			require.Error(t, ValidateSecretRefName(name))
		})
	}
	require.NoError(t, ValidateSecretRefNote(strings.Repeat("é", 256)))
	require.Error(t, ValidateSecretRefNote(strings.Repeat("é", 257)))
}

func TestSecretRefAppendFoldTombstoneAndReregister(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	a := New(home)
	base := time.Date(2026, time.August, 9, 15, 0, 0, 0, time.FixedZone("EDT", -4*60*60))

	first, err := a.AppendSecretRefEvent(syntheticSecretRefEvent("FIXTURE_SECRET", SecretRefOpRegister, base))
	require.NoError(t, err)
	assert.Equal(t, "secretref_1", first.ID)
	assert.Equal(t, SecretRefSchema, first.Schema)

	refs, err := a.ListSecretRefs()
	require.NoError(t, err)
	require.Equal(t, []SecretRef{{Name: "FIXTURE_SECRET", CreatedAt: base.Format(time.RFC3339)}}, refs)

	removed, err := a.AppendSecretRefEvent(syntheticSecretRefEvent("FIXTURE_SECRET", SecretRefOpRemove, base.Add(time.Minute)))
	require.NoError(t, err)
	assert.Equal(t, "secretref_2", removed.ID)
	refs, err = a.ListSecretRefs()
	require.NoError(t, err)
	assert.Empty(t, refs)

	reregister := syntheticSecretRefEvent("FIXTURE_SECRET", SecretRefOpRegister, base.Add(2*time.Minute))
	reregister.Note = "synthetic reference"
	third, err := a.AppendSecretRefEvent(reregister)
	require.NoError(t, err)
	assert.Equal(t, "secretref_3", third.ID)
	refs, err = a.ListSecretRefs()
	require.NoError(t, err)
	require.Equal(t, []SecretRef{{
		Name:      "FIXTURE_SECRET",
		Note:      "synthetic reference",
		CreatedAt: base.Add(2 * time.Minute).Format(time.RFC3339),
	}}, refs)

	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Zero(t, skipped)
	assert.Len(t, events, 3)
}

func TestFoldSecretRefsKeepsFirstRegistrationInLiveGeneration(t *testing.T) {
	base := fixedTime()
	events := []SecretRefEvent{
		syntheticSecretRefEvent("FIXTURE_SECRET", SecretRefOpRegister, base),
		syntheticSecretRefEvent("FIXTURE_SECRET", SecretRefOpRegister, base.Add(time.Minute)),
	}
	events[1].Note = "latest note"
	folded := foldSecretRefs(events)
	assert.Equal(t, SecretRef{
		Name:      "FIXTURE_SECRET",
		Note:      "latest note",
		CreatedAt: base.Format(time.RFC3339),
	}, folded["FIXTURE_SECRET"])
}

func TestSecretRefIDsAreNeverReusedAndMalformedLinesAreTolerated(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	a := New(home)
	path := secretRefsLogPath(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
	seed := `{"schema":"secretref.v1","id":"secretref_1","name":"FIXTURE_A","op":"register","at":"2026-08-09T15:00:00-04:00","source":"cli"}
{ this is malformed
{"schema":"secretref.v1","id":"secretref_5","name":"FIXTURE_B","op":"register","at":"2026-08-09T15:01:00-04:00","source":"cli"}
`
	require.NoError(t, os.WriteFile(path, []byte(seed), filePerm))

	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, 1, skipped)

	appended, err := a.AppendSecretRefEvent(syntheticSecretRefEvent(
		"FIXTURE_C",
		SecretRefOpRegister,
		time.Date(2026, time.August, 9, 15, 2, 0, 0, time.FixedZone("EDT", -4*60*60)),
	))
	require.NoError(t, err)
	assert.Equal(t, "secretref_6", appended.ID)

	ids, err := a.ListSecretRefIDs()
	require.NoError(t, err)
	assert.Equal(t, []string{"secretref_1", "line-2", "secretref_5", "secretref_6"}, ids)
	require.Error(t, a.ReadSecretRefErr("line-2"))
	require.NoError(t, a.ReadSecretRefErr("secretref_6"))
}

func TestAppendSecretRefEventRejectsInvalidRecordsBeforeWriting(t *testing.T) {
	a := New(t.TempDir())
	base := syntheticSecretRefEvent("FIXTURE_SECRET", SecretRefOpRegister, fixedTime())
	cases := map[string]func(SecretRefEvent) SecretRefEvent{
		"name":   func(ev SecretRefEvent) SecretRefEvent { ev.Name = "invalid"; return ev },
		"op":     func(ev SecretRefEvent) SecretRefEvent { ev.Op = "unknown"; return ev },
		"note":   func(ev SecretRefEvent) SecretRefEvent { ev.Note = strings.Repeat("x", 257); return ev },
		"at":     func(ev SecretRefEvent) SecretRefEvent { ev.At = "not-a-time"; return ev },
		"source": func(ev SecretRefEvent) SecretRefEvent { ev.Source = ""; return ev },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.AppendSecretRefEvent(mutate(base))
			require.Error(t, err)
		})
	}
	events, skipped, err := a.ReadSecretRefEvents()
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Zero(t, skipped)
}

func TestListSecretRefsSortsByName(t *testing.T) {
	a := New(t.TempDir())
	for i, name := range []string{"FIXTURE_Z", "FIXTURE_A", "FIXTURE_M"} {
		_, err := a.AppendSecretRefEvent(syntheticSecretRefEvent(name, SecretRefOpRegister, fixedTime().Add(time.Duration(i)*time.Second)))
		require.NoError(t, err)
	}
	refs, err := a.ListSecretRefs()
	require.NoError(t, err)
	require.Len(t, refs, 3)
	assert.Equal(t, []string{"FIXTURE_A", "FIXTURE_M", "FIXTURE_Z"}, []string{refs[0].Name, refs[1].Name, refs[2].Name})
}
