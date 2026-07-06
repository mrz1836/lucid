package intake_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/agents/intake"
)

// TestBundleAuthorship_Acceptable is the "acceptable" example from
// product-principles.md §6: user words preserved, only paragraph breaks
// and a question prefix added. It must clear the ≥90% floor.
func TestBundleAuthorship_Acceptable(t *testing.T) {
	opening := ""
	questions := []string{"What stuck with you?", "How did it land?"}
	answers := []string{
		"The bit where I tried to push back and then dropped it.",
		"Annoyed. A little embarrassed. Not at them, more at myself.",
	}
	bundle := "Q: What stuck with you?\n" +
		"A: The bit where I tried to push back and then dropped it.\n\n" +
		"Q: How did it land?\n" +
		"A: Annoyed. A little embarrassed. Not at them, more at myself."

	score := intake.BundleAuthorship(bundle, opening, questions, answers)
	assert.GreaterOrEqual(t, score, 0.90, "faithful reflow should be ~fully user-authored, got %.3f", score)
}

// TestBundleAuthorship_Borderline is the "borderline" example: a couple of
// invisible connective words ("Afterward"), still ≥90% user-authored.
func TestBundleAuthorship_Borderline(t *testing.T) {
	opening := "The dinner with M. and J. went sideways again."
	answers := []string{
		"I tried to push back and then dropped it. I just kind of agreed.",
		"Annoyed. A little embarrassed.",
	}
	bundle := "The dinner with M. and J. went sideways again. Afterward, the bit " +
		"where I tried to push back and then dropped it. I just kind of agreed. " +
		"Annoyed. A little embarrassed."

	score := intake.BundleAuthorship(bundle, opening, nil, answers)
	assert.GreaterOrEqual(t, score, 0.90, "borderline connective should still pass, got %.3f", score)
}

// TestBundleAuthorship_NotAcceptable is the "not acceptable" example:
// Intake editorialized in third person with invented vocabulary. It must
// fall below the floor so the router rejects it.
func TestBundleAuthorship_NotAcceptable(t *testing.T) {
	opening := "The dinner with M. and J. went sideways again."
	answers := []string{
		"I tried to push back and then dropped it. I just kind of agreed.",
		"Annoyed. A little embarrassed.",
	}
	bundle := "The dinner went poorly because the user felt unable to advocate for " +
		"themselves, leading to a familiar pattern of folding. They reported some " +
		"annoyance afterward."

	score := intake.BundleAuthorship(bundle, opening, nil, answers)
	assert.Less(t, score, 0.90, "editorialized bundle must fail the floor, got %.3f", score)
}

// TestBundleAuthorship_EmptyBundle scores an empty bundle as zero — there
// is nothing user-authored to speak of.
func TestBundleAuthorship_EmptyBundle(t *testing.T) {
	assert.InDelta(t, 0.0, intake.BundleAuthorship("", "opening", nil, nil), 1e-9)
	assert.InDelta(t, 0.0, intake.BundleAuthorship("   \n  ", "opening", nil, nil), 1e-9)
}

// TestBundleAuthorship_DigitsAndAccents confirms the tokenizer counts
// digits and non-ASCII letters as word content: a bundle that echoes the
// user's numbers and accented words stays fully user-authored.
func TestBundleAuthorship_DigitsAndAccents(t *testing.T) {
	opening := "Café at 3pm, café again at 7."
	answers := []string{"Felt 100% present at the café."}
	bundle := "Café at 3pm, café again at 7.\n\nFelt 100% present at the café."

	score := intake.BundleAuthorship(bundle, opening, nil, answers)
	assert.GreaterOrEqual(t, score, 0.90, "digits and accents are user words, got %.3f", score)
}
