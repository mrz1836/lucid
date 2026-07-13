package observations

import "testing"

// BenchmarkParseMicrolog establishes the throughput baseline for the capture
// hot path (go-essentials §Performance; `magex bench`). ParseMicrolog runs on
// every /log and /obs, so its cost bounds capture latency — a deterministic,
// no-LLM path (P9). The input mixes a scale head, an @-backdate, and a #tag to
// exercise the common branches; ParseMicrolog does not mutate its input, so the
// same value is reused across iterations.
func BenchmarkParseMicrolog(b *testing.B) {
	in := ParseInput{
		Kind:      KindPain,
		Args:      []string{"7", "knee", "@yesterday", "#flare"},
		Now:       now,
		SpelledOK: true,
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = ParseMicrolog(in)
	}
}
