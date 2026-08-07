package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// Flag names for the person curation verbs.
const (
	personDobFlag          = "dob"
	personRelationshipFlag = "relationship"
	personNoteFlag         = "note"
	personRestoreFlag      = "restore"
)

// errPersonNotRecorded maps a deterministically rejected person-curation verb to
// a non-zero exit. The fixed reason is already printed to stderr; this sentinel
// keeps a script from reading a rejected write as success (mirrors
// errSelfNotRecorded). Every write verb shares it.
var errPersonNotRecorded = errors.New("lucid: person write not recorded")

// personRejection maps the router's deterministic rejection to the shared exit
// shape: the fixed reason on stderr, a non-zero exit, and nothing written.
// Anything else is a real failure and travels on unchanged.
func personRejection(cmd *cobra.Command, err error) error {
	var rejected *router.PersonRejectedError
	if errors.As(err, &rejected) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return errPersonNotRecorded
	}
	return err
}

// personWriteView is the machine-readable projection of a curation write under
// --json (ADR-0007). It is built CLI-side with stable snake_case names so a
// shelling harness reads the record and the ack without parsing prose, and never
// sees the raw storage struct. off_limits is present only on the off-limits
// verb; the three optional identity fields follow the on-disk omitempty rule.
type personWriteView struct {
	PersonKey    string   `json:"person_key"`
	DisplayName  string   `json:"display_name"`
	Aka          []string `json:"aka"`
	FirstSeenAt  string   `json:"first_seen_at"`
	LastSeenAt   string   `json:"last_seen_at"`
	EntryRefs    []string `json:"entry_refs"`
	RedirectTo   string   `json:"redirect_to,omitempty"`
	Dob          *string  `json:"dob,omitempty"`
	Relationship *string  `json:"relationship,omitempty"`
	Notes        *string  `json:"notes"`
	OffLimits    *bool    `json:"off_limits,omitempty"`
	Ack          string   `json:"ack"`
}

// projectPersonWrite builds the --json view from a router result, always
// rendering aka/entry_refs as (possibly empty) arrays rather than null so a
// harness can index them unconditionally.
func projectPersonWrite(rec storage.PersonRecord, ack string, offLimits *bool) personWriteView {
	return personWriteView{
		PersonKey:    rec.PersonKey,
		DisplayName:  rec.DisplayName,
		Aka:          orEmptyStrings(rec.Aka),
		FirstSeenAt:  rec.FirstSeenAt.Format(personCandidateDateLayout),
		LastSeenAt:   rec.LastSeenAt.Format(personCandidateDateLayout),
		EntryRefs:    orEmptyStrings(rec.EntryRefs),
		RedirectTo:   rec.RedirectTo,
		Dob:          rec.Dob,
		Relationship: rec.Relationship,
		Notes:        rec.Notes,
		OffLimits:    offLimits,
		Ack:          ack,
	}
}

// orEmptyStrings renders a nil slice as an empty array so --json never emits null
// for aka/entry_refs.
func orEmptyStrings(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// emitPersonWrite renders a curation verb's result: the projected record under
// --json for a harness, the inventory ack as prose otherwise (ADR-0007).
func emitPersonWrite(cmd *cobra.Command, rec storage.PersonRecord, ack string, offLimits *bool) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), projectPersonWrite(rec, ack, offLimits))
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), ack)
	return nil
}

// newPersonMergeCmd builds the `merge` child: fold a duplicate person onto a
// canonical one. The source becomes a redirect tombstone; nothing is deleted.
func newPersonMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge <source> <target>",
		Short: "Fold a duplicate person into a canonical one (redirect, never delete)",
		Long: `merge collapses a person that was split into two records — a nickname and a
full name, a transcription variant — onto one canonical record. The target
absorbs the source's aka[], display name, and entry_refs; the source becomes a
redirect tombstone that forwards to the target, so a later bare mention of the
merged-away form resolves to the one canonical record. Nothing is deleted.
Merging a person into themselves is rejected. Both arguments are subjects — a
person_key or a name (quote a multi-word name).`,
		Args: cobra.ExactArgs(2),
		Example: `  lucid person merge person_s-quiet person_s-river
  lucid person merge Sammy "Sam Rivera" --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.PersonMerge(router.PersonMergeRequest{Source: args[0], Target: args[1]})
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonWrite(cmd, res.Record, res.Ack, nil)
		},
	}
}

// newPersonAliasCmd builds the `alias` child: record another written form and
// plant a redirect tombstone so a future bare mention of it resolves here.
func newPersonAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "alias <subject> <form>",
		Short: "Record another written form of a person",
		Long: `alias records another written form of a person without waiting for it to be
captured, and plants a redirect tombstone at that form's slug so a future bare
mention of it resolves to this person. A form that already belongs to a
different person is rejected — merge them instead if they are the same.
Idempotent.`,
		Args: cobra.ExactArgs(2),
		Example: `  lucid person alias person_s-river Sammy
  lucid person alias "Sam Rivera" Sam --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.PersonAlias(router.PersonAliasRequest{Subject: args[0], Form: args[1]})
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonWrite(cmd, res.Record, res.Ack, nil)
		},
	}
}

// newPersonRenameCmd builds the `rename` child: change the display name, folding
// the old form into aka[]. The person_key does not change.
func newPersonRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <subject> <new-name...>",
		Short: "Change a person's display name (the key is unchanged)",
		Long: `rename changes a person's display name; the old form folds into aka[] and the
person_key does not change — it is a stable id and a mutable address, the same
model as anchor rename and self move. Trailing words join into the new name.
Renaming onto a name that already identifies a different person is rejected —
use merge if they are the same person.`,
		Args: cobra.MinimumNArgs(2),
		Example: `  lucid person rename person_s-river "Sam Rivera"
  lucid person rename Sammy Samuel --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.PersonRename(router.PersonRenameRequest{
				Subject: args[0],
				NewName: strings.Join(args[1:], " "),
			})
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonWrite(cmd, res.Record, res.Ack, nil)
		},
	}
}

// newPersonSetCmd builds the `set` child: record the user-authored durable
// fields. A flag left unset leaves that field unchanged; passing an empty value
// clears it. Identity is never touched.
func newPersonSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <subject>",
		Short: "Record user-authored durable fields (dob, relationship, note)",
		Long: `set records the user-authored durable fields on a person: --dob (a civil
YYYY-MM-DD), --relationship (a free-text label), and --note (free text). These
never touch identity — no key, no aka, no merge — and are the one carve-out to
the extractive-only people record: they exist only because you typed them, and
no agent infers them. A flag left unset leaves that field unchanged; an empty
value clears it. A dob that is not a civil date is rejected.`,
		Args: cobra.MinimumNArgs(1),
		Example: `  lucid person set person_s-river --relationship colleague --dob 1990-04-12
  lucid person set "Sam Rivera" --note "met at the co-op" --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			req := router.PersonSetRequest{Subject: strings.Join(args, " ")}
			if cmd.Flags().Changed(personDobFlag) {
				v, _ := cmd.Flags().GetString(personDobFlag)
				req.Dob = &v
			}
			if cmd.Flags().Changed(personRelationshipFlag) {
				v, _ := cmd.Flags().GetString(personRelationshipFlag)
				req.Relationship = &v
			}
			if cmd.Flags().Changed(personNoteFlag) {
				v, _ := cmd.Flags().GetString(personNoteFlag)
				req.Note = &v
			}
			res, err := r.PersonSet(req)
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonWrite(cmd, res.Record, res.Ack, nil)
		},
	}
	cmd.Flags().String(personDobFlag, "", "User-authored date of birth (YYYY-MM-DD)")
	cmd.Flags().String(personRelationshipFlag, "", "User-authored relationship label (e.g. colleague)")
	cmd.Flags().String(personNoteFlag, "", "User-authored free-text note")
	return cmd
}

// reconcileCandidateView is one row of the reconcile --json output: the pair,
// why they were flagged, and the exact merge to run.
type reconcileCandidateView struct {
	SourceKey      string `json:"source_key"`
	SourceName     string `json:"source_display_name"`
	TargetKey      string `json:"target_key"`
	TargetName     string `json:"target_display_name"`
	Reason         string `json:"reason"`
	SuggestedMerge string `json:"suggested_merge"`
}

// personReconcileView is the machine-readable projection of a reconcile scan.
// Candidates is always a (possibly empty) array, never null.
type personReconcileView struct {
	Candidates []reconcileCandidateView `json:"candidates"`
}

// projectReconcile builds the --json view from a router result.
func projectReconcile(res router.PersonReconcileResult) personReconcileView {
	cands := make([]reconcileCandidateView, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		cands = append(cands, reconcileCandidateView{
			SourceKey:      c.SourceKey,
			SourceName:     c.SourceName,
			TargetKey:      c.TargetKey,
			TargetName:     c.TargetName,
			Reason:         c.Reason,
			SuggestedMerge: fmt.Sprintf("lucid person merge %s %s", c.SourceKey, c.TargetKey),
		})
	}
	return personReconcileView{Candidates: cands}
}

// newPersonReconcileCmd builds the `reconcile` child: a read-only detector that
// lists likely-duplicate people and a suggested merge for each. It changes
// nothing and always exits 0 (an empty result is honest, not an error).
func newPersonReconcileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile",
		Short: "List likely-duplicate people and a suggested merge for each (read-only)",
		Long: `reconcile scans the people records for likely duplicates — a nickname and a
full name split into two records, a transcription variant — and lists each pair
with a suggested merge direction and the exact command to run. It is a pure read:
no model, no writes, byte-stable across runs, and off-limits people are excluded.
Two signals flag a pair: a shared written form (a normalized display_name/aka in
common) or name proximity (a bounded edit distance, or a short diminutive prefix
like sam ⊂ sammy). It changes nothing — you decide whether to merge.`,
		Args: cobra.NoArgs,
		Example: `  lucid person reconcile
  lucid person reconcile --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.PersonReconcile()
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), projectReconcile(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Text)
			return nil
		},
	}
}

// newPersonOffLimitsCmd builds the `off-limits` child: toggle P-3 redaction. It
// is the CLI writer for the off-limits registry, which previously had no verb.
func newPersonOffLimitsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "off-limits <subject>",
		Short: "Mark a person off-limits to inference (--restore clears it)",
		Long: `off-limits marks a person off-limits to inference: while set, lucid person
<name> renders the raw record only (mentions and dates, nothing derived) and the
person is redacted from every agent slice. --restore clears it. Nothing is
deleted — only inference visibility changes. Idempotent: marking someone already
off-limits, or restoring someone who is not, is a no-op ack.`,
		Args: cobra.MinimumNArgs(1),
		Example: `  lucid person off-limits person_s-river
  lucid person off-limits "Sam Rivera" --restore --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			restore, _ := cmd.Flags().GetBool(personRestoreFlag)
			res, err := r.PersonOffLimits(router.PersonOffLimitsRequest{
				Subject: strings.Join(args, " "),
				Restore: restore,
			})
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonWrite(cmd, res.Record, res.Ack, &res.OffLimits)
		},
	}
	cmd.Flags().Bool(personRestoreFlag, false, "Clear the off-limits mark instead of setting it")
	return cmd
}
