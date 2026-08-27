package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// TestListEras_EmptyStore proves the read-only enumerator returns an honest
// empty result on a thin store and writes nothing (it never scaffolds the
// registry tree).
func TestListEras_EmptyStore(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	eras, err := r.ListEras()
	require.NoError(t, err)
	assert.Empty(t, eras)

	recs, err := a.ReadRegistryKind(observations.RegistryEra)
	require.NoError(t, err)
	assert.Empty(t, recs, "era list must not create the eras registry")
}

// TestListEras_SummariesAndReadOnly proves ListEras enumerates every chapter
// key-sorted, renders the shared chapter span, surfaces the raw bounds, and
// writes nothing.
func TestListEras_SummariesAndReadOnly(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.WriteEra(EraWriteRequest{Name: "synthetic coast years", Start: "2010", End: "2014", Now: fixedNow()})
	require.NoError(t, err)
	_, err = r.WriteEra(EraWriteRequest{Name: "synthetic desert years", Start: "2015", Now: fixedNow()})
	require.NoError(t, err)

	before, err := a.ReadRegistryKind(observations.RegistryEra)
	require.NoError(t, err)
	require.Len(t, before, 2)

	eras, err := r.ListEras()
	require.NoError(t, err)
	require.Len(t, eras, 2)

	// Key-sorted (ReadRegistryKind's contract), each carrying its span + bounds.
	byName := map[string]EraSummary{}
	for _, e := range eras {
		byName[e.DisplayName] = e
		assert.NotEmpty(t, e.Key)
	}
	assert.Equal(t, "2010 → 2014", byName["synthetic coast years"].Span)
	assert.Equal(t, "2010", byName["synthetic coast years"].Start)
	assert.Equal(t, "2014", byName["synthetic coast years"].End)
	assert.Equal(t, "ongoing since 2015", byName["synthetic desert years"].Span)

	after, err := a.ReadRegistryKind(observations.RegistryEra)
	require.NoError(t, err)
	assert.Equal(t, before, after, "era list must not mutate the registry")
}

// TestWriteEra_MustExistHardErrors proves the strict-amend gate: MustExist on a
// name that matches no existing chapter hard-errors (pointing at era create) and
// writes nothing, while MustExist on an existing chapter amends normally.
func TestWriteEra_MustExistHardErrors(t *testing.T) {
	r, a, _ := newBootedRouter(t)

	_, err := r.WriteEra(EraWriteRequest{Name: "synthetic anchor chapter", Start: "2010", Now: fixedNow()})
	require.NoError(t, err)
	before, err := a.ReadRegistryKind(observations.RegistryEra)
	require.NoError(t, err)
	require.Len(t, before, 1)

	// A non-matching amend hard-errors and writes nothing.
	_, err = r.WriteEra(EraWriteRequest{Name: "synthetic ghost chapter", MustExist: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no era named")
	assert.Contains(t, err.Error(), "era create")
	after, err := a.ReadRegistryKind(observations.RegistryEra)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a rejected amend must write nothing")

	// A matching amend succeeds under the same gate.
	amended, err := r.WriteEra(EraWriteRequest{Name: "synthetic anchor chapter", MustExist: true, Note: "still open", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, amended.Created)
	assert.Equal(t, "still open", amended.Fields["note"])
}
