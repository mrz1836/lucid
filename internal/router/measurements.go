package router

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// MeasurementsRejectedError is a `lucid export measurements` argument the
// grammar does not take (error-states.md Mx-1, Mx-3): a window that is neither
// `@YYYY-MM-DD` nor `all`, or a `--metric` that is empty or holds whitespace
// or a control character. Reason is the fixed, model-free copy. It is decided
// before anything is read or written, so the CLI types it as a usage error
// with errors.As.
type MeasurementsRejectedError struct {
	Reason string
}

// Error returns the fixed reason.
func (e *MeasurementsRejectedError) Error() string { return e.Reason }

// MeasurementsExport executes `lucid export measurements [@YYYY-MM-DD|all]
// [--metric <slug>]` (usage/commands.md §export, "Measurement export rules"):
// it writes the long-format projections/measurements.csv, appends the
// disclosure-log line, and returns only the path as the postable message. The
// window reuses the packet's positional grammar, but its default is all
// history — `""` and `all` both mean no lower bound, and `@<date>` starts the
// window on that day. metricSet is the flag's Changed bit, so an explicit
// empty `--metric ""` is refused rather than read as "no filter". Every
// refusal is a [*MeasurementsRejectedError], raised before any write.
func (r *Router) MeasurementsExport(arg, metric string, metricSet bool, now time.Time) (ExportOutcome, error) {
	now = whenOr(now)

	startOverride, _, err := parsePacketArg(arg)
	if err != nil {
		return ExportOutcome{}, &MeasurementsRejectedError{
			Reason: fmt.Sprintf("measurements window must be @YYYY-MM-DD or all, got %q", arg),
		}
	}
	if metricSet && !validMetricSlug(metric) {
		return ExportOutcome{}, &MeasurementsRejectedError{Reason: "--metric needs one slug with no spaces"}
	}
	if !metricSet {
		metric = ""
	}

	res, err := r.store.ExportMeasurementsCSV(now, now.Location(), startOverride, metric)
	if err != nil {
		return ExportOutcome{}, err
	}
	return ExportOutcome{
		What:        res.What,
		Path:        res.Path,
		WindowStart: res.WindowStart,
		WindowEnd:   res.WindowEnd,
		Message:     res.Path, // only the path is posted — never a reading
		Metric:      metric,
	}, nil
}

// validMetricSlug reports whether a --metric value can name a captured slug:
// non-empty, with no whitespace and no control character. Capture splits on
// whitespace, so no stored slug holds one, and a tab would break the
// tab-separated exports.log line the filter is recorded in.
func validMetricSlug(metric string) bool {
	if metric == "" {
		return false
	}
	return !strings.ContainsFunc(metric, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}
