package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Era write-verb flag names (mvp/life-archive.md §4): a named life chapter with
// an optional, backdate-aware start/end range. flagNote is shared with
// `lucid injury` (declared in injury.go).
const (
	flagStart = "start"
	flagEnd   = "end"
)

// eraWriteView is the machine-readable projection for `lucid era --json`. An era
// is a chapter, not a graded state (life-archive.md §4), so — unlike the shared
// registryWriteView that injury/thread/pet use — it surfaces no status word.
// Instead it renders the chapter span plus its raw start/end bounds, so no era
// output surface presents "active". This is output-only: the stored status
// placeholder is untouched, merely never surfaced. Built CLI-side with stable
// snake_case names so a harness branches on fields rather than parsing prose.
type eraWriteView struct {
	Kind        string         `json:"kind"`
	Key         string         `json:"key"`
	DisplayName string         `json:"display_name"`
	Created     bool           `json:"created"`
	Start       string         `json:"start,omitempty"`
	End         string         `json:"end,omitempty"`
	Span        string         `json:"span,omitempty"`
	Fields      map[string]any `json:"fields"`
}

// eraWriteViewOf projects an era write result into the stable --json shape: the
// chapter span (through the shared router.EraSpan helper, so the write ack,
// recall, and this machine surface cannot drift) plus its raw start/end bounds,
// and Fields as a (possibly empty) object rather than null so a harness can index
// it unconditionally.
func eraWriteViewOf(res router.RegistryWriteResult) eraWriteView {
	fields := res.Fields
	if fields == nil {
		fields = map[string]any{}
	}
	start := fieldString(fields["start"])
	end := fieldString(fields["end"])
	return eraWriteView{
		Kind:        res.Kind,
		Key:         res.Key,
		DisplayName: res.DisplayName,
		Created:     res.Created,
		Start:       start,
		End:         end,
		Span:        router.EraSpan(start, end),
		Fields:      fields,
	}
}

// fieldString reads a string-valued registry Fields value (an era's start/end
// bound is stored as a plain string), trimmed, or "" when absent or not a string.
func fieldString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// newEraCmd wires `lucid era <name> [--start …] [--end …] [--note …]`: the
// registry-write verb for a life chapter (mvp/life-archive.md §4). Stories
// attach to an era via refs.era so the past becomes browsable by chapter rather
// than by a date no one remembers. Either bound may be approximate; an open end
// is a still-running chapter. Dispatch-only over [router.WriteEra] —
// deterministic and agent-free — reusing the same append-only merge path as
// `lucid injury`. A name may contain spaces (joined from the trailing args).
func newEraCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "era <name>",
		Short: "Record or amend a life chapter (era)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := router.EraWriteRequest{
				Name: strings.Join(args, " "),
				Now:  clockNow(),
			}
			req.Start, _ = f.GetString(flagStart)
			req.End, _ = f.GetString(flagEnd)
			req.Note, _ = f.GetString(flagNote)

			res, err := r.WriteEra(req)
			if err != nil {
				// The root silences returned errors, so a rejected --start/--end
				// would otherwise be a bare exit code. A strict date flag has to
				// say what it wanted (error-states.md §B-3, §B-4).
				return emitErr(cmd, err)
			}
			return renderRegistryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagStart, "", "When the chapter began: @yesterday, YYYY-MM-DD, or a partial date like 2014 or 2014-09")
	f.String(flagEnd, "", "When it ended, same forms as --start (omit for a still-running chapter)")
	f.String(flagNote, "", "A free-text note kept verbatim")
	return cmd
}
