package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Flag names for the identity curation verbs.
const (
	personReasonFileFlag = "reason-file"
	personForceFlag      = "force"
	personFromFlag       = "from"
	personToFlag         = "to"
)

// reasonWithRestoreError is the usage refusal for --reason-file alongside
// --restore (exit 2): a reason explains why someone is dormant, so it never
// rides on a restore. Its text is the documented copy (error-states.md
// §"Person (write verbs)"), which ends like every curation refusal.
type reasonWithRestoreError struct{}

// Error returns the fixed, documented refusal.
func (reasonWithRestoreError) Error() string {
	return "--reason-file only applies when marking someone dormant, not with --restore; nothing was saved."
}

// personIdentityRefView is one person a forced claim overrode, under --json.
type personIdentityRefView struct {
	PersonKey   string `json:"person_key"`
	DisplayName string `json:"display_name"`
}

// personIdentityPinView is one held mention a write settled, under --json.
type personIdentityPinView struct {
	RawID     string `json:"raw_id"`
	Form      string `json:"form"`
	PersonKey string `json:"person_key"`
}

// personIdentityWriteView is the one --json shape every identity curation
// write emits (usage/commands.md §"The identity receipt"), in its documented
// field order. receipt_id is left out on a no-op; status only on dormant /
// restore; form only on claim / release; displaced only on a forced claim;
// raw_id / from / to only on move-ref. pinned is always an array and
// still_held always a number, so a harness can read them unconditionally.
type personIdentityWriteView struct {
	ReceiptID   string                  `json:"receipt_id,omitempty"`
	PersonKey   string                  `json:"person_key"`
	DisplayName string                  `json:"display_name"`
	Status      string                  `json:"status,omitempty"`
	Form        string                  `json:"form,omitempty"`
	Displaced   []personIdentityRefView `json:"displaced,omitempty"`
	RawID       string                  `json:"raw_id,omitempty"`
	From        string                  `json:"from,omitempty"`
	To          string                  `json:"to,omitempty"`
	Pinned      []personIdentityPinView `json:"pinned"`
	StillHeld   int                     `json:"still_held"`
	Ack         string                  `json:"ack"`
}

// projectPersonIdentityWrite builds the --json view from a router result.
func projectPersonIdentityWrite(res router.PersonIdentityResult) personIdentityWriteView {
	v := personIdentityWriteView{
		ReceiptID:   res.Receipt,
		PersonKey:   res.Record.PersonKey,
		DisplayName: res.Record.DisplayName,
		Status:      res.Status,
		Form:        res.Form,
		RawID:       res.RawID,
		From:        res.From,
		To:          res.To,
		Pinned:      make([]personIdentityPinView, 0, len(res.Pinned)),
		StillHeld:   res.StillHeld,
		Ack:         res.Ack,
	}
	for _, d := range res.Displaced {
		v.Displaced = append(v.Displaced, personIdentityRefView{PersonKey: d.PersonKey, DisplayName: d.DisplayName})
	}
	for _, p := range res.Pinned {
		v.Pinned = append(v.Pinned, personIdentityPinView{RawID: p.RawID, Form: p.Form, PersonKey: p.PersonKey})
	}
	return v
}

// emitPersonIdentityWrite renders an identity write: the receipt view under
// --json, the prose ack otherwise.
func emitPersonIdentityWrite(cmd *cobra.Command, res router.PersonIdentityResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), projectPersonIdentityWrite(res))
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// newPersonDormantCmd builds the `dormant` child: mark a person not who you
// mean now, or active again with --restore. It appends to the identity log
// only — nothing about the person is deleted or rewritten.
func newPersonDormantCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dormant <subject>",
		Short: "Mark a person dormant — not who you mean now (--restore reverses it)",
		Long: `dormant marks a person not who you mean now, without deleting anything:
once an old "Sam" is dormant, a new bare "Sam" no longer folds into them and goes
to the one active Sam instead. Every mention, entry_refs id, contact event,
user-authored field and off-limits mark is kept, and --restore makes the person
active again, exactly as they were. Dormant and off-limits are independent.

While dormant, a person never wins a bare name against an active person, ranks
last (labeled dormant) in candidate lists, and is left out of contact due, the
Monday who's-due post and person reconcile merge suggestions (--all includes
them). --reason-file records why, as one line shown on the person view (- reads
stdin); it does not combine with --restore. The subject is a person_key or a
name (trailing words join); --restore considers only dormant records. Repeating
either form is a no-op ack with no receipt.`,
		Args: cobra.MinimumNArgs(1),
		Example: `  lucid person dormant Sam --reason-file reason.txt
  lucid person dormant person_s-oak --restore --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			restore, _ := cmd.Flags().GetBool(personRestoreFlag)
			if restore && cmd.Flags().Changed(personReasonFileFlag) {
				return newUsageError(cmd, reasonWithRestoreError{})
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			req := router.PersonDormantRequest{Subject: strings.Join(args, " "), Restore: restore, Now: clockNow()}
			if cmd.Flags().Changed(personReasonFileFlag) {
				path, _ := cmd.Flags().GetString(personReasonFileFlag)
				reason, rerr := readBodyFile(personReasonFileFlag, path, cmd.InOrStdin())
				if rerr != nil {
					return emitErr(cmd, fmt.Errorf("lucid person dormant: %w", rerr))
				}
				req.Reason = reason
			}
			res, err := r.PersonDormant(req)
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonIdentityWrite(cmd, res)
		},
	}
	cmd.Flags().String(personReasonFileFlag, "", "Read a one-line reason from this file (or - for stdin); dormant only")
	cmd.Flags().Bool(personRestoreFlag, false, "Make a dormant person active again")
	return cmd
}

// newPersonClaimCmd builds the `claim` child: make a bare form mean one person
// from now on.
func newPersonClaimCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claim <subject> <form>",
		Short: "Say who a bare name means from now on",
		Long: `claim makes every mention of <form> resolved from now on go to <subject> —
lucid person claim "Sam Rivera" Sam means a bare "Sam" is Sam Rivera. The claim
lives in the identity log and never touches aka[], a people record, or any past
mention: entries already filed stay where they are (move one with move-ref). A
form has at most one claimant.

A claim is refused when a different active person holds the form — its slug
holder, an exact display_name/aka holder, or its current claimant. The refusal
names them and the remedy: make them dormant, or pass --force. A dormant
holder, a dormant claimant, and someone who only shares the first name never
block a claim. Quote a multi-word subject or form. Claiming a form the person
already claims is a no-op ack.`,
		Args: cobra.ExactArgs(2),
		Example: `  lucid person claim "Sam Rivera" Sam
  lucid person claim person_s-river Sam --force --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool(personForceFlag)
			res, err := r.PersonClaim(router.PersonClaimRequest{Subject: args[0], Form: args[1], Force: force, Now: clockNow()})
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonIdentityWrite(cmd, res)
		},
	}
	cmd.Flags().Bool(personForceFlag, false, "Claim the form even though a different active person holds it")
	return cmd
}

// newPersonReleaseCmd builds the `release` child: end a claim.
func newPersonReleaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "release <subject> <form>",
		Short: "End a person's claim on a bare name",
		Long: `release stops <form> meaning <subject> by claim, for mentions resolved from now
on; nothing already filed moves. Releasing a form the person does not claim is a
no-op ack. Quote a multi-word subject or form.`,
		Args: cobra.ExactArgs(2),
		Example: `  lucid person release "Sam Rivera" Sam
  lucid person release person_s-river Sam --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.PersonRelease(router.PersonReleaseRequest{Subject: args[0], Form: args[1], Now: clockNow()})
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonIdentityWrite(cmd, res)
		},
	}
}

// newPersonMoveRefCmd builds the `move-ref` child: repair one misfiled mention
// with an append-only correction, never by hand-editing people/ or processed/.
func newPersonMoveRefCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move-ref <raw_id>",
		Short: "Move one misfiled mention to the person it really was",
		Long: `move-ref repairs one misfiled mention: lucid person move-ref <raw_id> --from
<subject> --to <subject> appends one correction to the identity log, and every
read then shows that entry's mention under --to only — the person view's count,
entry list and insights, the dominance line, the reflect gate panel, the
Reflection slice and reconcile. The raw entry, its processed artifact and both
people records are never rewritten, and a lucid structure --force re-run does not
undo the correction. --to must already be a person (lucid person create first).
The undo is the reverse move-ref.

Leave out --from to settle a held mention — one capture could not assign to a
single person: the entry's one held mention is pinned to --to. The move is
refused, before anything is written, for an unknown raw id, an entry with no
mention under --from, --from equal to --to, or — without --from — an entry with
no held mention or more than one. Each subject is a person_key or a name.`,
		Args: cobra.ExactArgs(1),
		Example: `  lucid person move-ref raw_2026_11_18_21_40 --from Sam --to "Sam Rivera"
  lucid person move-ref raw_2026_11_18_09_00 --to person_s-river --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			from, _ := cmd.Flags().GetString(personFromFlag)
			to, _ := cmd.Flags().GetString(personToFlag)
			res, err := r.PersonMoveRef(router.PersonMoveRefRequest{RawID: args[0], From: from, To: to, Now: clockNow()})
			if err != nil {
				return personRejection(cmd, err)
			}
			return emitPersonIdentityWrite(cmd, res)
		},
	}
	cmd.Flags().String(personFromFlag, "", "The person the mention is filed under now (leave out to settle the entry's one held mention)")
	cmd.Flags().String(personToFlag, "", "The person the mention really was (must already exist)")
	_ = cmd.MarkFlagRequired(personToFlag)
	return cmd
}
