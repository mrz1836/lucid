package storage

import (
	"testing"

	"github.com/mrz1836/lucid/data"
)

// BenchmarkNormalizeName tracks the per-mention normalization cost — it runs on
// every person name a capture references.
func BenchmarkNormalizeName(b *testing.B) {
	const name = "María-José O'Neill, PhD"
	b.ReportAllocs()
	for b.Loop() {
		_ = NormalizeName(name)
	}
}

// BenchmarkDerivePersonKey tracks the deterministic person_key derivation
// (normalize + sha256 + two wordlist lookups) that keys every person.
func BenchmarkDerivePersonKey(b *testing.B) {
	wl := data.Wordlist()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = DerivePersonKey("Alex Rivera", wl)
	}
}
