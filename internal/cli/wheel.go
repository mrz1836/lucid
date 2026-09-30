package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
)

// Flag names for the wheel verbs (wheel.md §7). The eight rating flags are the
// pillar keys themselves ([observations.WheelPillarKeys]).
const (
	wheelNoteFlag                 = "note"
	wheelSuggestedFlag            = "suggested"
	wheelVisionReviewedFlag       = "vision-reviewed"
	wheelVisionReflectionFlag     = "vision-reflection"
	wheelVisionReflectionFileFlag = "vision-reflection-file"
	wheelMonthFlag                = "month"
	wheelInputFlag                = "input"
)

// wheelAddVerb labels the add path's errors.
const wheelAddVerb = "wheel add"

// newWheelCmd wires `lucid wheel` (wheel.md §7): the monthly Wheel of Life —
// your own 1–10 rating of eight life pillars, kept as an append-only snapshot
// per calendar month. It is a thin dispatch group, deterministic and agent-free
// (architecture P9): no wheel path reaches a model, so every subcommand
// completes with no provider and no companion.
//
//	lucid wheel add --health 6 --relationships 7 --career 5 --finances 4 \
//	  --growth 7 --fun 4 --environment 6 --contribution 5 --note health="moving most days"
//	lucid wheel add --input wheel-2026-09.json --json
//
// add records one month's whole wheel and prints the snapshot's receipt id; a
// second add for the same month appends another snapshot that wins on read.
func newWheelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wheel",
		Short: "Keep a monthly Wheel of Life — your own 1–10 on eight life pillars",
	}
	cmd.AddCommand(newWheelAddCmd())
	return cmd
}

// newWheelAddCmd wires `lucid wheel add` (wheel.md §7.1). The wheel comes either
// from flags — the eight required rating flags, repeatable --note and
// --suggested <pillar>=<value> pairs, the vision fields, and --month — or whole
// from a strict JSON document via --input (a file, or - for stdin), which
// excludes every other add flag but --json. A missing, blank, out-of-range, or
// non-integer rating is refused before anything is written and is never filled
// in. The human ack names the month and the receipt and echoes the eight stored
// ratings; --json emits {receipt_id, entry_id, month, amended, scores}.
func newWheelAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Record one month's wheel — all eight pillars, each your own 1–10",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req, err := wheelAddRequest(cmd)
			if err != nil {
				return emitErr(cmd, err)
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			req.Now = clockNow()
			res, err := r.AddWheel(req)
			if err != nil {
				return emitErr(cmd, err)
			}
			return emit(cmd, wheelAddViewOf(res), res.Ack)
		},
	}
	for _, p := range observations.WheelPillars() {
		cmd.Flags().String(p.Key, "", fmt.Sprintf("Your own 1–10 rating for %s (required)", p.Label))
	}
	cmd.Flags().StringArray(wheelNoteFlag, nil, "An optional one-line why for a pillar, as <pillar>=<text> (repeatable)")
	cmd.Flags().StringArray(wheelSuggestedFlag, nil,
		"An optional calibration value for a pillar, as <pillar>=<1-10> (repeatable; never the rating)")
	cmd.Flags().Bool(wheelVisionReviewedFlag, false, "Record that you re-read your vision this month")
	cmd.Flags().String(wheelVisionReflectionFlag, "", "A short reflection on the vision re-read (implies --vision-reviewed)")
	cmd.Flags().String(wheelVisionReflectionFileFlag, "", "Read the vision reflection from a file, or - for stdin")
	cmd.Flags().String(wheelMonthFlag, "", "The month the wheel covers, YYYY-MM (default: the current logical month)")
	cmd.Flags().String(wheelInputFlag, "", "Read the whole wheel from a JSON document (a file, or - for stdin)")
	return cmd
}

// wheelAddRequest builds the add request from the command line: the whole
// document behind --input, else the flags. Every refusal here happens before
// the Ledger is opened, so nothing is written.
func wheelAddRequest(cmd *cobra.Command) (router.AddWheelRequest, error) {
	if !cmd.Flags().Changed(wheelInputFlag) {
		return wheelRequestFromFlags(cmd)
	}
	path, _ := cmd.Flags().GetString(wheelInputFlag)
	if others := wheelFlagsBesideInput(cmd); len(others) > 0 {
		return router.AddWheelRequest{}, fmt.Errorf(
			"invalid argument %q for %q flag: it reads the whole wheel, so %s cannot be given with it; nothing was saved",
			path, "--"+wheelInputFlag, strings.Join(others, ", "),
		)
	}
	data, err := readRawInput(wheelInputFlag, path, cmd.InOrStdin())
	if err != nil {
		return router.AddWheelRequest{}, fmt.Errorf("lucid %s: %w; nothing was saved", wheelAddVerb, err)
	}
	return parseWheelInput(data)
}

// wheelFlagsBesideInput lists every add flag set alongside --input, which
// carries the whole wheel on its own (--json is an output switch, not part of
// the wheel, so it is always allowed).
func wheelFlagsBesideInput(cmd *cobra.Command) []string {
	names := slices.Concat(observations.WheelPillarKeys(), []string{
		wheelNoteFlag, wheelSuggestedFlag, wheelVisionReviewedFlag,
		wheelVisionReflectionFlag, wheelVisionReflectionFileFlag, wheelMonthFlag,
	})
	var set []string
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			set = append(set, "--"+name)
		}
	}
	return set
}

// wheelRequestFromFlags builds the add request from the flag form. All eight
// rating flags are required — a missing one is a usage error naming every
// missing pillar, in cobra's own required-flag dialect so it maps to exit 2 —
// and each must be a plain whole number 1–10. --note and --suggested pairs name
// a pillar each; a pillar named twice in one flag is refused rather than
// silently overwritten.
func wheelRequestFromFlags(cmd *cobra.Command) (router.AddWheelRequest, error) {
	flags := cmd.Flags()
	var missing []string
	for _, key := range observations.WheelPillarKeys() {
		if !flags.Changed(key) {
			missing = append(missing, fmt.Sprintf("%q", key))
		}
	}
	if len(missing) > 0 {
		return router.AddWheelRequest{}, fmt.Errorf(
			"required flag(s) %s not set — every pillar needs your own %d–%d rating; nothing was saved",
			strings.Join(missing, ", "), observations.WheelScoreMin, observations.WheelScoreMax,
		)
	}

	pillars := make(map[string]observations.PillarScore, len(observations.WheelPillarKeys()))
	for _, p := range observations.WheelPillars() {
		raw, _ := flags.GetString(p.Key)
		score, ok := observations.ParseWheelScore(raw)
		if !ok {
			return router.AddWheelRequest{}, fmt.Errorf(
				"invalid argument %q for %q flag: %s needs a whole number from %d to %d; nothing was saved",
				raw, "--"+p.Key, p.Label, observations.WheelScoreMin, observations.WheelScoreMax,
			)
		}
		pillars[p.Key] = observations.PillarScore{Score: score}
	}

	notes, _ := flags.GetStringArray(wheelNoteFlag)
	if err := applyWheelPairs(wheelNoteFlag, notes, pillars, setWheelNote); err != nil {
		return router.AddWheelRequest{}, err
	}
	suggested, _ := flags.GetStringArray(wheelSuggestedFlag)
	if err := applyWheelPairs(wheelSuggestedFlag, suggested, pillars, setWheelSuggested); err != nil {
		return router.AddWheelRequest{}, err
	}

	reflection, err := resolveOptionalText(cmd, wheelAddVerb, "vision reflection",
		wheelVisionReflectionFlag, wheelVisionReflectionFileFlag)
	if err != nil {
		return router.AddWheelRequest{}, err
	}
	reviewed, _ := flags.GetBool(wheelVisionReviewedFlag)
	month, _ := flags.GetString(wheelMonthFlag)
	return router.AddWheelRequest{
		Month:            month,
		Pillars:          pillars,
		VisionReviewed:   reviewed,
		VisionReflection: reflection,
	}, nil
}

// wheelPairSetter applies one <pillar>=<value> pair's value onto its pillar,
// or reports why the value is not acceptable.
type wheelPairSetter func(p *observations.PillarScore, value string) error

// applyWheelPairs applies a repeatable <pillar>=<value> flag's pairs onto
// pillars. A pair without `=`, an unknown pillar, a pillar named twice, or a
// value its setter refuses is a usage error in cobra's invalid-argument dialect.
func applyWheelPairs(
	flag string, pairs []string, pillars map[string]observations.PillarScore, set wheelPairSetter,
) error {
	seen := map[string]bool{}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || !observations.IsWheelPillar(key) {
			return fmt.Errorf(
				"invalid argument %q for %q flag: want <pillar>=<value>, where <pillar> is one of %s; nothing was saved",
				pair, "--"+flag, strings.Join(observations.WheelPillarKeys(), ", "),
			)
		}
		if seen[key] {
			return fmt.Errorf("invalid argument %q for %q flag: %s is named twice; nothing was saved", pair, "--"+flag, key)
		}
		seen[key] = true
		p := pillars[key]
		if err := set(&p, value); err != nil {
			return fmt.Errorf("invalid argument %q for %q flag: %w; nothing was saved", pair, "--"+flag, err)
		}
		pillars[key] = p
	}
	return nil
}

// setWheelNote stores a --note value verbatim; an empty or blank note is a
// mistake, not a request to store nothing.
func setWheelNote(p *observations.PillarScore, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("the note is empty")
	}
	p.Note = value
	return nil
}

// setWheelSuggested stores a --suggested value, which uses the rating's own
// 1–10 scale and validation (wheel.md §5).
func setWheelSuggested(p *observations.PillarScore, value string) error {
	n, ok := observations.ParseWheelScore(value)
	if !ok {
		return fmt.Errorf("a suggestion needs a whole number from %d to %d",
			observations.WheelScoreMin, observations.WheelScoreMax)
	}
	p.Suggested = &n
	return nil
}

// wheelInputDoc is the strict --input document (wheel.md §7.1): the same shape
// as a snapshot minus the receipt fields. Pointer and raw fields tell "absent"
// apart from a zero value, so a missing score reads as missing (and is refused)
// rather than as 0, and a quoted or decimal score is caught by the strict
// integer parse instead of being coerced.
type wheelInputDoc struct {
	Month            *string                     `json:"month"`
	Pillars          map[string]wheelInputPillar `json:"pillars"`
	VisionReviewed   *bool                       `json:"vision_reviewed"`
	VisionReflection *string                     `json:"vision_reflection"`
}

// wheelInputPillar is one pillar in the --input document.
type wheelInputPillar struct {
	Score     json.RawMessage `json:"score"`
	Note      *string         `json:"note"`
	Suggested json.RawMessage `json:"suggested"`
}

// parseWheelInput decodes a --input document into an add request. The document
// is strict: an unknown key, trailing data, or a score or suggestion that is not
// a plain whole number 1–10 is a clean error and nothing is written. A pillar
// whose score is absent (or null) is left out of the request, so the router
// refuses it as missing — never filled in; an unknown pillar key is passed
// through for the router to refuse by name.
func parseWheelInput(data []byte) (router.AddWheelRequest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc wheelInputDoc
	if err := dec.Decode(&doc); err != nil {
		return router.AddWheelRequest{}, fmt.Errorf("lucid %s: --%s is not a valid wheel document: %w; nothing was saved",
			wheelAddVerb, wheelInputFlag, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return router.AddWheelRequest{}, fmt.Errorf(
			"lucid %s: --%s holds more than one JSON document; nothing was saved", wheelAddVerb, wheelInputFlag,
		)
	}

	pillars := make(map[string]observations.PillarScore, len(doc.Pillars))
	for _, key := range slices.Sorted(maps.Keys(doc.Pillars)) {
		in := doc.Pillars[key]
		if !observations.IsWheelPillar(key) {
			pillars[key] = observations.PillarScore{}
			continue
		}
		if isJSONAbsent(in.Score) {
			continue // missing — the router refuses it by name
		}
		label, _ := observations.WheelPillarLabel(key)
		score, ok := observations.ParseWheelScore(string(in.Score))
		if !ok {
			return router.AddWheelRequest{}, fmt.Errorf(
				"wheel: %s needs a whole number from %d to %d (got %s); nothing was saved",
				label, observations.WheelScoreMin, observations.WheelScoreMax, in.Score,
			)
		}
		p := observations.PillarScore{Score: score}
		if in.Note != nil {
			p.Note = *in.Note
		}
		if !isJSONAbsent(in.Suggested) {
			n, sok := observations.ParseWheelScore(string(in.Suggested))
			if !sok {
				return router.AddWheelRequest{}, fmt.Errorf(
					"wheel: the suggestion for %s needs a whole number from %d to %d (got %s); nothing was saved",
					label, observations.WheelScoreMin, observations.WheelScoreMax, in.Suggested,
				)
			}
			p.Suggested = &n
		}
		pillars[key] = p
	}

	req := router.AddWheelRequest{Pillars: pillars}
	if doc.Month != nil {
		req.Month = *doc.Month
	}
	if doc.VisionReviewed != nil {
		req.VisionReviewed = *doc.VisionReviewed
	}
	if doc.VisionReflection != nil {
		req.VisionReflection = *doc.VisionReflection
	}
	return req, nil
}

// isJSONAbsent reports whether a raw JSON field was omitted or given as null.
func isJSONAbsent(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}

// wheelAddView is the `wheel add --json` payload (wheel.md §7.4): the
// snapshot's receipt, the month's stable id, the month, whether the month
// already had a wheel, and the eight stored self-ratings — never a suggestion.
type wheelAddView struct {
	ReceiptID string         `json:"receipt_id"`
	EntryID   string         `json:"entry_id"`
	Month     string         `json:"month"`
	Amended   bool           `json:"amended"`
	Scores    map[string]int `json:"scores"`
}

// wheelAddViewOf maps a write result onto its --json view.
func wheelAddViewOf(res router.WheelWriteResult) wheelAddView {
	return wheelAddView{
		ReceiptID: res.Receipt,
		EntryID:   res.EntryID,
		Month:     res.Month,
		Amended:   res.Amended,
		Scores:    res.Scores,
	}
}
