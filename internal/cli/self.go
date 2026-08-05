package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
)

// historyFlag switches `lucid self` from the folded profile to the append log
// behind it. It lives on the read surface only — the write verbs append, so
// there is no history for them to ask for.
const historyFlag = "history"

// selfIndent prefixes a fact row under its namespace header.
const selfIndent = "  "

// errSelfNotRecorded maps a deterministically rejected self verb to a non-zero
// exit. The fixed reason is already printed to stderr; this sentinel just keeps
// a script from reading a rejected write as success (mirrors
// errAnchorNotRecorded). All three write verbs share it: whether the rejection
// was about the input (an unknown namespace, an empty value, an unreadable
// --since) or about the state of the store (which fact a bare key means), the
// caller's contract is the same — nothing was appended, and the exit says so.
var errSelfNotRecorded = errors.New("lucid: self fact not recorded")

// selfRejection maps the router's two deterministic rejection types to the
// shared exit shape: the fixed reason on stderr, a non-zero exit, and nothing
// written. [router.ErrSelfRejected] gates a verb's input;
// [router.SelfRejectedError] gates the state of the store. Anything else is a
// real failure and travels on unchanged.
func selfRejection(cmd *cobra.Command, err error) error {
	var rejected *router.SelfRejectedError
	if errors.Is(err, router.ErrSelfRejected) || errors.As(err, &rejected) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return errSelfNotRecorded
	}
	return err
}

// selfProfileView is the machine-readable projection of the folded profile
// under --json. Facts is always a (possibly empty) array rather than null, so a
// harness can index it unconditionally — the personView precedent.
type selfProfileView struct {
	Facts []engine.SelfFactRecord `json:"facts"`
}

// selfHistoryView is the machine-readable projection of the append log under
// --json --history. It is a distinct shape from [selfProfileView] on purpose: a
// caller asking for history is asking a different question, and the records it
// gets back include retirements and superseded values that must never be
// mistaken for the profile.
type selfHistoryView struct {
	History []engine.SelfFactRecord `json:"history"`
}

// projectSelfFacts renders a set of stored records as their published JSON
// form, always as a (possibly empty) array.
func projectSelfFacts(facts []engine.SelfFact) []engine.SelfFactRecord {
	out := make([]engine.SelfFactRecord, 0, len(facts))
	for _, f := range facts {
		out = append(out, engine.ProjectSelfFact(f))
	}
	return out
}

// newSelfCmd wires `lucid self` (engine-module.md §Commands): the durable
// self-profile — the Ledger's one semantic surface, beside the episodic ones.
//
// Unlike `anchor`, the parent is itself a read rather than a bare group: the
// profile is the thing a user wants most often, so `lucid self` renders it and
// the write verbs hang beneath. The read never scaffolds (Router.SelfProfile),
// so it answers on a Ledger that predates this store instead of writing one.
func newSelfCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "self [<key-or-prefix>]",
		Short: "Read and record durable facts about yourself",
		Long: `self is the durable self-profile: atemporal attributes of the subject —
what is simply true of you, rather than something that happened. Everything
else in the Ledger is episodic and already has a home: an event belongs in the
journal, a chapter in ` + "`era`" + `, a body event in ` + "`injury`" + `, another person in
` + "`person`" + `.

Keys are namespaced ` + "`<namespace>.<leaf>`" + ` over a fixed set — identity., body.,
constraint., pref. — plus misc. for anything true and durable the other four do
not fit. The leaf is free-form and may itself contain dots. An unrecognized
namespace is rejected with the accepted set named, so a fact is never turned
away for want of a category.

Each fact carries a stable id, and the key is a mutable address rather than the
identity. The store is append-only and the latest record per id wins, so
correcting a value is just another ` + "`set`" + `, re-categorizing is ` + "`move`" + `, and
neither forks the fact's history. Nothing is ever deleted.

A value that changes on a schedule — a daily weight, a weekly reading — is an
observation, not an attribute: record it with ` + "`lucid obs`" + `. This store tolerates
the occasional correction, not routine churn.

With no argument it renders the whole active profile grouped by namespace. An
argument that exactly matches an active key reads that one fact; anything else
filters by prefix.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  # The whole profile, grouped by namespace.
  lucid self

  # One namespace.
  lucid self body

  # One fact.
  lucid self body.handedness

  # The append log behind a fact, including records written under an older key.
  lucid self body.handedness --history

  # Machine-readable output for a harness.
  lucid self --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			history, _ := cmd.Flags().GetBool(historyFlag)
			res, err := r.SelfProfile(router.SelfProfileRequest{
				Query:   strings.Join(args, " "),
				History: history,
			})
			if err != nil {
				return err
			}
			if history {
				return emit(cmd, selfHistoryView{History: projectSelfFacts(res.History)}, selfHistoryLines(res.History))
			}
			return emit(cmd, selfProfileView{Facts: projectSelfFacts(res.Facts)}, selfProfileLines(res.Facts, args))
		},
	}
	parent.Flags().Bool(historyFlag, false, "Emit the append log behind the selection instead of the folded profile")
	parent.AddCommand(newSelfSetCmd())
	parent.AddCommand(newSelfRetireCmd())
	parent.AddCommand(newSelfMoveCmd())
	return parent
}

// selfProfileLines renders the folded profile grouped by namespace, in the
// engine's priority order — which the router already sorted the facts into, so
// this only has to break the run whenever the namespace changes.
//
// The header carries its trailing dot (`identity.`) so the full key a write
// verb takes is visible as the header plus the row: the grouping stays
// scannable at the hundreds of facts this store is designed to hold, without
// hiding how to address one.
//
// An empty selection prints one honest line naming the remedy rather than an
// empty list or a dangling header.
func selfProfileLines(facts []engine.SelfFact, args []string) []string {
	if len(facts) == 0 {
		return []string{selfEmptyLine(args)}
	}
	width := selfLeafWidth(facts)
	lines := make([]string, 0, len(facts)+len(facts))
	ns := ""
	for _, f := range facts {
		if group := selfKeyNamespace(f.Key); group != ns {
			if ns != "" {
				lines = append(lines, "")
			}
			ns = group
			lines = append(lines, group+".")
		}
		lines = append(lines, selfIndent+selfFactRow(f, width))
	}
	return lines
}

// selfEmptyLine explains an empty selection. A profile with nothing in it names
// the remedy — there is no list verb, so the way in has to travel with the
// message — while a query that matched nothing says so and points at the bare
// read, because the store is not necessarily empty.
func selfEmptyLine(args []string) string {
	if query := strings.TrimSpace(strings.Join(args, " ")); query != "" {
		return fmt.Sprintf("No facts recorded under %q. Run `lucid self` to see the whole profile.", query)
	}
	return "No facts recorded yet. Record one with: lucid self set identity.generation elder millennial"
}

// selfFactRow renders one fact under its namespace header: the leaf padded to
// the group's width, the value, and the optional origin and annotation.
func selfFactRow(f engine.SelfFact, width int) string {
	row := fmt.Sprintf("%-*s  %s", width, selfKeyLeaf(f.Key), f.Value)
	if f.Since != "" {
		row += fmt.Sprintf(" (since %s)", f.Since)
	}
	if f.Note != "" {
		row += " — " + f.Note
	}
	return row
}

// selfLeafWidth returns the column width the leaves align to. It is measured
// across the whole selection rather than per namespace so the values line up
// down the page instead of stepping between groups.
func selfLeafWidth(facts []engine.SelfFact) int {
	width := 0
	for _, f := range facts {
		if n := len(selfKeyLeaf(f.Key)); n > width {
			width = n
		}
	}
	return width
}

// selfHistoryLines renders the append log: one line per record, in append
// order, with the record time, the key it was written under, the value, and the
// state when it is a retirement.
//
// The key is printed in full here, unlike the grouped profile, and that is the
// point of the surface: a moved fact's records were written under a previous
// key, so the history is exactly where both keys need to be visible.
func selfHistoryLines(history []engine.SelfFact) []string {
	if len(history) == 0 {
		return []string{"No records in the history for that selection."}
	}
	lines := make([]string, 0, len(history))
	for _, f := range history {
		line := fmt.Sprintf("%s  %s  %s", f.RecordedAt, f.Key, f.Value)
		if f.IsRetired() {
			line += "  [retired]"
		}
		lines = append(lines, line)
	}
	return lines
}

// selfKeyNamespace returns the namespace half of a key, and selfKeyLeaf the
// rest. A key with no dot never validates, so both degrade to printing what is
// there rather than failing a read path on a hand-edited file.
func selfKeyNamespace(key string) string {
	ns, _, ok := strings.Cut(key, ".")
	if !ok {
		return key
	}
	return ns
}

func selfKeyLeaf(key string) string {
	_, leaf, ok := strings.Cut(key, ".")
	if !ok {
		return key
	}
	return leaf
}

// newSelfSetCmd builds the `set` child: record one durable attribute. Trailing
// args join into the value, so quoting is optional for the multi-word values
// this store is full of. Human-first prose ack by default; the recorded fact as
// JSON under --json for scripts (ADR-0007). A rejected input — an unknown
// namespace, an empty or oversized value, an unreadable --since — prints the
// fixed reason on stderr and exits non-zero without writing; the record path is
// model-free.
func newSelfSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value...>",
		Short: "Record a durable fact about yourself",
		Long: `set records one durable attribute: a namespaced key and a value. Trailing
words join into the value with spaces, so quoting is optional —
` + "`lucid self set identity.generation elder millennial`" + ` records the two words as
one value. The annotation and the origin are flags rather than trailing words,
which is the deliberate difference from ` + "`anchor add`" + `: an anchor's date is a
single token so its note can trail, while a self fact's value is the multi-word
part. Because the value is what trails, quote a multi-word --note and put the
flags after the value — otherwise the flag takes one word and the rest joins the
value.

Recording a key an active fact already holds appends under that same fact and
the latest record wins, so a mistyped value and a genuine change are the same
operation. Recording a key whose only holder was retired starts a new fact with
its own id — a freed key, not a resumed fact — and the ack names the earlier
retirement.

--since records an origin and is optional; this is the one write surface in the
CLI where a date is not required at all, because a self fact is atemporal by
definition. A future date is rejected. A partial date is accepted and stored as
typed: --since 1985 stays "1985" and is never snapped to a guessed January 1st,
because nothing derives a day count from a self fact.

A value that changes on a schedule is an observation, not an attribute — record
it with ` + "`lucid obs`" + ` instead.`,
		Args: cobra.MinimumNArgs(2),
		Example: `  # Record a fact (trailing words are joined into the value).
  lucid self set identity.generation elder millennial

  # Correct it later — same key, same fact, new record.
  lucid self set identity.generation xennial

  # Record an origin. A partial date is kept as typed.
  lucid self set identity.birthplace Portland --since 1985

  # Annotate the record (quote a multi-word note; the value is what trails).
  lucid self set constraint.diet.dairy avoid --note "flares the sinuses"

  # Machine-readable output for a harness.
  lucid self set body.handedness left --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			now := clockNow()
			since, err := selfSinceFlag(cmd, now)
			if err != nil {
				// Same shape as the router's deterministic rejection: the fixed
				// reason on stderr, a non-zero exit, and nothing written.
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return errSelfNotRecorded
			}
			note, _ := cmd.Flags().GetString("note")
			res, err := r.SelfSet(router.SelfSetRequest{
				Key:   args[0],
				Value: strings.Join(args[1:], " "),
				Since: since,
				Note:  note,
				Now:   now,
			})
			if err != nil {
				return selfRejection(cmd, err)
			}
			return emitSelfFact(cmd, res.Fact, res.Ack)
		},
	}
	cmd.Flags().String("since", "", "Optional origin for the fact (a partial YYYY or YYYY-MM is kept as typed)")
	cmd.Flags().String("note", "", "Optional annotation stored with the record")
	return cmd
}

// newSelfRetireCmd builds the `retire` child: drop a fact that has stopped
// being true. The retirement is an append like every other write on this store,
// so nothing is deleted and the record stays auditable. Human-first prose ack by
// default; the appended record as JSON under --json for scripts (ADR-0007). A
// key no active fact holds is rejected with the fixed reason on stderr and a
// non-zero exit, and nothing is appended.
func newSelfRetireCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retire <key> [reason...]",
		Short: "Retire a fact that has stopped being true (the record is kept)",
		Long: `retire drops a fact from the profile: it stops being part of who you are
now, and nothing is deleted. The store is append-only, so a retirement is a new
record mirroring the fact plus the optional trailing reason and the retired
state. The fact carries its own origin into that record rather than the day it
was retired, so the record reads the same as every other one in the store.

The key leaves the rendered profile, the JSON profile, and the ` + "`ask`" + ` grounding
slice. It stays in the history in full and is still read by --history.

A bare key is enough, because at most one active fact holds one. Setting the key
again later is supported and starts a new fact under that freed key, with its own
id, so the two stay distinct in the ledger. A key no active fact holds — never
recorded, or already retired — is rejected with the recorded keys named, and
nothing is appended.`,
		Args: cobra.MinimumNArgs(1),
		Example: `  # Retire a fact that no longer holds.
  lucid self retire pref.editor

  # Say why (trailing words are joined into the record's note).
  lucid self retire pref.editor switched years ago

  # Machine-readable output for a harness.
  lucid self retire pref.editor --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.SelfRetire(router.SelfRetireRequest{
				Key:    args[0],
				Reason: strings.Join(args[1:], " "),
				Now:    clockNow(),
			})
			if err != nil {
				return selfRejection(cmd, err)
			}
			return emitSelfFact(cmd, res.Fact, res.Ack)
		},
	}
}

// newSelfMoveCmd builds the `move` child: file an existing fact under a
// different key. It takes exactly two arguments and no trailing reason —
// nothing is being retired, so there is nothing to explain (the same argument
// `anchor rename` makes). Human-first prose ack by default; the moved record as
// JSON under --json for scripts (ADR-0007). Every rejection prints the fixed
// reason on stderr, exits non-zero, and appends nothing.
func newSelfMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move <key> <new-key>",
		Short: "Re-categorize a fact under a different key (its history carries forward)",
		Long: `move re-categorizes a fact. The key is a mutable address rather than the
identity, so this is one more append under the same id: the value, the origin
and the annotation all carry forward, and the fact's whole history — including
every record written under the previous key — stays reachable.

That is what makes the taxonomy repairable: no key-naming decision is a one-way
door. A fact filed under one namespace on the day it was recorded can be moved
years later without orphaning a record or forking the fact in two.

A new key an active fact already holds is rejected; a key held only by a retired
fact is free, exactly as it is for ` + "`self set`" + `. A key no active fact holds —
never recorded, or already retired — is rejected with the recorded keys named.`,
		Args: cobra.ExactArgs(2),
		Example: `  # File a fact under a better-fitting namespace.
  lucid self move misc.handedness body.handedness

  # Machine-readable output for a harness.
  lucid self move misc.handedness body.handedness --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.SelfMove(router.SelfMoveRequest{
				Key:    args[0],
				NewKey: args[1],
				Now:    clockNow(),
			})
			if err != nil {
				return selfRejection(cmd, err)
			}
			return emitSelfFact(cmd, res.Fact, res.Ack)
		},
	}
}

// emitSelfFact renders a write verb's result: the projected record under --json
// for a harness, the inventory ack as prose otherwise (ADR-0007). The record is
// projected rather than marshaled from the stored struct, so the published
// shape stays the JSON contract rather than whatever the store happens to hold.
func emitSelfFact(cmd *cobra.Command, fact engine.SelfFact, ack string) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), engine.ProjectSelfFact(fact))
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), ack)
	return nil
}

// selfSinceFlag reads `self set --since`, returning the empty string when the
// flag was not given. A self fact is atemporal, so no origin is the normal case
// and an unset flag must not be resolved to today — which is exactly what
// [observations.ResolveDay] does with an empty value.
func selfSinceFlag(cmd *cobra.Command, now time.Time) (string, error) {
	arg, _ := cmd.Flags().GetString("since")
	if strings.TrimSpace(arg) == "" {
		return "", nil
	}
	return resolveSelfSince(arg, now)
}

// resolveSelfSince reads `self set --since` through the shared date grammar
// ([observations.ResolveDay], usage/commands.md §"Backdating with --day") and
// returns the origin the self store records. Both carve-outs are the inverse of
// [resolveAnchorDate]'s, and for the inverse reason:
//
//   - AllowFuture is off. An anchor may be a date you count toward; an origin
//     is by definition something that has already begun, so the ordinary
//     strict-tier ceiling applies.
//   - AllowPartial is on, and a partial value is written back exactly as typed
//     rather than snapped to the period's first day (the registry precedent).
//     Nothing derives a day count from a self fact, so "sometime in 1985" is an
//     honest origin, while recording 1985-01-01 would fabricate a precision the
//     user never claimed.
//
// Every failure is one message naming the value and the day forms this flag
// takes — nothing is written either way (error-states.md §St-1).
func resolveSelfSince(arg string, now time.Time) (string, error) {
	res, err := observations.ResolveDay(arg, now, observations.DayOptions{
		// An origin has by definition already begun.
		AllowFuture: false,
		// "Sometime in 1985" is an honest origin; a snapped day would not be.
		AllowPartial: true,
	})
	switch {
	case errors.Is(err, observations.ErrDayFuture):
		return "", fmt.Errorf(
			"cannot record %q as an origin — that day has not happened yet; nothing was saved", arg,
		)
	case err != nil:
		return "", fmt.Errorf(
			"could not read the origin %q (want %s); nothing was saved", arg, observations.AcceptedDayForms,
		)
	}
	if res.Granularity != observations.GranularityDay {
		return res.Typed, nil
	}
	return observations.DateString(res.OccurredAt), nil
}
