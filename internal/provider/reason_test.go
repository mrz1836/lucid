package provider_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
)

// TestReportedReason_FindsWrappedReason pins the reason seam: a reason
// attached with WithReason is recovered through any %w chain, plain and nil
// errors report none, and attaching a reason changes neither the error text
// nor its outage classification.
func TestReportedReason_FindsWrappedReason(t *testing.T) {
	base := fmt.Errorf("backend: cli reported is_error: %w", provider.ErrUnavailable)
	direct := provider.WithReason(base, "Not logged in · Please run /login")

	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "direct", err: direct, want: "Not logged in · Please run /login"},
		{name: "double-wrapped", err: fmt.Errorf("compose: %w", fmt.Errorf("call: %w", direct)), want: "Not logged in · Please run /login"},
		{name: "plain error", err: errors.New("plain failure"), want: ""},
		{name: "plain sentinel chain", err: base, want: ""},
		{name: "nil", err: nil, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, provider.ReportedReason(tc.err))
		})
	}

	t.Run("text and classification preserved", func(t *testing.T) {
		assert.Equal(t, base.Error(), direct.Error())
		require.ErrorIs(t, direct, provider.ErrUnavailable)
		assert.True(t, provider.IsOutage(direct))
		assert.True(t, provider.IsOutage(fmt.Errorf("wrap: %w", direct)))
	})

	t.Run("nil and empty reason pass through", func(t *testing.T) {
		require.NoError(t, provider.WithReason(nil, "ignored"))
		assert.Same(t, base, provider.WithReason(base, ""))
	})
}
