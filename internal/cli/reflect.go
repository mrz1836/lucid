package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// reflectGateArg is the single positional token that switches `lucid reflect`
// to the gate pass (every accepted insight + the deterministic panel); anything
// else is rejected.
const reflectGateArg = "gate"

// recallAnswer is one per-insight answer in the optional stdin/JSON batch a
// harness may pipe into `lucid reflect`. Status is confirmed | softened |
// retired (blank leaves the insight unanswered) and Rule is kept | lapsed |
// retired for a ruled insight. Unrecognized values record nothing (the router
// treats them as unanswered), so a harness never has to pre-validate.
type recallAnswer struct {
	Status string `json:"status,omitempty"`
	Rule   string `json:"rule,omitempty"`
}

// recallBatch is the optional stdin/JSON envelope for `lucid reflect`:
// per-insight answers keyed by insight id. Piping it is entirely optional — an
// empty or interactive stdin leaves every surfaced insight `unanswered` so the
// one-shot verb never blocks on a human (A2).
type recallBatch struct {
	Answers map[string]recallAnswer `json:"answers"`
}

// batchRecall is the non-blocking [router.RecallResponder] backing
// `lucid reflect`. It replays the optional stdin batch keyed by insight id and
// defaults any unlisted surface to `unanswered`, keeping the verb strictly
// one-shot: the router surfaces each insight, this responder answers from the
// batch (or lets it pass), and nothing ever waits on interactive input.
type batchRecall struct {
	answers map[string]recallAnswer
}

// RespondToRecall answers one surfaced insight from the batch, or `unanswered`
// (a zero RecallResponse) when the id was not supplied.
func (b *batchRecall) RespondToRecall(insightID, _ string) (router.RecallResponse, error) {
	if a, ok := b.answers[insightID]; ok {
		return router.RecallResponse{Status: a.Status, Rule: a.Rule}, nil
	}
	return router.RecallResponse{}, nil
}

// reflectSurfaceView is the machine-readable projection of one surfaced insight
// under --json: the id, the full Safety-gated text the user saw, and how it was
// answered (unanswered when no batch entry matched).
type reflectSurfaceView struct {
	InsightID    string `json:"insight_id"`
	Surface      string `json:"surface"`
	ResponseKind string `json:"response_kind"`
	RuleKind     string `json:"rule_kind,omitempty"`
}

// reflectView is the --json projection of a [router.ReflectResult]
// (harness-integration.md §D). It is built CLI-side with stable snake_case
// names so a harness branches on fields rather than parsing prose; the router
// package stays untouched. It never proposes, so there is no new-insight field.
type reflectView struct {
	Scope    string               `json:"scope"`
	RecordID string               `json:"record_id,omitempty"`
	Surfaces []reflectSurfaceView `json:"surfaces"`
	Panel    []string             `json:"panel,omitempty"`
	Message  string               `json:"message,omitempty"`
	Wrote    bool                 `json:"wrote"`
	Fallback bool                 `json:"fallback"`
}

// newReflectCmd wires `lucid reflect [gate]`: the one-shot weekly recall of the
// user's validated insights (and, with `gate`, every accepted insight plus the
// deterministic panel). It is provider-backed — it builds the model backend
// from the lucid.json `provider` block and routes every surfaced resonance line
// through the Safety/Consent gate — but it **never proposes a new pattern**
// (agent-contracts.md §3): it writes only the ISO-week reflection record and any
// rule-status transitions the optional stdin batch supplies. Surfaces default to
// `unanswered`; a harness may pipe a JSON batch of per-insight answers to apply
// confirm / soften / retire (and kept / lapsed for ruled insights) in one shot.
func newReflectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reflect [gate]",
		Short: "Recall your validated insights (never proposes a new pattern)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := reflectScopeFromArgs(args)
			if err != nil {
				return err
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			p, err := buildProvider(r.Config().Provider)
			if err != nil {
				return err
			}
			batch, err := readRecallBatch(cmd.InOrStdin())
			if err != nil {
				return err
			}
			res, err := r.Reflect(cmd.Context(), router.ReflectRequest{
				Scope:     scope,
				Now:       clockNow(),
				Provider:  p,
				Responder: &batchRecall{answers: batch},
			})
			if err != nil {
				return err
			}
			return renderReflect(cmd, res)
		},
	}
	// `week` is the read-only weekly deep-dive — a separate surface that never
	// writes, dispatched ahead of the `[gate]` positional (a non-subcommand arg
	// still runs the parent, so `reflect` and `reflect gate` are unaffected).
	cmd.AddCommand(newReflectWeekCmd())
	return cmd
}

// reflectScopeFromArgs resolves the optional positional token to a scope: no arg
// is the weekly pass, `gate` is the gate pass, and anything else is rejected so
// a typo never silently runs the wrong cadence.
func reflectScopeFromArgs(args []string) (router.ReflectScope, error) {
	if len(args) == 0 {
		return router.ReflectWeek, nil
	}
	if args[0] == reflectGateArg {
		return router.ReflectGate, nil
	}
	return "", fmt.Errorf("lucid reflect: unknown scope %q — use `reflect` or `reflect gate`", args[0])
}

// readRecallBatch reads the optional stdin/JSON answer batch. An interactive
// (terminal) stdin, an empty stream, or a redirected /dev/null all yield an
// empty (non-nil) batch — the one-shot verb never blocks waiting for a human to
// type or close input, and every surfaced insight is left unanswered. Malformed
// JSON on a piped stdin is a real error (a harness sent a bad batch), surfaced
// rather than silently dropped.
func readRecallBatch(in io.Reader) (map[string]recallAnswer, error) {
	empty := map[string]recallAnswer{}
	if stdinIsInteractive(in) {
		return empty, nil
	}
	var batch recallBatch
	if err := json.NewDecoder(in).Decode(&batch); err != nil {
		if errors.Is(err, io.EOF) {
			return empty, nil // empty stdin — no batch supplied
		}
		return nil, fmt.Errorf("lucid reflect: decode answer batch: %w", err)
	}
	if batch.Answers == nil {
		return empty, nil
	}
	return batch.Answers, nil
}

// stdinIsInteractive reports whether r is the process's real stdin attached to a
// terminal — the one case where reading the optional recall batch would block on
// a human. A piped or redirected stdin, or a test reader, is not a char device
// and returns false so the batch is read (and an empty one decodes to nothing).
func stdinIsInteractive(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// renderReflect prints the reflection pass: the --json view, or human-first
// prose (the fixed message if any, each surfaced line, the gate panel, and the
// recorded reflection id).
func renderReflect(cmd *cobra.Command, res router.ReflectResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), reflectViewOf(res))
	}

	out := cmd.OutOrStdout()
	if res.Message != "" {
		_, _ = fmt.Fprintln(out, res.Message)
	}
	for _, s := range res.Surfaces {
		_, _ = fmt.Fprintln(out, s.Surface)
	}
	for _, line := range res.Panel {
		_, _ = fmt.Fprintln(out, line)
	}
	if res.Wrote {
		_, _ = fmt.Fprintf(out, "Recorded reflection %s.\n", res.RecordID)
	}
	return nil
}

// reflectViewOf projects a router result into the stable --json shape.
func reflectViewOf(res router.ReflectResult) reflectView {
	surfaces := make([]reflectSurfaceView, 0, len(res.Surfaces))
	for _, s := range res.Surfaces {
		surfaces = append(surfaces, reflectSurfaceView{
			InsightID:    s.InsightID,
			Surface:      s.Surface,
			ResponseKind: s.ResponseKind,
			RuleKind:     s.RuleKind,
		})
	}
	return reflectView{
		Scope:    string(res.Scope),
		RecordID: res.RecordID,
		Surfaces: surfaces,
		Panel:    res.Panel,
		Message:  res.Message,
		Wrote:    res.Wrote,
		Fallback: res.Fallback,
	}
}
