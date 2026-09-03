package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// stdinPath is the sentinel a free-text --*-file flag accepts to read its
// value from standard input instead of a file on disk. An agent that streams
// prose in through stdin never has to place it on the command line, so no
// shell metacharacter can split or truncate the invocation.
const stdinPath = "-"

// flagBodyFile is the shared flag name for the primary free-text field of a
// verb (the log body, the memory story, the thread name, and so on). Verbs
// with a single dominant free-text field reuse this one name so the off-
// command-line input path reads the same everywhere.
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
