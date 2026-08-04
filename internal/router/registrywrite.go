package router

import (
	"errors"
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
// mention. Onset is a strict-tier date (@yesterday / YYYY-MM-DD / a partial
// YYYY-MM or YYYY — free text is rejected, see [resolveRegistryDate]); the
// remaining fields are free-text testimony stored verbatim under the documented
// Fields keys. Status, when set, is an active/managed/resolved transition
// recorded in the append-only status_history. A zero Now defaults to the wall
// clock.
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

	// The date resolves before the first field is built, so a rejected onset
	// leaves the record byte-unchanged (error-states.md §B-3, §B-4).
	onset, onsetPrec, err := resolveRegistryDate(req.Onset, now)
	if err != nil {
		return RegistryWriteResult{}, err
	}

	fields := map[string]any{}
	if onset != "" {
		fields["onset"] = onset
		fields["onset_precision"] = onsetPrec
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

	// Both bounds resolve before the first field is built, so a rejected end
	// cannot leave a half-applied range behind (error-states.md §B-3, §B-4).
	start, startPrec, err := resolveRegistryDate(req.Start, now)
	if err != nil {
		return RegistryWriteResult{}, err
	}
	end, endPrec, err := resolveRegistryDate(req.End, now)
	if err != nil {
		return RegistryWriteResult{}, err
	}

	fields := map[string]any{}
	if start != "" {
		fields["start"] = start
		fields["start_precision"] = startPrec
	}
	if end != "" {
		fields["end"] = end
		fields["end_precision"] = endPrec
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

// acceptedRegistryDateForms names the forms a registry date flag reads, for the
// error it returns when it cannot read one. These fields are date-granular, so
// the list stops at a day rather than repeating the shared grammar's clock
// forms — a rejection should teach the shape that belongs here.
const acceptedRegistryDateForms = "@yesterday, YYYY-MM-DD, YYYY-MM, or YYYY"

// resolveRegistryDate resolves a registry date field (an injury onset, an era
// start/end) through the shared date grammar (usage/commands.md §"Backdating
// with --day") on its strict tier: a value the grammar cannot read — free text
// naming a season, say — and a day that has not happened yet are both clean
// errors, returned before any field is built so the record stays byte-unchanged
// (error-states.md §B-3, §B-4). An empty arg yields no field. Free text is the
// one capability the unified grammar removed: the phrase now belongs in --note,
// where it reads as testimony rather than as a date.
//
// What the registry *stores* diverges from a capture on purpose
// (life-archive.md §4, "Registry date values"):
//
//   - A partial date is written back exactly as typed — `2014-09` stores
//     "2014-09", `2014` stores "2014" — never the snapped first instant. This
//     is the one place in Lucid that keeps the *degree* of imprecision an
//     observation loses when it files under one real day, because a life
//     chapter or an old injury genuinely has no known day, and that is the
//     record rather than a defect in it. The branch is explicit because it has
//     to be: the shared grammar reads a partial date, so without it the
//     normalizing path below would quietly rewrite "2014-09" to "2014-09-01".
//   - A relative word and a full date normalize to YYYY-MM-DD.
//   - A supplied time is parsed but not stored — `"2014-09-01 19:30"` records
//     "2014-09-01". An explicit truncation, not a silent one.
//
// Precision is always approximate: a registry date is a placeholder for when a
// life event began, never a timestamp. The future ceiling is evaluated on the
// *resolved* instant while the *typed* string is what gets stored, so `2027` is
// rejected via its snapped 2027-01-01 while the current year is accepted and
// stored as typed.
func resolveRegistryDate(arg string, now time.Time) (value, precision string, err error) {
	if strings.TrimSpace(arg) == "" {
		return "", "", nil
	}

	res, err := observations.ResolveDay(arg, now, observations.DayOptions{AllowPartial: true})
	switch {
	case errors.Is(err, observations.ErrDayFuture):
		// The ceiling stays in ResolveDay; this second pass only recovers the
		// day the value resolved to, so the refusal can name it.
		future, _ := observations.ResolveDay(arg, now, observations.DayOptions{
			AllowFuture: true, AllowPartial: true,
		})
		return "", "", fmt.Errorf(
			"cannot record %s — that day has not happened yet; nothing was saved", future.LogicalDate,
		)
	case err != nil:
		return "", "", fmt.Errorf(
			"could not read the date %q (want %s); keep an approximate phrase in --note instead; nothing was saved",
			arg, acceptedRegistryDateForms,
		)
	}

	if res.Granularity != observations.GranularityDay {
		return res.Typed, observations.PrecisionApproximate, nil
	}
	return observations.DateString(res.OccurredAt), observations.PrecisionApproximate, nil
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
