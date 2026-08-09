package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

const legacyPetsSelfKey = "misc.pets"

// PetMigrateResult reports the outcome of the one-time misc.pets migration.
// A missing active fact is an idempotent no-op; a value that appears to name
// multiple pets is skipped so the migration never guesses how to split it.
type PetMigrateResult struct {
	Migrated   bool
	PetKey     string
	RetiredKey string
	Skipped    bool
	Reason     string
	Ack        string
}

// MigratePetsFromSelf moves the legacy misc.pets self fact into the pet
// registry, then retires the fact through the normal append-only self path.
// Acting only on an active fact makes a completed migration safe to re-run;
// WritePet's stable salted-key resolution makes a retry after the registry
// write but before retirement amend the same referent rather than duplicate it.
func (r *Router) MigratePetsFromSelf(now time.Time) (PetMigrateResult, error) {
	log, err := r.store.ReadSelfFacts()
	if err != nil {
		return PetMigrateResult{}, err
	}
	latest := engine.LatestSelfFacts(log)
	fact, ok := engine.FindActiveSelfFact(latest, legacyPetsSelfKey)
	if !ok {
		const reason = "no active misc.pets fact"
		return PetMigrateResult{Reason: reason, Ack: "No active misc.pets fact to migrate."}, nil
	}

	name := strings.TrimSpace(fact.Value)
	if strings.ContainsAny(name, ",;\n") {
		const reason = "multi-value misc.pets fact; handle manually"
		return PetMigrateResult{
			Skipped: true,
			Reason:  reason,
			Ack:     "Skipped misc.pets migration: the value appears to name multiple pets; handle it manually.",
		}, nil
	}

	pet, err := r.WritePet(PetWriteRequest{Name: name, Now: now})
	if err != nil {
		return PetMigrateResult{}, err
	}
	if _, err := r.SelfRetire(SelfRetireRequest{
		Key:    legacyPetsSelfKey,
		Reason: fmt.Sprintf("migrated to pet registry (%s)", pet.Key),
		Now:    now,
	}); err != nil {
		return PetMigrateResult{}, err
	}

	return PetMigrateResult{
		Migrated:   true,
		PetKey:     pet.Key,
		RetiredKey: legacyPetsSelfKey,
		Ack:        fmt.Sprintf("Migrated misc.pets to pet registry %s and retired the self fact.", pet.Key),
	}, nil
}
