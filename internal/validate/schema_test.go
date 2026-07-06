package validate

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errSentinel is a shared sentinel for the error-propagation tests.
var errSentinel = errors.New("boom")

// fakeLedger is an in-memory LedgerSource: it enumerates fixed id lists and
// fails the reads named in the bad* sets, so the schema sweep can be driven
// without touching disk. A listErr forces a listing failure; a configErr fails
// the lucid.json check.
type fakeLedger struct {
	home         string
	processed    []string
	insights     []string
	reflections  []string
	people       []string
	badProcessed map[string]bool
	badInsight   map[string]bool
	badReflect   map[string]bool
	badPerson    map[string]bool
	listErr      error
	configErr    error
}

func (f *fakeLedger) Home() string { return f.home }

func (f *fakeLedger) ListProcessedIDs() ([]string, error)  { return f.processed, f.listErr }
func (f *fakeLedger) ListInsightIDs() ([]string, error)    { return f.insights, nil }
func (f *fakeLedger) ListReflectionIDs() ([]string, error) { return f.reflections, nil }
func (f *fakeLedger) ListPeopleKeys() ([]string, error)    { return f.people, nil }

func (f *fakeLedger) ReadProcessedErr(id string) error  { return errIf(f.badProcessed, id) }
func (f *fakeLedger) ReadInsightErr(id string) error    { return errIf(f.badInsight, id) }
func (f *fakeLedger) ReadReflectionErr(id string) error { return errIf(f.badReflect, id) }
func (f *fakeLedger) ReadPersonErr(key string) error    { return errIf(f.badPerson, key) }

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

// TestCheckLedgerSchema_ListError: an unreadable listing is a hard error, not
// a finding.
func TestCheckLedgerSchema_ListError(t *testing.T) {
	_, err := CheckLedgerSchema(&fakeLedger{listErr: errSentinel})
	require.ErrorIs(t, err, errSentinel)
}
