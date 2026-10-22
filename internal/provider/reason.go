package provider

import "errors"

// reasonError attaches a backend's own short, human-readable failure reason
// (e.g. the CLI's "Not logged in · Please run /login") to a transport error
// without changing it: Error() is the wrapped text verbatim and Unwrap keeps
// the sentinel in the chain, so errors.Is / [IsOutage] classify exactly as
// they did before the reason was attached.
type reasonError struct {
	err    error
	reason string
}

func (e *reasonError) Error() string { return e.err.Error() }

func (e *reasonError) Unwrap() error { return e.err }

// WithReason wraps err so [ReportedReason] can recover reason later without
// parsing the error chain's text. The error's message and classification are
// unchanged. A nil err stays nil, and an empty reason returns err as-is.
func WithReason(err error, reason string) error {
	if err == nil || reason == "" {
		return err
	}
	return &reasonError{err: err, reason: reason}
}

// ReportedReason returns the backend-reported reason attached by
// [WithReason] anywhere in err's %w chain, or "" when err is nil or carries
// none. Callers use it to tell a person why a model was unreachable in the
// backend's own words, never the Go error chain.
func ReportedReason(err error) string {
	var re *reasonError
	if errors.As(err, &re) {
		return re.reason
	}
	return ""
}
