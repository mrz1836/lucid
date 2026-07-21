package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Thread write-verb flag names (mvp/life-archive.md §4): a named thing being
// worked on, with an intent and optional domains. flagStatus is shared with
// `lucid injury` (declared in injury.go). There is deliberately no
// progress/percent/streak flag — the obliquity guard is structural.
const (
	flagIntent = "intent"
	flagDomain = "domain"
)

// newThreadCmd wires `lucid thread <name> [--intent …] [--domain …] [--status …]`:
// the registry-write verb for a thread (mvp/life-archive.md §4). A thread's
// progress is the narrative its linked events tell — it has no progress number,
// percent, or streak (the obliquity guard, enforced in [router.WriteThread]).
// --domain may be repeated. Dispatch-only over [router.WriteThread] —
// deterministic and agent-free — reusing the same append-only merge path. A name
// may contain spaces (joined from the trailing args).
func newThreadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thread <name>",
		Short: "Record or amend a thread you're working on",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := router.ThreadWriteRequest{
				Name: strings.Join(args, " "),
				Now:  clockNow(),
			}
			req.Intent, _ = f.GetString(flagIntent)
			req.Domains, _ = f.GetStringArray(flagDomain)
			req.Status, _ = f.GetString(flagStatus)
			req.Note, _ = f.GetString(flagNote)

			res, err := r.WriteThread(req)
			if err != nil {
				return err
			}
			return renderRegistryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagIntent, "", "The one-line statement of what this thread is")
	f.StringArray(flagDomain, nil, "A domain this thread touches (repeatable)")
	f.String(flagStatus, "", "Status transition: active | managed | resolved")
	f.String(flagNote, "", "A free-text note kept verbatim")
	return cmd
}
