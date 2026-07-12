package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSource(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "cli", in: "cli", want: "cli"},
		{name: "discord", in: "discord", want: "discord"},
		{name: "enricher form keeps colon", in: "enricher:weather", want: "enricher:weather"},
		{name: "dot dash underscore digits", in: "harness-2.0_beta9", want: "harness-2.0_beta9"},
		{name: "trims and lowercases", in: "  CLI ", want: "cli"},
		{name: "uppercase normalizes", in: "Discord", want: "discord"},
		{name: "empty", in: "", wantErr: true},
		{name: "whitespace only", in: "   ", wantErr: true},
		{name: "space inside token", in: "bad token!", wantErr: true},
		{name: "leading separator", in: ":leading", wantErr: true},
		{name: "illegal punctuation", in: "cli/discord", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeSource(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid source token", "error is descriptive")
				assert.Empty(t, got, "no token is returned on rejection")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestNormalizeSource_Idempotent confirms feeding a normalized token back in
// returns it unchanged — the property the raw/log and observation paths rely
// on when re-validating an already-normalized value.
func TestNormalizeSource_Idempotent(t *testing.T) {
	for _, in := range []string{"cli", "discord", "enricher:weather", "harness-2.0_beta9"} {
		once, err := NormalizeSource(in)
		require.NoError(t, err)
		twice, err := NormalizeSource(once)
		require.NoError(t, err)
		assert.Equal(t, once, twice, "NormalizeSource is idempotent for %q", in)
	}
}
