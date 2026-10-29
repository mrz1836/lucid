// Package contactweekly is the Monday contact check-in post (ADR-0014 §8): a
// config-gated scheduler node that, once a week, posts who is due this week and
// who is still open from before, plus the week's birthdays, to the user's own
// channel.
//
// It mirrors internal/witnessreport's weekly-job shape — its own disposable
// flywheel node and job DB, one weekly periodic reconciled by slug, a bounded
// late attempt and a 48-hour stale cut-off — and delivers through the shared
// [flynode.Fire] reliability order (cut-off, idempotency, compose, send,
// read-back, receipt). What it leaves out is the model: the post is the fixed
// template internal/contact renders from the deterministic due view, so friend
// names never reach a model provider (P9). purity_test.go enforces that no
// provider, agent, compose or model SDK package is imported.
//
// An empty week posts nothing: when both lists and the birthdays line are
// empty the node returns before the send path, records a nothing-due skip
// marker on the job run, and logs one info line — no send, no alert. Every
// send and every alert goes only to the user channel; no code path here
// addresses a friend.
package contactweekly
