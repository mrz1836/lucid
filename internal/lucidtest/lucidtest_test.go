package lucidtest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/lucidtest"
)

func assertDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "%s should be a directory", path)
}

func TestLedger(t *testing.T) {
	t.Run("bare ledger scaffolds at tempdir", func(t *testing.T) {
		home, a := lucidtest.Ledger(t)
		require.NotNil(t, a)
		assert.Equal(t, home, a.Home())
		assertDir(t, home)
	})
	t.Run("nested home roots under .lucid", func(t *testing.T) {
		home, a := lucidtest.Ledger(t, lucidtest.NestedHome())
		require.NotNil(t, a)
		assert.Equal(t, ".lucid", filepath.Base(home))
		assertDir(t, home)
	})
	t.Run("engine and observations subtrees scaffold cleanly", func(t *testing.T) {
		home, a := lucidtest.Ledger(t, lucidtest.WithEngine(), lucidtest.WithObservations())
		require.NotNil(t, a)
		assertDir(t, home)
	})
}

func TestFakeDeliverer(t *testing.T) {
	ctx := context.Background()

	t.Run("records sends, alerts, and read-backs with monotonic ids", func(t *testing.T) {
		f := &lucidtest.FakeDeliverer{}

		id1, err := f.SendReturningID(ctx, "chan", "hello")
		require.NoError(t, err)
		assert.Equal(t, "msg-1", id1)
		id2, err := f.SendReturningID(ctx, "chan", "again")
		require.NoError(t, err)
		assert.Equal(t, "msg-2", id2, "ids increment per send")

		require.NoError(t, f.VerifyPresent(ctx, "chan", id1))
		require.NoError(t, f.Send(ctx, "chan", "alert!"))

		assert.Equal(t, []lucidtest.SendRecord{{Channel: "chan", Text: "hello"}, {Channel: "chan", Text: "again"}}, f.Sends)
		assert.Equal(t, []lucidtest.SendRecord{{Channel: "chan", Text: "alert!"}}, f.Alerts)
		assert.Equal(t, []string{id1}, f.Verifies)
	})

	t.Run("SendErr fails delivery and yields no id", func(t *testing.T) {
		want := errors.New("discord 503")
		f := &lucidtest.FakeDeliverer{SendErr: want}
		id, err := f.SendReturningID(ctx, "chan", "text")
		require.ErrorIs(t, err, want)
		assert.Empty(t, id)
		assert.Len(t, f.Sends, 1, "the attempt is still recorded")
	})

	t.Run("VerifyErr fails every read-back; VerifyErrFor fails one id", func(t *testing.T) {
		blanket := errors.New("all gone")
		f := &lucidtest.FakeDeliverer{VerifyErr: blanket}
		require.ErrorIs(t, f.VerifyPresent(ctx, "chan", "any"), blanket)

		oneGone := errors.New("404 unknown message")
		g := &lucidtest.FakeDeliverer{VerifyErrFor: map[string]error{"msg-1": oneGone}}
		require.ErrorIs(t, g.VerifyPresent(ctx, "chan", "msg-1"), oneGone, "the named id fails")
		require.NoError(t, g.VerifyPresent(ctx, "chan", "msg-2"), "an unnamed id still reads back")
	})
}
