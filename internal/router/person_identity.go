package router

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/identity"
	"github.com/mrz1836/lucid/internal/storage"
)

// The identity curation verbs (data-model.md §"Status, claims and move-ref";
// error-states.md §"Person (write verbs)"; ADR-0016): `person dormant`
// (`--restore`), `person claim`, `person release`, and `person move-ref`. They
// are on the strict tier like merge and rename — every rejection is decided
// from the store before any write, ends with the nothing-was-saved clause, and
// exits non-zero — and they append only to the identity log, never to
// people/, contacts/, or processed/. Each successful write returns the
// identity_YYYY_MM_DD_<slot> receipt of the event it appended; a no-op ack
// carries none. All copy is fixed and model-free.

var (
	// ErrPersonClaimConflict — a claim on a form a different active person
	// holds, without --force (§P-10).
	ErrPersonClaimConflict = errors.New("person claim conflict")
	// ErrPersonMoveRefRejected — a move-ref that cannot apply (§P-12).
	ErrPersonMoveRefRejected = errors.New("person move-ref rejected")
)

// The fixed §P-9 copy the identity verbs add.
const (
	personBlankFormReason       = "a name form is required; nothing was saved."
	personReasonNotOneLine      = "a dormant reason is one line; nothing was saved."
	personReasonWithRestoreText = "--reason-file only applies when marking someone dormant, not with --restore; nothing was saved."
)

// PersonRef names one person in an identity receipt: their key and display
// name.
type PersonRef struct {
	PersonKey   string
	DisplayName string
}

// PersonIdentityResult reports one identity curation write. Receipt is empty
// on a no-op. Status is set on dormant / restore, Form on claim / release,
// Displaced only on a forced claim, and RawID / From / To on move-ref. Pinned
// lists the held mentions the write settled and StillHeld how many held
// mentions of the affected forms it left held. Ack is the full prose ack.
type PersonIdentityResult struct {
	Record    storage.PersonRecord
	Receipt   string
	Status    string
	Form      string
	Displaced []PersonRef
	RawID     string
	From      string
	To        string
	Pinned    []storage.PinnedMention
	StillHeld int
	Ack       string
}

// PersonDormantRequest is one `lucid person dormant` intent: mark Subject
// dormant, or active again when Restore is set. Reason is the optional one-line
// --reason-file text (dormant only). Now is the event instant (zero falls back
// to the wall clock).
type PersonDormantRequest struct {
	Subject string
	Restore bool
	Reason  string
	Now     time.Time
}

// PersonDormant executes `lucid person dormant <subject> [--reason-file]
// [--restore]`: mark a person not who you mean now, or active again. It
// appends one status event and pins every held mention of the person's forms
// that now resolves to exactly one person; nothing else changes, so every
// mention, entry_refs id, contact event, user-authored field, and off-limits
// mark is kept. The subject is record-targeted; --restore considers only
// dormant records, so it reaches an old record whose bare name now means
// someone else. Repeating either form is a no-op ack with no receipt.
func (r *Router) PersonDormant(req PersonDormantRequest) (PersonIdentityResult, error) {
	reason := strings.TrimSuffix(req.Reason, "\r")
	if req.Restore && reason != "" {
		return PersonIdentityResult{}, rejectPerson(ErrPersonRejected, personReasonWithRestoreText)
	}
	if strings.ContainsAny(reason, "\r\n") {
		return PersonIdentityResult{}, rejectPerson(ErrPersonRejected, personReasonNotOneLine)
	}
	reason = strings.TrimSpace(reason)
	subject, err := r.resolvePersonRecord(req.Subject, req.Restore)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	status := identity.StatusDormant
	if req.Restore {
		status = identity.StatusActive
	}
	w, err := r.store.SetPersonStatus(subject.PersonKey, status, reason, whenOr(req.Now))
	if err != nil {
		return PersonIdentityResult{}, fmt.Errorf("person: dormant: %w", err)
	}
	res := PersonIdentityResult{Record: subject, Receipt: w.Receipt, Status: status, Pinned: w.Pinned, StillHeld: w.StillHeld}
	if w.NoOp() {
		res.Ack = fmt.Sprintf("%q is already %s — nothing to change.", subject.DisplayName, status)
		return res, nil
	}

	lines := []string{personStatusAck(subject, req.Restore, w.Receipt)}
	st, err := r.store.IdentityState()
	if err != nil {
		return PersonIdentityResult{}, fmt.Errorf("person: read identity log: %w", err)
	}
	if claims := st.ClaimsBy(subject.PersonKey); len(claims) > 0 {
		lines = append(lines, personClaimsStateLine(claims, req.Restore))
	}
	pinLines, err := r.pinnedLines(w.Pinned, w.StillHeld)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	res.Ack = strings.Join(append(lines, pinLines...), "\n")
	return res, nil
}

// personStatusAck is the first line of a dormant or restore receipt.
func personStatusAck(rec storage.PersonRecord, restore bool, receipt string) string {
	if restore {
		return fmt.Sprintf("%q is active again (%s).", rec.DisplayName, receipt)
	}
	return fmt.Sprintf("%q is now dormant — the record and its history are kept (%s).", rec.DisplayName, receipt)
}

// personClaimsStateLine says what a status write did to the person's claims:
// inert while they are dormant (D5), or active again on restore.
func personClaimsStateLine(claims []identity.Claim, restore bool) string {
	forms := make([]string, len(claims))
	for i, c := range claims {
		forms[i] = fmt.Sprintf("%q", c.Form)
	}
	state := "inert while they are dormant"
	if restore {
		state = "active again"
	}
	if len(forms) == 1 {
		return fmt.Sprintf("Their claim on %s is %s.", forms[0], state)
	}
	return fmt.Sprintf("Their claims on %s are %s.", strings.Join(forms, ", "), state)
}

// PersonClaimRequest is one `lucid person claim` intent: make Form mean
// Subject from now on. Force overrides a different active holder. Now is the
// event instant (zero falls back to the wall clock).
type PersonClaimRequest struct {
	Subject string
	Form    string
	Force   bool
	Now     time.Time
}

// PersonClaim executes `lucid person claim <subject> <form> [--force]`: every
// mention of the form resolved from now on goes to the subject. The claim
// lives in the identity log and never touches aka[], a people record, or a
// past mention. A different active holder of the form — its slug holder, an
// exact display_name/aka holder, or its current claimant — refuses the claim
// (§P-10) unless Force; a dormant holder, a dormant claimant, or someone who
// only shares the first name never does. A standing claimant is released
// first. The write then pins the form's held mentions that now resolve to
// exactly one person. Claiming a form the person already claims is a no-op ack.
func (r *Router) PersonClaim(req PersonClaimRequest) (PersonIdentityResult, error) {
	form, err := identityForm(req.Form)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	subject, err := r.resolvePersonRecord(req.Subject, false)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	w, err := r.store.ClaimForm(subject.PersonKey, form, req.Force, whenOr(req.Now))
	if err != nil {
		var conflict *storage.ClaimConflictError
		if errors.As(err, &conflict) {
			return PersonIdentityResult{}, r.claimConflict(form, conflict.Holders)
		}
		return PersonIdentityResult{}, fmt.Errorf("person: claim: %w", err)
	}
	res := PersonIdentityResult{Record: subject, Receipt: w.Receipt, Form: form, Pinned: w.Pinned, StillHeld: w.StillHeld}
	if w.NoOp() {
		res.Ack = fmt.Sprintf("%q already claims %q — nothing to change.", subject.DisplayName, form)
		return res, nil
	}

	lines := []string{fmt.Sprintf("A bare %q now means %q — earlier mentions stay where they are (%s).",
		form, subject.DisplayName, w.Receipt)}
	if len(w.Displaced) > 0 {
		refs, rerr := r.personRefs(w.Displaced)
		if rerr != nil {
			return PersonIdentityResult{}, rerr
		}
		res.Displaced = refs
		lines = append(lines, fmt.Sprintf("Overrode %s; still active and reached by person_key.", joinPersonRefs(refs)))
	}
	if w.ReleasedDormant != "" {
		refs, rerr := r.personRefs([]string{w.ReleasedDormant})
		if rerr != nil {
			return PersonIdentityResult{}, rerr
		}
		lines = append(lines, fmt.Sprintf("Released the dormant claimant %s.", joinPersonRefs(refs)))
	}
	pinLines, err := r.pinnedLines(w.Pinned, w.StillHeld)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	res.Ack = strings.Join(append(lines, pinLines...), "\n")
	return res, nil
}

// claimConflict builds the §P-10 refusal naming every active holder and the
// remedy.
func (r *Router) claimConflict(form string, holders []string) error {
	refs, err := r.personRefs(holders)
	if err != nil {
		return err
	}
	if len(refs) == 1 {
		return rejectPerson(ErrPersonClaimConflict,
			"the form %q is held by %s, who is active — mark them dormant with lucid person dormant %s, or re-run with --force; nothing was saved.",
			form, joinPersonRefs(refs), refs[0].PersonKey)
	}
	return rejectPerson(ErrPersonClaimConflict,
		"the form %q is held by other active people — %s; mark each dormant with lucid person dormant <key>, or re-run with --force; nothing was saved.",
		form, joinPersonRefs(refs))
}

// PersonReleaseRequest is one `lucid person release` intent: end Subject's
// claim on Form. Now is the event instant (zero falls back to the wall clock).
type PersonReleaseRequest struct {
	Subject string
	Form    string
	Now     time.Time
}

// PersonRelease executes `lucid person release <subject> <form>`: the form
// stops meaning the subject by claim, for mentions resolved from now on;
// nothing already filed moves. The write then pins the form's held mentions
// that now resolve to exactly one person. Releasing a form the person does not
// claim is a no-op ack.
func (r *Router) PersonRelease(req PersonReleaseRequest) (PersonIdentityResult, error) {
	form, err := identityForm(req.Form)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	subject, err := r.resolvePersonRecord(req.Subject, false)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	w, err := r.store.ReleaseForm(subject.PersonKey, form, whenOr(req.Now))
	if err != nil {
		return PersonIdentityResult{}, fmt.Errorf("person: release: %w", err)
	}
	res := PersonIdentityResult{Record: subject, Receipt: w.Receipt, Form: form, Pinned: w.Pinned, StillHeld: w.StillHeld}
	if w.NoOp() {
		res.Ack = fmt.Sprintf("%q does not claim %q — nothing to release.", subject.DisplayName, form)
		return res, nil
	}
	pinLines, err := r.pinnedLines(w.Pinned, w.StillHeld)
	if err != nil {
		return PersonIdentityResult{}, err
	}
	first := fmt.Sprintf("Released %q from %q — a bare %q no longer means them by claim (%s).",
		form, subject.DisplayName, form, w.Receipt)
	res.Ack = strings.Join(append([]string{first}, pinLines...), "\n")
	return res, nil
}

// PersonMoveRefRequest is one `lucid person move-ref` intent: move RawID's
// mention from From onto To, or — with From empty — pin RawID's one held
// mention to To. Now is the event instant (zero falls back to the wall clock).
type PersonMoveRefRequest struct {
	RawID string
	From  string
	To    string
	Now   time.Time
}

// PersonMoveRef executes `lucid person move-ref <raw_id> [--from <subject>]
// --to <subject>`: repair one misfiled mention with an append-only correction.
// With --from it appends a move_ref event, so every read shows that entry's
// mention under --to only; without it, it pins the entry's one held mention
// (§P-11) to --to. The raw entry, its processed artifact, and both people
// records are never rewritten, and a structuring re-run does not undo it. Both
// subjects are record-targeted; --to must already be a person — a correction
// never creates one, so an unknown --to carries the `person create` pointer
// (§P-4). Every other refusal is §P-12, decided before anything is written: an
// unknown raw id, --from equal to --to, no mention under --from, or — without
// --from — no held mention or more than one. The undo is the reverse move-ref.
func (r *Router) PersonMoveRef(req PersonMoveRefRequest) (PersonIdentityResult, error) {
	rawID := strings.TrimSpace(req.RawID)
	exists, err := r.store.RawExists(rawID)
	if err != nil {
		return PersonIdentityResult{}, fmt.Errorf("person: move-ref: %w", err)
	}
	if !exists {
		return PersonIdentityResult{}, moveRefUnknownRaw(rawID)
	}
	to, err := r.resolvePersonRecord(req.To, false)
	if err != nil {
		if errors.Is(err, ErrPersonUnknownSubject) {
			return PersonIdentityResult{}, rejectPerson(ErrPersonUnknownSubject,
				"no one matches %q — add them first with lucid person create <name>; nothing was saved.",
				strings.TrimSpace(req.To))
		}
		return PersonIdentityResult{}, err
	}
	var from storage.PersonRecord
	if strings.TrimSpace(req.From) != "" {
		if from, err = r.resolvePersonRecord(req.From, false); err != nil {
			return PersonIdentityResult{}, err
		}
	}

	w, err := r.store.MoveRef(rawID, from.PersonKey, to.PersonKey, whenOr(req.Now))
	if err != nil {
		return PersonIdentityResult{}, moveRefRefusal(err, rawID, from, to)
	}
	res := PersonIdentityResult{
		Record: to, Receipt: w.Receipt, RawID: rawID, From: from.PersonKey, To: to.PersonKey,
		Pinned: w.Pinned, StillHeld: w.StillHeld,
	}
	if from.PersonKey == "" {
		res.Ack = fmt.Sprintf("Pinned the held %q in %s to %s (%s).",
			w.Pinned[0].Form, rawID, personRefOf(to), w.Receipt)
		return res, nil
	}
	res.Ack = fmt.Sprintf("Moved the mention in %s from %s to %s (%s).",
		rawID, personRefOf(from), personRefOf(to), w.Receipt)
	return res, nil
}

// moveRefRefusal maps a storage move-ref refusal to its fixed §P-12 copy.
// Anything that is not a refusal travels on wrapped.
func moveRefRefusal(err error, rawID string, from, to storage.PersonRecord) error {
	var several *storage.MoveRefHeldError
	switch {
	case errors.Is(err, storage.ErrMoveRefUnknownRaw):
		return moveRefUnknownRaw(rawID)
	case errors.Is(err, storage.ErrMoveRefSamePerson):
		return rejectPerson(ErrPersonMoveRefRejected,
			"--from and --to are the same person (%q); nothing was saved.", to.DisplayName)
	case errors.Is(err, storage.ErrMoveRefNoMention):
		return rejectPerson(ErrPersonMoveRefRejected,
			"%s has no mention under %s to move; nothing was saved.", rawID, personRefOf(from))
	case errors.Is(err, storage.ErrMoveRefNoHeld):
		return rejectPerson(ErrPersonMoveRefRejected,
			"%s has no held mention — name the person it is filed under with --from; nothing was saved.", rawID)
	case errors.As(err, &several):
		forms := make([]string, len(several.Forms))
		for i, f := range several.Forms {
			forms[i] = fmt.Sprintf("%q", f)
		}
		return rejectPerson(ErrPersonMoveRefRejected,
			"%s has more than one held mention (%s) — settle them with lucid person claim or lucid person dormant; nothing was saved.",
			rawID, strings.Join(forms, ", "))
	}
	return fmt.Errorf("person: move-ref: %w", err)
}

// moveRefUnknownRaw is the §P-12 refusal for a raw id no captured entry has.
func moveRefUnknownRaw(rawID string) error {
	return rejectPerson(ErrPersonMoveRefRejected, "there is no entry %s — check the raw id; nothing was saved.", rawID)
}

// personRefOf renders one record as `"Name" (key)`.
func personRefOf(rec storage.PersonRecord) string {
	return joinPersonRefs([]PersonRef{{PersonKey: rec.PersonKey, DisplayName: rec.DisplayName}})
}

// identityForm trims a claim or release form and refuses one with no letter or
// digit, or one that spans lines (§P-9).
func identityForm(raw string) (string, error) {
	form := strings.TrimSpace(raw)
	if identity.NormalizeForm(form) == "" || strings.ContainsAny(form, "\r\n") {
		return "", rejectPerson(ErrPersonRejected, personBlankFormReason)
	}
	return form, nil
}

// pinnedLines renders the settled-held-mentions block of a receipt: the pinned
// mentions, then how many are still held, each part left out when its count is
// zero.
func (r *Router) pinnedLines(pinned []storage.PinnedMention, stillHeld int) ([]string, error) {
	var lines []string
	if n := len(pinned); n > 0 {
		lines = append(lines, fmt.Sprintf("Pinned %d held mention%s:", n, pluralS(n)))
		for _, p := range pinned {
			refs, err := r.personRefs([]string{p.PersonKey})
			if err != nil {
				return nil, err
			}
			lines = append(lines, fmt.Sprintf("- %s %q → %s", p.RawID, p.Form, joinPersonRefs(refs)))
		}
	}
	switch {
	case stillHeld == 1:
		lines = append(lines, "1 mention is still held — see lucid person reconcile.")
	case stillHeld > 1:
		lines = append(lines, fmt.Sprintf("%d mentions are still held — see lucid person reconcile.", stillHeld))
	}
	return lines, nil
}

// personRefs reads each key's display name, in the order given.
func (r *Router) personRefs(keys []string) ([]PersonRef, error) {
	refs := make([]PersonRef, 0, len(keys))
	for _, k := range keys {
		rec, found, err := r.store.ReadPerson(k)
		if err != nil {
			return nil, fmt.Errorf("person: read %q: %w", k, err)
		}
		name := k
		if found {
			name = rec.DisplayName
		}
		refs = append(refs, PersonRef{PersonKey: k, DisplayName: name})
	}
	return refs, nil
}

// joinPersonRefs renders people as `"Name" (key)`, joined by " / ".
func joinPersonRefs(refs []PersonRef) string {
	parts := make([]string, len(refs))
	for i, ref := range refs {
		parts[i] = fmt.Sprintf("%q (%s)", ref.DisplayName, ref.PersonKey)
	}
	return strings.Join(parts, " / ")
}

// pluralS returns "s" for any count but one.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
