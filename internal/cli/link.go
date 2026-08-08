package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// flagTo is the repeatable subject selector shared by link, unlink, annotate,
// and attach: each occurrence is one <kind>:<key> token, split on its first
// colon (flagNote, the annotate note, is reused from the injury verb). It is a
// StringArray, not a StringSlice, so a key that itself contains a comma is one
// token rather than two.
const flagTo = "to"

// linkAppliedView is one subject's outcome in the `link`/`unlink`/`annotate`
// --json surface: the resolved kind and key, the appended event id, and whether
// it was a no-op (a re-link of a live pair, or an unlink of a pair that is not
// live, appends nothing). EventID is empty on a no-op.
type linkAppliedView struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	EventID string `json:"event_id"`
	NoOp    bool   `json:"no_op"`
}

// linkResultView is the `link`/`unlink`/`annotate` --json surface: the resolved
// media id and one applied entry per --to subject, so a harness branches on the
// per-subject outcome rather than parsing the prose ack. Applied is a non-nil
// slice so the shape stays stable ({"media_id":…,"applied":[…]}).
type linkResultView struct {
	MediaID string            `json:"media_id"`
	Applied []linkAppliedView `json:"applied"`
}

// linkResultJSON projects a router link result into its machine surface.
func linkResultJSON(res router.LinkResult) linkResultView {
	applied := make([]linkAppliedView, len(res.Applied))
	for i, a := range res.Applied {
		applied[i] = linkAppliedView{Kind: a.Kind, Key: a.Key, EventID: a.EventID, NoOp: a.NoOp}
	}
	return linkResultView{MediaID: res.MediaID, Applied: applied}
}

// newLinkLikeCmd builds the shared spine of the three association verbs — the
// same positional <media>, repeatable --to grammar, provenance surface, and
// output shaping — parameterized by op. The three named constructors below
// delegate to it so link/unlink/annotate can never drift apart; each is exposed
// under its own name so the command spine stays greppable in root.go.
func newLinkLikeCmd(use, short, long string, op storage.LinkOp) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			// Scaffold the links tree up front so a link never blocks on setup
			// (product-principles.md P10).
			if err = r.Store().ScaffoldLinks(); err != nil {
				return fmt.Errorf("lucid %s: %w", op, err)
			}

			subjects, _ := cmd.Flags().GetStringArray(flagTo)
			note := ""
			if op == storage.LinkOpAnnotate {
				note, _ = cmd.Flags().GetString(flagNote)
			}

			res, err := r.Link(router.LinkRequest{
				Media:    args[0],
				Subjects: subjects,
				Op:       op,
				Note:     note,
				Now:      clockNow(),
				Source:   flagOrEnv(cmd, flagSource, envSource, sourceCLI),
			})
			if err != nil {
				// A refusal (unknown media, unresolvable subject) is a fixed
				// reason the user should read: print it and exit non-zero, since
				// the root silences returned errors.
				return emitErr(cmd, err)
			}

			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), linkResultJSON(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArray(flagTo, nil, "Subject to act on, as <kind>:<key> (repeatable)")
	_ = cmd.MarkFlagRequired(flagTo)
	// The provenance token the event records, through the capture verbs' shared
	// flag > env > default accept surface (a bare terminal call stays cli).
	f.String(flagSource, "", "Harness source token recorded on the link event (overrides "+envSource+")")
	if op == storage.LinkOpAnnotate {
		f.String(flagNote, "", "The annotation text kept verbatim on the event")
		_ = cmd.MarkFlagRequired(flagNote)
	}
	return cmd
}

// newLinkCmd wires `lucid link <media> --to <kind>:<key> [--to …]`: point a
// stored media at anything it is about, correctably and retroactively. A media
// of any age links to any existing subject, one media may link to many
// subjects, and re-linking a live pair is a no-op ack (never an error that
// blocks growth). The immutable media binary and sidecar are never touched.
func newLinkCmd() *cobra.Command {
	return newLinkLikeCmd(
		"link <media>",
		"Link a media to one or more subjects",
		`link points a stored media at anything it is about — a person, an injury,
a specific day, an anchor, a thread — by appending to the append-only link
ledger. The media binary and its sidecar are never touched.

The <media> is the sidecar id (a stored path is accepted and reduced to its
basename). --to is repeatable, so one media can link to several subjects at
once, and each token is <kind>:<key> split on its first colon. A media of any
age links to any existing subject with no recency constraint, and re-linking a
pair that is already live is a no-op ack, exit 0 — an acknowledgement of growth,
not an error.`,
		storage.LinkOpLink,
	)
}

// newUnlinkCmd wires `lucid unlink <media> --to <kind>:<key> [--to …]`: retire a
// link by appending an unlink event — nothing is destroyed, the association just
// stops being live. Unlinking a pair that is not live is a no-op ack, not an
// error.
func newUnlinkCmd() *cobra.Command {
	return newLinkLikeCmd(
		"unlink <media>",
		"Unlink a media from one or more subjects",
		`unlink retires an association by appending an unlink event to the
append-only link ledger. Nothing is destroyed — the full history stays, the pair
just stops being live, and the media binary and sidecar are never touched.

--to is repeatable and takes the same <kind>:<key> grammar as link. Unlinking a
pair that is not currently live is a no-op ack, exit 0, not an error.`,
		storage.LinkOpUnlink,
	)
}

// newAnnotateCmd wires `lucid annotate <media> --to <kind>:<key> --note <text>`:
// attach a note to an association without changing its liveness. annotate is its
// own op — it never implies link and may be applied to a pair that is not
// currently linked, so its notes fold independently of link/unlink state.
func newAnnotateCmd() *cobra.Command {
	return newLinkLikeCmd(
		"annotate <media>",
		"Annotate a media's link to a subject",
		`annotate attaches a free-text note to a (media, subject) association
without changing whether it is linked. It is its own op: it never implies link,
folds independently of link/unlink state, and may be applied to a pair that is
not currently live.

The <media> and repeatable --to grammar match link; --note carries the
annotation text and is required.`,
		storage.LinkOpAnnotate,
	)
}
