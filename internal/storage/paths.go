package storage

import (
	"fmt"
	"path/filepath"
	"strings"
)

// safeRecordPath resolves dir/<id><ext> for a single-file record family,
// rejecting an empty or separator-bearing id so a malformed slug can never
// escape the record tree. noun names the id kind for the error ("person key",
// "insight id"). It is the one path-safety rule the per-family path helpers
// ([Adapter.personPath], [Adapter.insightPath]) share.
func safeRecordPath(dir, id, ext, noun string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("storage: invalid %s %q", noun, id)
	}
	return filepath.Join(dir, id+ext), nil
}
