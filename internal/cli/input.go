package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// stdinPath is the sentinel a free-text --*-file flag accepts to read its
// value from standard input instead of a file on disk. An agent that streams
// prose in through stdin never has to place it on the command line, so no
// shell metacharacter can split or truncate the invocation.
const stdinPath = "-"

// flagBodyFile is the flag name [readBodyFile] uses to label its own errors
// (an empty or unreadable file). Each verb registers its file-input flags with
// literal names at the call site — `--body-file`, `--note-file`, `--reason-file`,
// and so on — so the off-command-line spelling reads plainly beside the field
// it feeds; this constant is only the shared error label behind them.
const flagBodyFile = "body-file"

// readBodyFile is the one shared reader behind every free-text --*-file flag.
// It delivers human-authored prose to a verb off the command line so shell
// metacharacters (&, ;, |, `, $, single/double quotes) stay data instead of
// being parsed by the shell that launched lucid — the whole point of the
// file-input surface.
//
// A path of "-" reads all of cmdIn (cobra's InOrStdin()); any other path is
// read from disk. The only normalization is stripping one trailing newline —
// the newline an editor or heredoc appends — so the payload is otherwise
// returned byte-for-byte, its leading and trailing spaces intact. An empty
// result after that strip is an error: a --*-file flag that supplies nothing
// is a mistake, not a request to write emptiness.
func readBodyFile(path string, cmdIn io.Reader) (string, error) {
	data, err := readRawInput(path, cmdIn)
	if err != nil {
		return "", err
	}
	// Strip exactly one terminal newline (not TrimRight): a trailing space is
	// data the fidelity contract preserves, only the editor/heredoc newline is
	// framing.
	body := strings.TrimSuffix(string(data), "\n")
	if body == "" {
		return "", fmt.Errorf("--%s: file is empty", flagBodyFile)
	}
	return body, nil
}

// readRawInput returns the unnormalized bytes behind a --*-file flag: the whole
// of stdin for a "-" path, or the file's contents otherwise. Splitting the read
// out keeps readBodyFile's normalization free of the source branch.
func readRawInput(path string, cmdIn io.Reader) ([]byte, error) {
	if path != stdinPath {
		data, err := os.ReadFile(path) //nolint:gosec // operator-supplied free-text input path, read once per invocation
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", flagBodyFile, err)
		}
		return data, nil
	}
	if cmdIn == nil {
		return nil, fmt.Errorf("--%s: no stdin available to read", flagBodyFile)
	}
	data, err := io.ReadAll(cmdIn)
	if err != nil {
		return nil, fmt.Errorf("--%s: read stdin: %w", flagBodyFile, err)
	}
	return data, nil
}

// ensureSingleStdin rejects an invocation that routes more than one free-text
// field to standard input. Stdin is a single stream: if two --*-file flags are
// both given "-", the first read drains it and the second sees empty, a silent
// data loss. named maps each flag's user-facing name to the path it was given;
// only entries equal to "-" are counted, so a caller can pass every file flag
// unconditionally and let this decide. Verbs with a single file flag never need
// it; the multi-field verbs added later call it before any read.
func ensureSingleStdin(named map[string]string) error {
	var fromStdin []string
	for name, path := range named {
		if path == stdinPath {
			fromStdin = append(fromStdin, name)
		}
	}
	if len(fromStdin) > 1 {
		sort.Strings(fromStdin)
		return fmt.Errorf(
			"only one field can read from stdin (-), but %s were all given -",
			strings.Join(fromStdin, ", "),
		)
	}
	return nil
}

// ensureSingleStdinFlags rejects routing more than one of a verb's --*-file
// flags to stdin before any of them is read. It reads each flag's current value
// off cmd, so a verb can list every string-valued file flag it resolves and let
// this decide; only changed flags whose value is "-" are counted. Verbs with a
// single file flag never need it — one field draining stdin is the intended
// use; the multi-field verbs call this ahead of any [readBodyFile].
func ensureSingleStdinFlags(cmd *cobra.Command, fileFlags ...string) error {
	named := make(map[string]string, len(fileFlags))
	for _, name := range fileFlags {
		if cmd.Flags().Changed(name) {
			v, _ := cmd.Flags().GetString(name)
			named["--"+name] = v
		}
	}
	return ensureSingleStdin(named)
}

// resolvePrimaryText resolves a verb's primary free-text field — the log body,
// the memory story, the thread name, the value that would otherwise be trailing
// positional words. inline is that positional text already joined; fileFlag is
// the --*-file flag that can supply it off the command line instead.
//
// When the flag was not given, inline is returned unchanged so the caller's
// existing emptiness rule still governs (a bare `lucid log` stays legal). When
// the flag was given alongside non-empty positional text, that is two sources
// for one field and a usage error — the silent-truncation class this surface
// exists to close. Otherwise the file (or stdin) body flows through the shared
// reader. verb labels the error.
func resolvePrimaryText(cmd *cobra.Command, verb, fileFlag, inline string) (string, error) {
	if !cmd.Flags().Changed(fileFlag) {
		return inline, nil
	}
	if strings.TrimSpace(inline) != "" {
		return "", fmt.Errorf("lucid %s: give the text via --%s or as positional words, not both", verb, fileFlag)
	}
	path, _ := cmd.Flags().GetString(fileFlag)
	body, err := readBodyFile(path, cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("lucid %s: %w", verb, err)
	}
	return body, nil
}

// resolveOptionalText resolves an auxiliary free-text field that may be supplied
// inline (--<inlineFlag>) or off the command line (--<fileFlag>), the two being
// mutually exclusive. label names the field in errors. When neither is set the
// inline value (empty) is returned, so the field stays optional exactly as
// before the file form existed.
func resolveOptionalText(cmd *cobra.Command, verb, label, inlineFlag, fileFlag string) (string, error) {
	fileChanged := cmd.Flags().Changed(fileFlag)
	if cmd.Flags().Changed(inlineFlag) && fileChanged {
		return "", fmt.Errorf("lucid %s: give the %s via --%s or --%s, not both", verb, label, inlineFlag, fileFlag)
	}
	if !fileChanged {
		v, _ := cmd.Flags().GetString(inlineFlag)
		return v, nil
	}
	path, _ := cmd.Flags().GetString(fileFlag)
	body, err := readBodyFile(path, cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("lucid %s: %w", verb, err)
	}
	return body, nil
}

// requireTextArgs builds a cobra positional validator that requires `without`
// positionals normally but only `with` when one of the named primary --*-file
// flags supplies the otherwise-required free text. It lets a file flag stand in
// for a required positional (a bare `lucid focus add --body-file f`) without
// permitting an actually empty write.
func requireTextArgs(with, without int, fileFlags ...string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		for _, name := range fileFlags {
			if cmd.Flags().Changed(name) {
				return cobra.MinimumNArgs(with)(cmd, args)
			}
		}
		return cobra.MinimumNArgs(without)(cmd, args)
	}
}
