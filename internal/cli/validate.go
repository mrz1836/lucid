package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/validate"
)

// errValidationFailed is returned when `lucid validate` found an
// error-severity issue. The findings are already printed; this sentinel just
// maps to a non-zero exit so a script (or CI) never reads a breached gate as
// success. Warnings alone do not return it.
var errValidationFailed = errors.New("lucid: validation found errors")

// moduleMarker identifies this repo's go.mod so validate can locate the repo
// root by walking up from the working directory. Grep checks run relative to
// that root; run outside a checkout, they are skipped rather than guessed.
const moduleMarker = "module github.com/mrz1836/lucid"

// validateJSON is the machine-readable shape of a validate run (ADR-0007).
// Field names are a stable script contract.
type validateJSON struct {
	OK       bool               `json:"ok"`
	Ran      []string           `json:"ran"`
	Skipped  []string           `json:"skipped"`
	Errors   int                `json:"errors"`
	Warnings int                `json:"warnings"`
	Findings []validate.Finding `json:"findings"`
	Repo     string             `json:"repo,omitempty"`
	Ledger   string             `json:"ledger,omitempty"`
}

// newValidateCmd wires `lucid validate` (claude-code-workflow.md; ADR-0008):
// the read-only verification sweep. It runs the S-7 public-boundary, S-8
// diagnostic-language, sanctuary, doc-link, and Ledger-schema checks and exits
// non-zero if any error-severity gate is breached. It writes nothing —
// neither the repo nor the Ledger is mutated (the Ledger is not even
// scaffolded).
func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the Ledger and boundary invariants (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, skipped, err := resolveValidateInputs()
			if err != nil {
				return err
			}
			rep, err := validate.Run(opts)
			if err != nil {
				return fmt.Errorf("lucid validate: %w", err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return emitValidateJSON(cmd, rep, opts, skipped)
			}
			printValidateHuman(cmd, rep, skipped)
			if !rep.OK() {
				return errValidationFailed
			}
			return nil
		},
	}
}

// resolveValidateInputs assembles the sweep's options from the environment:
// the repo root (found by walking up for this module's go.mod) enables the
// grep checks, and an already-initialized Ledger home enables the schema
// check. It returns the options plus the human-readable names of any halves
// that were skipped because their input was absent. It creates nothing.
func resolveValidateInputs() (validate.Options, []string, error) {
	opts := validate.Options{Denylist: router.SanctuaryDenylist()}
	var skipped []string

	if root, ok := findRepoRoot(); ok {
		opts.RepoRoot = root
	} else {
		skipped = append(skipped, "repo checks (no checkout found)")
	}

	store, err := storage.Open()
	if err != nil {
		return validate.Options{}, nil, fmt.Errorf("lucid validate: resolve home: %w", err)
	}
	if _, statErr := os.Stat(store.ConfigPath()); statErr == nil {
		opts.Ledger = ledgerAdapter{a: store}
	} else {
		skipped = append(skipped, "schema check (no Ledger found)")
	}

	return opts, skipped, nil
}

// findRepoRoot walks up from the working directory looking for the go.mod that
// declares this module. It returns the directory and true on success, or an
// empty string and false when run outside a checkout.
func findRepoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		//nolint:gosec // walking up from the working directory to read our own go.mod; read-only
		if b, rerr := os.ReadFile(modPath); rerr == nil && strings.Contains(string(b), moduleMarker) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// printValidateHuman renders the sweep's prose report: each finding on its own
// line, then a one-line summary and any skipped halves.
func printValidateHuman(cmd *cobra.Command, rep validate.Report, skipped []string) {
	out := cmd.OutOrStdout()
	for _, f := range rep.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		_, _ = fmt.Fprintf(out, "%-5s %-19s %s — %s\n", f.Severity, f.Check, loc, f.Message)
	}
	if rep.OK() {
		_, _ = fmt.Fprintf(out, "validate: clean (ran: %s; %d warning(s))\n",
			strings.Join(rep.Ran, ", "), rep.Warnings())
	} else {
		_, _ = fmt.Fprintf(out, "validate: %d error(s), %d warning(s) (ran: %s)\n",
			rep.Errors(), rep.Warnings(), strings.Join(rep.Ran, ", "))
	}
	for _, s := range skipped {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "skipped: %s\n", s)
	}
}

// emitValidateJSON writes the machine-readable report.
func emitValidateJSON(cmd *cobra.Command, rep validate.Report, opts validate.Options, skipped []string) error {
	payload := validateJSON{
		OK:       rep.OK(),
		Ran:      rep.Ran,
		Skipped:  skipped,
		Errors:   rep.Errors(),
		Warnings: rep.Warnings(),
		Findings: rep.Findings,
		Repo:     opts.RepoRoot,
	}
	if opts.Ledger != nil {
		payload.Ledger = opts.Ledger.Home()
	}
	if err := writeJSON(cmd.OutOrStdout(), payload); err != nil {
		return err
	}
	if !rep.OK() {
		return errValidationFailed
	}
	return nil
}

// ledgerAdapter adapts the storage adapter to validate.LedgerSource: it
// exposes the read-only listing/parse ops the schema sweep needs, collapsing
// each record read to just its error (the sweep cares only whether a record
// parses, never its contents).
type ledgerAdapter struct{ a *storage.Adapter }

// Home returns the Ledger root the adapter manages.
func (l ledgerAdapter) Home() string { return l.a.Home() }

// ListProcessedIDs enumerates the processed-artifact ids.
func (l ledgerAdapter) ListProcessedIDs() ([]string, error) { return l.a.ListProcessedIDs() }

// ListInsightIDs enumerates the insight ids.
func (l ledgerAdapter) ListInsightIDs() ([]string, error) { return l.a.ListInsightIDs() }

// ListReflectionIDs enumerates the reflection ids.
func (l ledgerAdapter) ListReflectionIDs() ([]string, error) { return l.a.ListReflectionIDs() }

// ListPeopleKeys enumerates the people record keys.
func (l ledgerAdapter) ListPeopleKeys() ([]string, error) { return l.a.ListPeopleKeys() }

// ReadProcessedErr returns only the parse error of reading one processed artifact.
func (l ledgerAdapter) ReadProcessedErr(id string) error { _, err := l.a.ReadProcessed(id); return err }

// ReadInsightErr returns only the parse error of reading one insight.
func (l ledgerAdapter) ReadInsightErr(id string) error { _, err := l.a.ReadInsight(id); return err }

// ReadReflectionErr returns only the parse error of reading one reflection.
func (l ledgerAdapter) ReadReflectionErr(id string) error {
	_, err := l.a.ReadReflection(id)
	return err
}

// ReadPersonErr returns only the parse error of reading one person record.
func (l ledgerAdapter) ReadPersonErr(key string) error { _, _, err := l.a.ReadPerson(key); return err }

// LoadConfigErr returns only the parse error of loading lucid.json.
func (l ledgerAdapter) LoadConfigErr() error { _, err := l.a.LoadConfig(); return err }
