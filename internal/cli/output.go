package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// jsonFlag is the persistent flag name that switches supported commands into
// machine-readable output: the read models (status, day, export, validate,
// version) emit their view, and the write verbs (log, closeout, obs, reframe,
// focus) emit a {receipt_id, logical_date} receipt through emitReceipt.
// Human-first prose is the default so automation never scrapes formatted text
// (ADR-0007).
const jsonFlag = "json"

// writeJSON marshals v as indented JSON to w with a trailing newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("lucid: encode json: %w", err)
	}
	return nil
}

// containsFold reports whether substr is contained in s,
// case-insensitively.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// emitErr prints a command error to stderr and returns it unchanged. The root
// sets SilenceErrors, so a returned error otherwise only sets the exit code with
// no explanation; the data-ops verbs (backup, restore) name their own remedy, so
// the message and the exit code must travel together — mirroring the scheduler
// verbs' "lucid: scheduler: <message>" stderr line.
func emitErr(cmd *cobra.Command, err error) error {
	if err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
	}
	return err
}

// emitRefusedDay prints a refused `--day` to stderr and returns err unchanged,
// so a capture verb's strict-tier rejection reaches the user instead of exiting
// silently — the root sets SilenceErrors and [Execute] renders no returned
// error, so a refusal nobody prints is a bare non-zero exit the user has to
// guess at. Only a [router.DayRejectedError] is printed: a refusal is a fixed
// reason naming the accepted forms and confirming nothing was saved, while a
// runtime fault stays a plain error and keeps whatever handling its caller
// gives it. This is the capture verbs' half of the same surfacing the Engine
// verbs do inline against their own rejection paths.
func emitRefusedDay(cmd *cobra.Command, err error) error {
	var refused *router.DayRejectedError
	if errors.As(err, &refused) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), refused.Reason)
	}
	return err
}

// emit renders a read-model command result: when --json is set it writes the
// machine payload verbatim, otherwise it prints each human-first line to
// stdout. It is the shared tail of the read commands (status, day, stats,
// metrics) so the --json branch and the line loop live in one place, keeping
// prose the default and JSON strictly opt-in (ADR-0007).
func emit(cmd *cobra.Command, jsonPayload any, lines []string) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), jsonPayload)
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
	}
	return nil
}

// writeReceiptView is the machine shape a write verb returns under --json: the
// receipt id and the logical day it landed on, a bare snake_case object with no
// {ok,data} envelope (ADR-0007). It is the newly-conformant write verbs' common
// contract (log, closeout, obs, reframe, focus); the verbs that already emitted
// JSON keep their own keys untouched.
type writeReceiptView struct {
	ReceiptID   string `json:"receipt_id"`
	LogicalDate string `json:"logical_date"`
}

// emitReceipt renders a write verb's result: the {receipt_id, logical_date}
// JSON view on stdout under --json, else the human ack unchanged. It is the
// shared tail of the write verbs so the --json branch lives in one place and a
// future write verb inherits conformance for free, the same way emit does for
// the read models.
func emitReceipt(cmd *cobra.Command, receiptID, logicalDate, ack string) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), writeReceiptView{ReceiptID: receiptID, LogicalDate: logicalDate})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), ack)
	return nil
}
