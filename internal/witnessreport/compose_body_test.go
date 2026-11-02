package witnessreport

import "testing"

// composeGoldenTemplate is the fixed, synthetic operator template the compose
// body characterization goldens are built from. It stands in for an operator's
// witness-report voice file and carries nothing personal.
//
// A test that drives the real Composer with a template FILE reproduces these
// goldens byte for byte when the file holds exactly these bytes:
// composekit.ReadPromptFile returns a file's contents unchanged, and
// composeBody trims any trailing newlines from the template before it joins
// the context block, so the file may also end with extra trailing newlines.
const composeGoldenTemplate = "Write this week's witness report for a small circle of friends.\n" +
	"Keep it warm, plain and short. Speak to them, not about the numbers.\n"

// TestComposeBody_GoldenUnchanged pins the exact bytes of the single user
// message the model composes from — the template, the context header, the
// report digest, and the slot instruction — for each synthetic fixture week.
// The goldens were captured before any optional compose input existed, so a
// later change that leaves the model's input untouched must keep every byte.
func TestComposeBody_GoldenUnchanged(t *testing.T) {
	for _, name := range []string{"strong-week", "quiet-week", "miss-heavy-week"} {
		t.Run(name, func(t *testing.T) {
			r, _ := buildFromFixture(t, name+".json")
			checkGolden(t, name+".compose.golden", composeBody(composeGoldenTemplate, r))
		})
	}
}
