package observations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzParseOpenMeteoDaily hardens the one parser that consumes untrusted network
// bytes: the Open-Meteo weather response. For any body it must never panic and
// must never return a non-nil payload alongside an error; a successful parse is
// always source-attributed with the place_ref it was given. Seeds cover a full
// response, empty/partial series, null daily, and non-JSON garbage.
func FuzzParseOpenMeteoDaily(f *testing.F) {
	for _, s := range []string{
		`{}`,
		`{"daily":{"temperature_2m_mean":[20.5],"precipitation_sum":[1.2]}}`,
		`{"daily":{"temperature_2m_mean":[]}}`,
		`{"daily":null}`,
		`{"daily":{"temperature_2m_mean":["x"]}}`,
		``, `not json`, `[]`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		payload, err := ParseOpenMeteoDaily(body, "place_a-cedar")
		if err != nil {
			require.Nil(t, payload, "an error must not carry a partial payload")
			return
		}
		require.NotNil(t, payload)
		require.Equal(t, "place_a-cedar", payload["place_ref"], "a parsed event is always source-attributed")
	})
}
