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

// safeDayShard splits a logical date (YYYY-MM-DD) into its year/month shard,
// rejecting any value that is not three non-empty all-digit fields — so a
// separator- or dot-bearing date can never be turned into a day-file path that
// escapes the record tree. It is the day-file counterpart to [safeRecordPath],
// shared by the observation, focus, and reframe day-path helpers. Every
// logical_date the router derives comes from the civil-day rule and is already
// this shape, so a malformed value is unreachable via the CLI; this is the
// defense-in-depth guard that keeps that guarantee true against any future
// caller (the day-path helpers otherwise only replace `-`→`_`, which leaves a
// separator-bearing date free to walk out of the tree on write).
func safeDayShard(date string) (year, month string, err error) {
	parts := strings.Split(date, "-")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("storage: malformed logical_date %q", date)
	}
	for _, p := range parts {
		if p == "" {
			return "", "", fmt.Errorf("storage: malformed logical_date %q", date)
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return "", "", fmt.Errorf("storage: malformed logical_date %q", date)
			}
		}
	}
	return parts[0], parts[1], nil
}
