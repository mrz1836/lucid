#!/usr/bin/env bash
# Named runner for the metrics/anchor engine tests (days-since, adherence,
# streak, and the metrics projection). Kept as a script so a fixed command
# resolves the package regardless of the caller's working directory.
set -euo pipefail
cd "$(dirname "$0")/../.."
exec go test ./internal/engine/ -run 'Metric|DaysSince|Adherence|Streak' "$@"
