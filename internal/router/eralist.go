package router

import (
	"fmt"

	"github.com/mrz1836/lucid/internal/observations"
)

// eralist.go is the read-only enumerator behind `lucid era list`
// (life-archive.md §4). Until now the only way to see an era was to already
// know its key and browse it through `recall --era <key>`; there was no
// side-effect-free way to discover what chapters exist, which made the natural
// discovery command (`era list`) mint an accidental chapter. This closes that
// gap with a pure read.

// EraSummary is one row of the read-only era listing: the resolved key, the
// display name, the raw start/end bounds exactly as stored, and the rendered
// chapter span (through the shared EraSpan helper, so the listing, the write
// ack, and recall cannot drift). It is projection-only — assembled from a
// registry read that writes nothing.
type EraSummary struct {
	Key         string
	DisplayName string
	Start       string
	End         string
	Span        string
}

// ListEras enumerates every era chapter for the read-only `lucid era list`
// surface (life-archive.md §4). It reads the era registry directly through the
// projection seam (store.ReadRegistryKind — the same reader excavate uses),
// which already returns records key-sorted so the render is byte-stable, and
// an honest empty result when the registry directory is absent.
//
// It writes nothing: it deliberately does NOT call prepareObservations /
// ScaffoldObservations, because a read-only listing must touch no bytes — even
// the idempotent scaffold would create the registry tree, and `era list` must
// be safe to run against a home the guard is watching without leaving a trace.
func (r *Router) ListEras() ([]EraSummary, error) {
	recs, err := r.store.ReadRegistryKind(observations.RegistryEra)
	if err != nil {
		return nil, fmt.Errorf("era list: read eras: %w", err)
	}
	out := make([]EraSummary, 0, len(recs))
	for _, rec := range recs {
		start := fieldValue(rec.Fields["start"])
		end := fieldValue(rec.Fields["end"])
		out = append(out, EraSummary{
			Key:         rec.Key,
			DisplayName: rec.DisplayName,
			Start:       start,
			End:         end,
			Span:        eraSpan(start, end),
		})
	}
	return out, nil
}
