package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Registry-write flag names shared across the life-archive write verbs
// (lucid injury / era / thread). Each maps to a documented convention Fields
// key (mvp/life-archive.md §2–§4); every one is optional so a bare
// `lucid injury "left knee"` is a valid first mention. flagStatus and flagNote
// are shared by more than one verb, declared once here.
const (
	flagStatus             = "status"
	flagNote               = "note"
	flagOnset              = "onset"
	flagTimeline           = "timeline"
	flagBodyArea           = "body-area"
	flagCause              = "cause"
	flagSeverity           = "severity"
	flagLastingEffects     = "lasting-effects"
	flagCurrentLimitations = "current-limitations"
	flagTreatments         = "treatments"
	flagUncertainty        = "uncertainty"
)

// registryWriteView is the machine-readable projection shared by the three
// registry-write verbs under --json: the resolved key, display name, status,
// whether this call created the record, and the merged convention Fields. Built
// CLI-side with stable snake_case names so a harness branches on fields rather
// than parsing the ack prose; the router package stays untouched.
type registryWriteView struct {
	Kind        string         `json:"kind"`
	Key         string         `json:"key"`
	DisplayName string         `json:"display_name"`
	Status      string         `json:"status"`
	Created     bool           `json:"created"`
	Fields      map[string]any `json:"fields"`
}

// registryWriteViewOf projects a router result into the stable --json shape,
// always rendering Fields as a (possibly empty) object rather than null so a
// harness can index it unconditionally.
func registryWriteViewOf(res router.RegistryWriteResult) registryWriteView {
	fields := res.Fields
	if fields == nil {
		fields = map[string]any{}
	}
	return registryWriteView{
		Kind:        res.Kind,
		Key:         res.Key,
		DisplayName: res.DisplayName,
		Status:      res.Status,
		Created:     res.Created,
		Fields:      fields,
	}
}

// renderRegistryWrite prints a registry-write result: the --json view, or the
// inventory ack prose. The shared tail of `lucid injury`, `lucid era`, and
// `lucid thread`.
func renderRegistryWrite(cmd *cobra.Command, res router.RegistryWriteResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), registryWriteViewOf(res))
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// newInjuryCmd wires `lucid injury <name> [flags]`: the first user-facing
// registry-write verb (mvp/life-archive.md §2). It creates or amends an injury
// with the convention Fields (timeline, body_area, cause, severity,
// lasting_effects, current_limitations, treatments, uncertainty) plus a
// backdate-aware onset, recording any status transition on the append-only
// status_history. It is dispatch-only over [router.WriteInjury] — deterministic
// and agent-free (architecture P3); the Ledger scaffolds on first use so capture
// never blocks on setup (product-principles.md P10). A name may contain spaces
// (joined from the trailing args, the obs/closeout precedent).
func newInjuryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "injury <name>",
		Short: "Record or amend an injury in your body history",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := router.InjuryWriteRequest{
				Name: strings.Join(args, " "),
				Now:  clockNow(),
			}
			req.Status, _ = f.GetString(flagStatus)
			req.Onset, _ = f.GetString(flagOnset)
			req.Timeline, _ = f.GetString(flagTimeline)
			req.BodyArea, _ = f.GetString(flagBodyArea)
			req.Cause, _ = f.GetString(flagCause)
			req.Severity, _ = f.GetString(flagSeverity)
			req.LastingEffects, _ = f.GetString(flagLastingEffects)
			req.CurrentLimitations, _ = f.GetString(flagCurrentLimitations)
			req.Treatments, _ = f.GetString(flagTreatments)
			req.Uncertainty, _ = f.GetString(flagUncertainty)
			req.Note, _ = f.GetString(flagNote)

			res, err := r.WriteInjury(req)
			if err != nil {
				return err
			}
			return renderRegistryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagStatus, "", "Status transition: active | managed | resolved")
	f.String(flagOnset, "", "When it began: @yesterday, YYYY-MM-DD, or an approximate value like 2014-09")
	f.String(flagTimeline, "", "The arc since onset — flares, surgeries, plateaus")
	f.String(flagBodyArea, "", "Body region, in your words (e.g. left knee)")
	f.String(flagCause, "", "How it happened, as testimony")
	f.String(flagSeverity, "", "Felt severity in your framing (free text, not a clinical scale)")
	f.String(flagLastingEffects, "", "What it left behind")
	f.String(flagCurrentLimitations, "", "What it still stops or shapes today")
	f.String(flagTreatments, "", "What has been tried — PT, injections, rest, rehab")
	f.String(flagUncertainty, "", "What the record is not sure of")
	f.String(flagNote, "", "A free-text note kept verbatim")
	return cmd
}
