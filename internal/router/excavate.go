package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/lifearchive"
	"github.com/mrz1836/lucid/internal/observations"
)

// excavate.go is the read-only excavation-selection seam (mvp/life-archive.md
// §5–§6): it assembles a projection-only bundle of the injury/era registries and
// the memory events, hands it to the deterministic cluster-selection engine, and
// returns the next cluster plus its generic prompt templates. Like the week
// bundle it reads only through storage-adapter projections — never the raw
// engine/observations/registries trees — and it writes NOTHING beyond the
// idempotent tree scaffold the read already performs (agent-contracts.md; the
// sanctuary boundary). No model runs in any path (architecture P9); an empty or
// thin store degrades to an honest empty result, spending nothing.

// ExcavationBundle is the projection-only input the cluster-selection engine
// reads (mvp/life-archive.md §6): the injury and era registries and every memory
// event, each read through the storage adapter (the only code that touches
// ~/.lucid/). It is a pure read.
type ExcavationBundle struct {
	Injuries []observations.Registry
	Eras     []observations.Registry
	Memories []observations.Event
}

// ExcavateResult is the read-only excavation selection: whether a cluster was
// found, and — when it was — its track (injury/story), the registry key and
// display name it names, the plain-language reason it was chosen, the convention
// gaps to ask about, and the generic prompt templates for it. Nothing is
// persisted; the personal driver acts on this over its own surface.
type ExcavateResult struct {
	Found       bool
	Track       string
	Key         string
	DisplayName string
	Reason      string
	Gaps        []string
	Prompts     []string
}

// BuildExcavationBundle assembles the projection-only excavation bundle for the
// cluster-selection engine. It reads the injury and era registries and the
// memory events through the storage adapter's projection seams (ReadRegistryKind
// / ReadObservationsKind), never the raw sanctuary trees — the same
// sanctuary-safe discipline BuildWeekBundle uses. Nothing is written beyond the
// idempotent observations scaffold.
func (r *Router) BuildExcavationBundle() (ExcavationBundle, error) {
	if err := r.prepareObservations(); err != nil {
		return ExcavationBundle{}, err
	}
	injuries, err := r.store.ReadRegistryKind(observations.RegistryInjury)
	if err != nil {
		return ExcavationBundle{}, fmt.Errorf("excavate: read injuries: %w", err)
	}
	eras, err := r.store.ReadRegistryKind(observations.RegistryEra)
	if err != nil {
		return ExcavationBundle{}, fmt.Errorf("excavate: read eras: %w", err)
	}
	memories, err := r.store.ReadObservationsKind(observations.KindMemory)
	if err != nil {
		return ExcavationBundle{}, fmt.Errorf("excavate: read memories: %w", err)
	}
	return ExcavationBundle{Injuries: injuries, Eras: eras, Memories: memories}, nil
}

// Excavate selects the next cluster to excavate and emits its generic prompts
// (mvp/life-archive.md §5–§6). It builds the projection-only bundle, runs the
// deterministic selection engine over it (auto track: injury while any injury
// has gaps, then story), and returns the cluster + prompts. It is read-only and
// agent-free; an empty or fully-excavated store returns Found: false with no
// model call.
func (r *Router) Excavate(now time.Time) (ExcavateResult, error) {
	now = whenOr(now)
	bundle, err := r.BuildExcavationBundle()
	if err != nil {
		return ExcavateResult{}, err
	}
	cluster, ok := lifearchive.SelectCluster(lifearchive.SelectInput{
		Injuries: bundle.Injuries,
		Eras:     bundle.Eras,
		Memories: bundle.Memories,
		Now:      now,
	})
	if !ok {
		return ExcavateResult{Found: false}, nil
	}
	return ExcavateResult{
		Found:       true,
		Track:       cluster.Track,
		Key:         cluster.Key,
		DisplayName: cluster.DisplayName,
		Reason:      cluster.Reason,
		Gaps:        cluster.Gaps,
		Prompts:     lifearchive.Prompts(cluster),
	}, nil
}
