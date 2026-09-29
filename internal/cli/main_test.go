package cli

import (
	"io"
	"os"
	"testing"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
)

// TestMain keeps the whole package offline by construction (ADR-0006: no test
// may need a live model). The production buildProvider seam would spawn the
// vendor CLI or dial the local model daemon, and some verbs reach for a model
// without the test asking for one — a plain `gratitude add` consults the
// optional tier-3 judge whenever tier 2 is not confident. So the default here is
// a provider that is never reachable: such a verb takes its documented
// no-model path, deterministically. A test that exercises a model injects a
// scripted provider.Fake (withServeProvider, withGratitudeJudge, …) and restores
// this default when it ends.
//
// Likewise no test may wait on a person: `go test` can hand the test binary the
// developer's real terminal as stdin, and an ambiguous-band `gratitude add` asks
// its question only on a terminal. So stdin is never a terminal here, and a test
// that exercises the question stands one in (withGratitudeTerminal).
func TestMain(m *testing.M) {
	buildProvider = func(config.ProviderConfig) (provider.Provider, error) {
		return &provider.Fake{ExhaustErr: provider.ErrUnavailable}, nil
	}
	gratitudeStdinIsTerminal = func(io.Reader) bool { return false }
	os.Exit(m.Run())
}
