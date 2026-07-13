package storage

import "testing"

// benchDoc returns a representative raw-entry document: a full frontmatter
// block plus a short body, matching what SplitFrontmatter/ParseFrontmatter see
// on every raw-entry read.
func benchDoc() []byte {
	return []byte(`---
id: raw_2026_07_05_18_41
recorded_at: 2026-07-05T18:41:39Z
occurred_at: 2026-07-05T18:41:39Z
occurred_at_precision: minute
source: cli
session_id: session_2026_07_05_18_41
command: /log
agent_versions:
  intake: null
bootstrap: false
---
the knee flared again on the evening walk
`)
}

// BenchmarkSplitFrontmatter tracks the cost of the fence split that precedes
// every raw-entry decode. It does not mutate its input, so the same bytes are
// reused across iterations.
func BenchmarkSplitFrontmatter(b *testing.B) {
	doc := benchDoc()
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = SplitFrontmatter(doc)
	}
}

// BenchmarkParseFrontmatter tracks the split-plus-YAML-decode cost — the full
// frontmatter read path used by ValidateRawFrontmatter on each write.
func BenchmarkParseFrontmatter(b *testing.B) {
	doc := benchDoc()
	b.ReportAllocs()
	for b.Loop() {
		_, _, _ = ParseFrontmatter(doc)
	}
}
