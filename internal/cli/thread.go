package cli

import (
	"fmt"
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
		Args:  requireTextArgs(0, 1, "body-file"),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			// Every free-text field can read stdin; only one can win, so reject
			// two "-" fields before the first read drains the stream.
			if err = ensureThreadSingleStdin(cmd); err != nil {
				return emitErr(cmd, err)
			}
			name, err := resolvePrimaryText(cmd, "thread", "body-file", strings.Join(args, " "))
			if err != nil {
				return emitErr(cmd, err)
			}
			intent, err := resolveOptionalText(cmd, "thread", "intent", flagIntent, "intent-file")
			if err != nil {
				return emitErr(cmd, err)
			}
			note, err := resolveOptionalText(cmd, "thread", "note", flagNote, "note-file")
			if err != nil {
				return emitErr(cmd, err)
			}
			domains, err := resolveThreadDomains(cmd)
			if err != nil {
				return emitErr(cmd, err)
			}
			f := cmd.Flags()
			req := router.ThreadWriteRequest{
				Name: name,
				Now:  clockNow(),
			}
			req.Intent = intent
			req.Domains = domains
			req.Status, _ = f.GetString(flagStatus)
			req.Note = note

			res, err := r.WriteThread(req)
			if err != nil {
				// The root silences returned errors, so a rejected status would
				// otherwise be a bare exit code. A rejection has to say what it
				// wanted (error-states.md §St-1).
				return emitErr(cmd, err)
			}
			return renderRegistryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagIntent, "", "The one-line statement of what this thread is")
	f.StringArray(flagDomain, nil, "A domain this thread touches (repeatable)")
	f.String(flagStatus, "", "Status transition: active | managed | resolved")
	f.String(flagNote, "", "A free-text note kept verbatim")
	registerBodyFileFlag(cmd, "thread name")
	f.String("intent-file", "", "Read the intent from this file (or - for stdin) instead of --intent")
	f.StringArray("domain-file", nil, "Read a domain from this file (or - for stdin), one per flag (repeatable)")
	f.String("note-file", "", "Read the note from this file (or - for stdin) instead of --note")
	return cmd
}

// ensureThreadSingleStdin rejects routing more than one of thread's free-text
// fields to stdin. It folds the repeatable --domain-file into the single-stream
// check alongside the string-valued file flags, so a second "-" on any field is
// caught before the first read drains the stream.
func ensureThreadSingleStdin(cmd *cobra.Command) error {
	named := map[string]string{}
	for _, name := range []string{"body-file", "intent-file", "note-file"} {
		if cmd.Flags().Changed(name) {
			v, _ := cmd.Flags().GetString(name)
			named["--"+name] = v
		}
	}
	if cmd.Flags().Changed("domain-file") {
		paths, _ := cmd.Flags().GetStringArray("domain-file")
		for i, p := range paths {
			named[fmt.Sprintf("--domain-file[%d]", i)] = p
		}
	}
	return ensureSingleStdin(named)
}

// resolveThreadDomains reads the thread's domains from either inline --domain
// values or --domain-file paths (one domain per file), the two being mutually
// exclusive. Each file flows through the shared reader, so a domain carrying a
// shell metacharacter stays data.
func resolveThreadDomains(cmd *cobra.Command) ([]string, error) {
	inline := cmd.Flags().Changed(flagDomain)
	fileChanged := cmd.Flags().Changed("domain-file")
	if inline && fileChanged {
		return nil, fmt.Errorf("lucid thread: give domains via --%s or --domain-file, not both", flagDomain)
	}
	if !fileChanged {
		v, _ := cmd.Flags().GetStringArray(flagDomain)
		return v, nil
	}
	paths, _ := cmd.Flags().GetStringArray("domain-file")
	domains := make([]string, 0, len(paths))
	for _, p := range paths {
		body, err := readBodyFile("domain-file", p, cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("lucid thread: %w", err)
		}
		domains = append(domains, body)
	}
	return domains, nil
}
