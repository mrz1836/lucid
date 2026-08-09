package validate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// errSentinel is a shared sentinel for the error-propagation tests.
var errSentinel = errors.New("boom")

// fakeLedger is an in-memory LedgerSource: it enumerates fixed id lists and
// fails the reads named in the bad* sets, so the schema sweep can be driven
// without touching disk. A listErr forces a listing failure; a configErr fails
// the lucid.json check.
type fakeLedger struct {
	home          string
	processed     []string
	insights      []string
	reflections   []string
	people        []string
	links         []string
	secrets       []string
	badProcessed  map[string]bool
	badInsight    map[string]bool
	badReflect    map[string]bool
	badPerson     map[string]bool
	badLink       map[string]bool
	badSecret     map[string]bool
	secretStore   *storage.Adapter
	redirects     map[string]string
	listErr       error
	peopleListErr error
	linkListErr   error
	secretListErr error
	configErr     error
}

func (f *fakeLedger) Home() string { return f.home }

func (f *fakeLedger) ListProcessedIDs() ([]string, error)  { return f.processed, f.listErr }
func (f *fakeLedger) ListInsightIDs() ([]string, error)    { return f.insights, nil }
func (f *fakeLedger) ListReflectionIDs() ([]string, error) { return f.reflections, nil }
func (f *fakeLedger) ListPeopleKeys() ([]string, error)    { return f.people, f.peopleListErr }
func (f *fakeLedger) ListLinkEventIDs() ([]string, error)  { return f.links, f.linkListErr }
func (f *fakeLedger) ListSecretRefIDs() ([]string, error) {
	if f.secretStore != nil {
		return f.secretStore.ListSecretRefIDs()
	}
	return f.secrets, f.secretListErr
}

func (f *fakeLedger) ReadProcessedErr(id string) error  { return errIf(f.badProcessed, id) }
func (f *fakeLedger) ReadInsightErr(id string) error    { return errIf(f.badInsight, id) }
func (f *fakeLedger) ReadReflectionErr(id string) error { return errIf(f.badReflect, id) }
func (f *fakeLedger) ReadPersonErr(key string) error    { return errIf(f.badPerson, key) }
func (f *fakeLedger) ReadLinkEventErr(id string) error  { return errIf(f.badLink, id) }
func (f *fakeLedger) ReadSecretRefErr(id string) error {
	if f.secretStore != nil {
		return f.secretStore.ReadSecretRefErr(id)
	}
	return errIf(f.badSecret, id)
}

func (f *fakeLedger) PersonRedirect(key string) (string, error) {
	if f.badPerson[key] {
		return "", errIf(f.badPerson, key)
	}
	return f.redirects[key], nil
}

func (f *fakeLedger) LoadConfigErr() error { return f.configErr }

// errIf returns a parse error when id is in the bad set.
func errIf(bad map[string]bool, id string) error {
	if bad[id] {
		return errors.New("parse " + id + ": unexpected token")
	}
	return nil
}

// TestCheckLedgerSchema_Clean: a well-formed (or empty) Ledger has no findings.
func TestCheckLedgerSchema_Clean(t *testing.T) {
	found, err := CheckLedgerSchema(&fakeLedger{
		processed: []string{"p1", "p2"}, insights: []string{"i1"},
	})
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckLedgerSchema_ConfigError: a bad lucid.json is one finding at the
// config path.
func TestCheckLedgerSchema_ConfigError(t *testing.T) {
	found, err := CheckLedgerSchema(&fakeLedger{configErr: errors.New("read config: no such file")})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, CheckSchema, found[0].Check)
	assert.Equal(t, "lucid.json", found[0].Path)
	assert.Equal(t, "config", found[0].Rule)
}

// TestCheckLedgerSchema_BadRecords: each unparseable record across families is
// its own finding, pathed under the right subtree.
func TestCheckLedgerSchema_BadRecords(t *testing.T) {
	found, err := CheckLedgerSchema(&fakeLedger{
		processed:    []string{"p_ok", "p_bad"},
		insights:     []string{"i_bad"},
		reflections:  []string{"r_ok"},
		people:       []string{"person_bad"},
		badProcessed: map[string]bool{"p_bad": true},
		badInsight:   map[string]bool{"i_bad": true},
		badPerson:    map[string]bool{"person_bad": true},
	})
	require.NoError(t, err)
	require.Len(t, found, 3)
	paths := map[string]bool{}
	for _, f := range found {
		assert.Equal(t, SeverityError, f.Severity)
		paths[f.Path] = true
	}
	assert.True(t, paths["processed/p_bad"])
	assert.True(t, paths["insights/i_bad"])
	assert.True(t, paths["people/person_bad"])
}

// TestCheckLedgerSchema_BadLink: a malformed link-ledger line is one finding
// pathed under links/, addressed by the locator the listing reported, while a
// well-formed log adds none.
func TestCheckLedgerSchema_BadLink(t *testing.T) {
	found, err := CheckLedgerSchema(&fakeLedger{
		links:   []string{"link_1", "line-2", "link_3"},
		badLink: map[string]bool{"line-2": true},
	})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, CheckSchema, found[0].Check)
	assert.Equal(t, SeverityError, found[0].Severity)
	assert.Equal(t, "links/line-2", found[0].Path)
	assert.Equal(t, "links", found[0].Rule)
}

// TestCheckLedgerSchema_SecretReferenceFixtures drives the schema sweep over
// real temporary catalog logs: a valid names-only event is clean, while a
// malformed line becomes one error finding under secrets/.
func TestCheckLedgerSchema_SecretReferenceFixtures(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		home := t.TempDir()
		store := storage.New(home)
		_, err := store.AppendSecretRefEvent(storage.SecretRefEvent{
			Name:   "FIXTURE_SECRET",
			Op:     storage.SecretRefOpRegister,
			At:     time.Date(2026, time.August, 9, 15, 30, 0, 0, time.FixedZone("EDT", -4*60*60)).Format(time.RFC3339),
			Source: "test",
		})
		require.NoError(t, err)

		found, err := CheckLedgerSchema(&fakeLedger{secretStore: store})
		require.NoError(t, err)
		assert.Empty(t, found)
	})

	t.Run("malformed", func(t *testing.T) {
		home := t.TempDir()
		store := storage.New(home)
		secretsDir := filepath.Join(home, "secrets")
		require.NoError(t, os.MkdirAll(secretsDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(secretsDir, "secrets.jsonl"), []byte("{malformed\n"), 0o600))

		found, err := CheckLedgerSchema(&fakeLedger{secretStore: store})
		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.Equal(t, CheckSchema, found[0].Check)
		assert.Equal(t, SeverityError, found[0].Severity)
		assert.Equal(t, "secrets/line-1", found[0].Path)
		assert.Equal(t, "secrets", found[0].Rule)
	})
}

// TestCheckLedgerSchema_ListError: an unreadable listing is a hard error, not
// a finding.
func TestCheckLedgerSchema_ListError(t *testing.T) {
	_, err := CheckLedgerSchema(&fakeLedger{listErr: errSentinel})
	require.ErrorIs(t, err, errSentinel)
}
