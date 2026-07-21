package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// registrywrite.go is the user-facing registry-write path for the life-archive
// module (mvp/life-archive.md §2–§4). Until now the only verb that wrote a
// registry was a sticky location (router/observation.go resolvePlace); this
// fills the CLI gap with create/amend verbs for the injury, era, and thread
// registries. Every write is deterministic and agent-free — resolve the salted
// key, build a RegistryPatch of the convention Fields present, and merge through
// the append-only UpdateRegistry path (observations.md §1). No model runs here.

// InjuryWriteRequest carries one `lucid injury` create/amend turn
// (life-archive.md §2, the injury Fields convention). Name is required; every
// other field is optional so a bare `lucid injury "left knee"` is a valid first
// mention. Onset is backdate-aware (@yesterday / YYYY-MM-DD / an approximate
// value like "2014-09"); the remaining fields are free-text testimony stored
// verbatim under the documented Fields keys. Status, when set, is an
// active/managed/resolved transition recorded in the append-only status_history.
// A zero Now defaults to the wall clock.
type InjuryWriteRequest struct {
	Name               string
	Status             string
	Onset              string
	Timeline           string
	BodyArea           string
	Cause              string
	Severity           string
	LastingEffects     string
	CurrentLimitations string
	Treatments         string
	Uncertainty        string
	Note               string
	Now                time.Time
}

// EraWriteRequest carries one `lucid era` create/amend turn (life-archive.md
// §4): a named life chapter with an optional, backdate-aware start/end range
// (either may be approximate; an open end is a still-running chapter). Stories
// attach to an era via refs.era so the past becomes browsable by chapter.
type EraWriteRequest struct {
	Name  string
	Start string
	End   string
	Note  string
	Now   time.Time
}

// ThreadWriteRequest carries one `lucid thread` create/amend turn
// (life-archive.md §4): a named thing being worked on, with a one-line Intent
// and optional Domains. The obliquity guard is structural — a thread has no
// progress number, percent, or streak — so the write path only ever sets
// intent/domains/note and defensively strips any progress-shaped field.
type ThreadWriteRequest struct {
	Name    string
	Intent  string
	Domains []string
	Status  string
	Note    string
	Now     time.Time
}

// RegistryWriteResult reports what a registry-write verb persisted and the
// inventory ack to show. Created is true when this call first minted the record
// (its status_history has exactly one entry); Fields is the merged result after
// the patch. Kind is the registry kind (injury/era/thread).
type RegistryWriteResult struct {
	Kind        string
	Key         string
	DisplayName string
	Status      string
	Created     bool
	Fields      map[string]any
	Ack         string
}

// WriteInjury creates or amends an injury registry record (life-archive.md §2).
// It resolves the salted key, records any status transition on the append-only
// history, and stores the convention Fields present — with a backdate-aware
// onset (the observations @-grammar, precision recorded alongside). It is
// deterministic and agent-free; a malformed status is rejected before any write
// (error-states.md §St-1: nothing is saved).
func (r *Router) WriteInjury(req InjuryWriteRequest) (RegistryWriteResult, error) {
	now := whenOr(req.Now)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return RegistryWriteResult{}, fmt.Errorf("injury needs a name; nothing was saved")
	}
	if !validRegistryStatus(req.Status) {
		return RegistryWriteResult{}, fmt.Errorf(
			"unknown injury status %q (want active, managed, or resolved); nothing was saved", req.Status,
		)
	}

	fields := map[string]any{}
	if onset, prec := resolveRegistryDate(req.Onset, now); onset != "" {
		fields["onset"] = onset
		fields["onset_precision"] = prec
	}
	putField(fields, "timeline", req.Timeline)
	putField(fields, "body_area", req.BodyArea)
	putField(fields, "cause", req.Cause)
	putField(fields, "severity", req.Severity)
	putField(fields, "lasting_effects", req.LastingEffects)
	putField(fields, "current_limitations", req.CurrentLimitations)
	putField(fields, "treatments", req.Treatments)
	putField(fields, "uncertainty", req.Uncertainty)
	putField(fields, "note", req.Note)

	return r.writeRegistry(observations.RegistryInjury, name, req.Status, fields, now)
}

// WriteEra creates or amends an era registry record (life-archive.md §4): a
// named chapter with an optional backdate-aware start/end range, each precision
// recorded alongside. It reuses the same append-only merge path as WriteInjury.
func (r *Router) WriteEra(req EraWriteRequest) (RegistryWriteResult, error) {
	now := whenOr(req.Now)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return RegistryWriteResult{}, fmt.Errorf("era needs a name; nothing was saved")
	}

	fields := map[string]any{}
	if start, prec := resolveRegistryDate(req.Start, now); start != "" {
		fields["start"] = start
		fields["start_precision"] = prec
	}
	if end, prec := resolveRegistryDate(req.End, now); end != "" {
		fields["end"] = end
		fields["end_precision"] = prec
	}
	putField(fields, "note", req.Note)

	// Era carries no status flag — a first mention lands active and stays as-is
	// on amend (life-archive.md §4: an era is a chapter, not a graded state).
	return r.writeRegistry(observations.RegistryEra, name, "", fields, now)
}

// WriteThread creates or amends a thread registry record (life-archive.md §4):
// a named thing being worked on, with an intent and optional domains. The
// obliquity guard is structural — the write path never sets a progress number,
// percent, or streak, and stripObliquityFields defensively removes any such key
// so the guard cannot be bypassed. Reuses the same append-only merge path.
func (r *Router) WriteThread(req ThreadWriteRequest) (RegistryWriteResult, error) {
	now := whenOr(req.Now)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return RegistryWriteResult{}, fmt.Errorf("thread needs a name; nothing was saved")
	}
	if !validRegistryStatus(req.Status) {
		return RegistryWriteResult{}, fmt.Errorf(
			"unknown thread status %q (want active, managed, or resolved); nothing was saved", req.Status,
		)
	}

	fields := map[string]any{}
	putField(fields, "intent", req.Intent)
	if domains := cleanStrings(req.Domains); len(domains) > 0 {
		fields["domains"] = domains
	}
	putField(fields, "note", req.Note)
	stripObliquityFields(fields) // the obliquity guard (life-archive.md §4)

	return r.writeRegistry(observations.RegistryThread, name, req.Status, fields, now)
}

// writeRegistry is the shared create/amend core the three registry-write verbs
// route through: scaffold the trees, resolve the salted collision-suffixed key,
// and merge the patch through the append-only UpdateRegistry path. Created is
// derived from the resulting status_history (exactly one entry ⇒ first mention),
// so no second read is needed to tell create from amend.
func (r *Router) writeRegistry(kind, name, status string, fields map[string]any, now time.Time) (RegistryWriteResult, error) {
	if err := r.prepareObservations(); err != nil {
		return RegistryWriteResult{}, err
	}
	key, err := r.store.ResolveRegistryKey(kind, name)
	if err != nil {
		return RegistryWriteResult{}, fmt.Errorf("could not resolve the %s key; nothing was saved: %w", kind, err)
	}
	rec, err := r.store.UpdateRegistry(kind, key, observations.RegistryPatch{
		DisplayName: name,
		Status:      status,
		At:          now.Format(time.RFC3339),
		Fields:      fields,
	})
	if err != nil {
		return RegistryWriteResult{}, fmt.Errorf("could not save the %s; nothing was saved: %w", kind, err)
	}
	created := len(rec.StatusHistory) == 1
	return RegistryWriteResult{
		Kind:        kind,
		Key:         rec.Key,
		DisplayName: rec.DisplayName,
		Status:      rec.Status,
		Created:     created,
		Fields:      rec.Fields,
		Ack:         registryAck(kind, rec, created),
	}, nil
}

// resolveRegistryDate resolves a backdate-aware registry date field (an injury
// onset, an era start/end) through the shared observations @-grammar. It is
// total — capture never blocks (product-principles.md P10) — so a value the
// grammar does not recognize is kept verbatim as an approximate testimony date
// (the convention's "2014-09" / "spring 2015" case). An empty arg yields no
// field. Historical registry dates are placeholders, so a resolved calendar
// date is recorded at approximate precision, matching `lucid attach --day`.
func resolveRegistryDate(arg string, now time.Time) (value, precision string) {
	body := strings.TrimPrefix(strings.TrimSpace(arg), "@")
	if body == "" {
		return "", ""
	}
	if strings.EqualFold(body, "yesterday") {
		y := observations.DateOf(now).AddDate(0, 0, -1)
		return observations.DateString(y), observations.PrecisionApproximate
	}
	if d, err := observations.ParseDate(body, now.Location()); err == nil {
		return observations.DateString(d), observations.PrecisionApproximate
	}
	return body, observations.PrecisionApproximate
}

// validRegistryStatus reports whether s is empty (no transition) or one of the
// three documented statuses (observations.md §8: active → managed → resolved).
func validRegistryStatus(s string) bool {
	switch s {
	case "", observations.StatusActive, observations.StatusManaged, observations.StatusResolved:
		return true
	default:
		return false
	}
}

// putField sets a Fields key only when its trimmed value is non-empty, so an
// unset flag leaves no key (a bare first mention writes an empty Fields map).
func putField(m map[string]any, key, val string) {
	if v := strings.TrimSpace(val); v != "" {
		m[key] = v
	}
}

// cleanStrings drops blank/whitespace-only entries, returning nil when nothing
// survives — so an unset --domain leaves no domains key.
func cleanStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if v := strings.TrimSpace(s); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// stripObliquityFields deletes any field whose key names a progress metric,
// keeping the thread obliquity guard total (life-archive.md §4): a thread's
// progress is the narrative its linked events tell, never a number. It is a
// structural guard, not a validation error — the write path never sets such a
// key, but a defensive strip means the invariant cannot be bypassed by a stray
// field.
func stripObliquityFields(fields map[string]any) {
	markers := []string{"progress", "percent", "streak"}
	for key := range fields {
		lk := strings.ToLower(key)
		for _, m := range markers {
			if strings.Contains(lk, m) {
				delete(fields, key)
				break
			}
		}
	}
}

// registryAck builds the inventory ack emitted after the write lands: the
// registry kind, display name, resolved key, and status in force — no score, no
// evaluation (observations.md §0). A first mention reads "Recorded", an amend
// reads "Updated", so provenance (what changed) is legible without magic.
func registryAck(kind string, rec observations.Registry, created bool) string {
	verb := "Updated"
	if created {
		verb = "Recorded"
	}
	return fmt.Sprintf("%s %s %q as `%s` (%s).", verb, kind, rec.DisplayName, rec.Key, rec.Status)
}
