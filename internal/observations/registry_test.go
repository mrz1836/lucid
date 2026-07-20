package observations

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/data"
)

var registryKeyRE = regexp.MustCompile(`^(injury|thread|place|era)_[a-z]-[a-z]+(-\d+)?$`)

func wordlist(t *testing.T) []string {
	t.Helper()
	wl := data.Wordlist()
	require.Len(t, wl, 256, "the MVP wordlist is exactly 256 entries")
	return wl
}

// TestDeriveRegistryKey_SaltedStableFormat: the key is stable for the same
// (kind, name, salt), carries the kind prefix, and changes with the salt —
// the reversibility guard (observations-module.md §"Registry keys").
func TestDeriveRegistryKey_SaltedStableFormat(t *testing.T) {
	wl := wordlist(t)

	k1, err := DeriveRegistryKey(RegistryInjury, "left knee", "salt-a", wl)
	require.NoError(t, err)
	k2, err := DeriveRegistryKey(RegistryInjury, "Left Knee.", "salt-a", wl)
	require.NoError(t, err)
	assert.Equal(t, k1, k2, "the same referent normalizes to one key")
	assert.Regexp(t, registryKeyRE, k1)
	assert.Contains(t, k1, "injury_")

	place, err := DeriveRegistryKey(RegistryPlace, "Lisbon", "salt-a", wl)
	require.NoError(t, err)
	assert.Contains(t, place, "place_")

	// Salt is incorporated: at least one of several salts must differ.
	keys := map[string]bool{}
	for _, salt := range []string{"salt-a", "salt-b", "salt-c", "salt-d", "salt-e"} {
		k, derr := DeriveRegistryKey(RegistryPlace, "Lisbon", salt, wl)
		require.NoError(t, derr)
		keys[k] = true
	}
	assert.Greater(t, len(keys), 1, "different salts must not collapse to one key")
}

// TestDeriveRegistryKey_Golden pins the salted derivation against the shipped
// wordlist for a fixed set of (kind, name, salt) tuples. These are regression
// anchors: if the wordlist, the salt handling (salt+NUL+name), or the
// derivation math changes, existing registry filenames would drift and this
// fails. It covers both an empty salt and a non-empty salt (which must not
// collapse to the same key) and confirms case/punctuation variants normalize
// to one key.
func TestDeriveRegistryKey_Golden(t *testing.T) {
	wl := wordlist(t)
	type input struct{ kind, name, salt string }
	golden := map[input]string{
		{RegistryInjury, "left knee", ""}:           "injury_b-flame",
		{RegistryInjury, "left knee", "salt-a"}:     "injury_c-peak",
		{RegistryInjury, "Left Knee.", "salt-a"}:    "injury_c-peak",
		{RegistryPlace, "Lisbon", ""}:               "place_b-marsh",
		{RegistryPlace, "Lisbon", "salt-a"}:         "place_f-hook",
		{RegistryThread, "money worries", "salt-a"}: "thread_r-clay",
		{RegistryEra, "college years", "salt-a"}:    "era_d-swell",
	}
	for in, want := range golden {
		got, err := DeriveRegistryKey(in.kind, in.name, in.salt, wl)
		require.NoErrorf(t, err, "DeriveRegistryKey(%q, %q, %q)", in.kind, in.name, in.salt)
		assert.Equalf(t, want, got, "DeriveRegistryKey(%q, %q, %q)", in.kind, in.name, in.salt)
	}
}

func TestDeriveRegistryKey_Errors(t *testing.T) {
	wl := wordlist(t)
	_, err := DeriveRegistryKey("nonsense", "x", "s", wl)
	require.Error(t, err)
	_, err = DeriveRegistryKey(RegistryPlace, "   ", "s", wl)
	require.Error(t, err) // empty after normalize
	_, err = DeriveRegistryKey(RegistryPlace, "Lisbon", "s", nil)
	require.Error(t, err) // empty wordlist
}

// TestResolveRegistryKey_CollisionSuffix: a second referent whose name hashes
// to a taken key gets a -2 suffix; the same referent keeps its key.
func TestResolveRegistryKey_CollisionSuffix(t *testing.T) {
	wl := wordlist(t)
	base, err := DeriveRegistryKey(RegistryPlace, "Lisbon", "s", wl)
	require.NoError(t, err)

	// The base key is held by a *different* normalized name.
	owner := func(candidate string) (string, bool) {
		if candidate == base {
			return NormalizedName("Porto"), true
		}
		return "", false
	}
	got, err := ResolveRegistryKey(RegistryPlace, "Lisbon", "s", wl, owner)
	require.NoError(t, err)
	assert.Equal(t, base+"-2", got)

	// The same referent already owns the base key → no suffix.
	ownerSelf := func(candidate string) (string, bool) {
		if candidate == base {
			return NormalizedName("Lisbon"), true
		}
		return "", false
	}
	got, err = ResolveRegistryKey(RegistryPlace, "Lisbon", "s", wl, ownerSelf)
	require.NoError(t, err)
	assert.Equal(t, base, got)

	// Nil owner yields the bare key.
	got, err = ResolveRegistryKey(RegistryPlace, "Lisbon", "s", wl, nil)
	require.NoError(t, err)
	assert.Equal(t, base, got)
}

// TestRegistry_ApplyAppendsHistory: every patch appends a status_history
// entry; aka merges; fields add/replace/null; status transitions
// (observations.md §1).
func TestRegistry_ApplyAppendsHistory(t *testing.T) {
	rec := NewRegistry(RegistryInjury, "injury_a-cedar", "left knee", "2026-07-01T10:00:00-04:00")
	assert.Equal(t, StatusActive, rec.Status)
	require.Len(t, rec.StatusHistory, 1)
	assert.Equal(t, []string{"left knee"}, rec.Aka)

	// A patch that adds a field and a new display variant, no status change.
	rec = rec.Apply(RegistryPatch{
		DisplayName: "left knee (medial)",
		At:          "2026-07-02T10:00:00-04:00",
		Fields:      map[string]any{"onset": "2025-11", "notes": "meniscus"},
	})
	require.Len(t, rec.StatusHistory, 2, "every patch appends a history entry")
	assert.Equal(t, StatusActive, rec.StatusHistory[1].Status)
	assert.Contains(t, rec.Aka, "left knee (medial)")
	assert.Equal(t, "2025-11", rec.Fields["onset"])
	assert.Equal(t, "2026-07-02T10:00:00-04:00", rec.UpdatedAt)

	// A status transition is recorded.
	rec = rec.Apply(RegistryPatch{Status: StatusManaged, At: "2026-07-05T10:00:00-04:00"})
	assert.Equal(t, StatusManaged, rec.Status)
	require.Len(t, rec.StatusHistory, 3)
	assert.Equal(t, StatusManaged, rec.StatusHistory[2].Status)

	// A null field patch removes the field.
	rec = rec.Apply(RegistryPatch{At: "2026-07-06T10:00:00-04:00", Fields: map[string]any{"onset": nil}})
	assert.NotContains(t, rec.Fields, "onset")
	assert.Equal(t, "meniscus", rec.Fields["notes"])
}

// TestRegistry_ApplyDoesNotMutateReceiver guards the copy-on-write contract.
func TestRegistry_ApplyDoesNotMutateReceiver(t *testing.T) {
	rec := NewRegistry(RegistryPlace, "place_a-river", "Lisbon", "t0")
	_ = rec.Apply(RegistryPatch{Status: StatusResolved, At: "t1", Fields: map[string]any{"lat": 38.7}})
	assert.Equal(t, StatusActive, rec.Status, "receiver status is unchanged")
	assert.Len(t, rec.StatusHistory, 1, "receiver history is unchanged")
	assert.NotContains(t, rec.Fields, "lat")
}

func TestRegistryDir_AndNormalized(t *testing.T) {
	dir, ok := RegistryDir(RegistryInjury)
	assert.True(t, ok)
	assert.Equal(t, "injuries", dir)
	_, ok = RegistryDir("nope")
	assert.False(t, ok)

	var zero Registry
	n := zero.Normalized()
	assert.NotNil(t, n.Aka)
	assert.NotNil(t, n.StatusHistory)
	assert.NotNil(t, n.Fields)
}
