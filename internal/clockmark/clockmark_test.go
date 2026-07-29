package clockmark

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParse_Valid pins the full derived surface of every accepted mark: the
// hour/minute split, minutes-since-midnight, the canonical zero-padded String,
// and the daily Cron rendering. The "7:5", "+7:30", and " 07:30 " rows lock the
// three leniencies Parse inherits from strconv.Atoi and the outer trim.
func TestParse_Valid(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		hour, min  int
		minutes    int
		wantString string
		wantCron   string
	}{
		{"midnight", "00:00", 0, 0, 0, "00:00", "0 0 * * *"},
		{"morning", "07:30", 7, 30, 450, "07:30", "30 7 * * *"},
		{"evening bell", "21:30", 21, 30, 1290, "21:30", "30 21 * * *"},
		{"last minute", "23:59", 23, 59, 1439, "23:59", "59 23 * * *"},
		{"unpadded canonicalizes", "7:5", 7, 5, 425, "07:05", "5 7 * * *"},
		{"outer whitespace trimmed", " 07:30 ", 7, 30, 450, "07:30", "30 7 * * *"},
		{"leading plus accepted", "+7:30", 7, 30, 450, "07:30", "30 7 * * *"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.hour, m.Hour(), "Hour()")
			assert.Equal(t, tc.min, m.Minute(), "Minute()")
			assert.Equal(t, tc.minutes, m.Minutes(), "Minutes()")
			assert.Equal(t, tc.wantString, m.String(), "String()")
			assert.Equal(t, tc.wantCron, m.Cron(), "Cron()")
		})
	}
}

// TestParse_Invalid covers every rejection branch: wrong shape, an extra colon,
// out-of-range or negative fields, non-numeric fields, and — the canonical
// tightening this package introduces — inner whitespace.
func TestParse_Invalid(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"no colon", "12"},
		{"missing minute", "12:"},
		{"missing hour", ":30"},
		{"extra colon", "12:30:00"},
		{"hour too large", "24:00"},
		{"minute too large", "12:60"},
		{"negative hour", "-1:00"},
		{"negative minute", "12:-5"},
		{"non-numeric", "aa:bb"},
		{"non-numeric minute", "12:xx"},
		{"inner whitespace both", "12 : 30"},
		{"inner whitespace hour", "12 :30"},
		{"inner whitespace minute", "12: 30"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.in)
			require.Error(t, err, "expected %q to be rejected", tc.in)
		})
	}
}

// TestMark_ZeroValueIsMidnight documents that the zero Mark is a usable 00:00.
func TestMark_ZeroValueIsMidnight(t *testing.T) {
	var m Mark
	assert.Equal(t, 0, m.Hour())
	assert.Equal(t, 0, m.Minute())
	assert.Equal(t, 0, m.Minutes())
	assert.Equal(t, "00:00", m.String())
	assert.Equal(t, "0 0 * * *", m.Cron())
}

// TestParse_RoundTrip proves String is the exact inverse of Parse for every
// valid mark: Parse(m.String()) recovers m.
func TestParse_RoundTrip(t *testing.T) {
	for h := range 24 {
		for min := range 60 {
			want := Mark{hour: h, minute: min}
			got, err := Parse(want.String())
			require.NoErrorf(t, err, "String()=%q should re-parse", want.String())
			assert.Equalf(t, want, got, "round-trip of %02d:%02d", h, min)
		}
	}
}

// FuzzParse asserts Parse never panics and that every accepted mark satisfies
// the type's invariants: in-range fields, Minutes == h*60+m, and a String that
// re-parses to the same Mark.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{"", "12", "07:30", "12 : 30", "24:00", "23:59", " 1:2 ", "aa:bb", "12:30:99", "+7:-0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := Parse(s)
		if err != nil {
			return
		}
		assert.GreaterOrEqual(t, m.Hour(), 0)
		assert.LessOrEqual(t, m.Hour(), 23)
		assert.GreaterOrEqual(t, m.Minute(), 0)
		assert.LessOrEqual(t, m.Minute(), 59)
		assert.Equal(t, m.Hour()*60+m.Minute(), m.Minutes())
		reparsed, err := Parse(m.String())
		require.NoError(t, err, "canonical String must re-parse")
		assert.Equal(t, m, reparsed, "String round-trip")
	})
}
