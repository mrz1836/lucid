package keyderive

import "testing"

// Baseline benchmarks for the salted slug derivation core — exercised only
// transitively (through storage person_keys and observations registry keys)
// until now. A floor, not an optimization target.

// BenchmarkDerive measures one salted derivation: normalize a messy name, hash
// the salt+name seed, and map it to a wordlist slug.
func BenchmarkDerive(b *testing.B) {
	wl := []string{"apple", "apricot", "cedar", "flame", "hazel", "marsh", "opal", "quill", "reed", "slate", "thorn", "vale"}
	seed := []byte("key-salt\x00Dr. Alex Rivera-Smith")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Derive("person_", seed, wl); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkResolve measures the common no-collision resolve: the base slug is
// free, so it returns without walking any suffix.
func BenchmarkResolve(b *testing.B) {
	free := OwnerFunc(func(string) (string, bool) { return "", false })
	b.ReportAllocs()
	for b.Loop() {
		_ = Resolve("person_a-cedar", "alex", free)
	}
}

// BenchmarkResolveCollision measures the suffix walk when the base and the first
// suffixes are held by other identities — the worst case the rule handles.
func BenchmarkResolveCollision(b *testing.B) {
	taken := map[string]bool{"person_a-cedar": true, "person_a-cedar-2": true, "person_a-cedar-3": true}
	owner := OwnerFunc(func(k string) (string, bool) {
		if taken[k] {
			return "someone-else", true
		}
		return "", false
	})
	b.ReportAllocs()
	for b.Loop() {
		_ = Resolve("person_a-cedar", "alex", owner)
	}
}
