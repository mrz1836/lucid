# Install Lucid

Lucid is a single Go binary named `lucid`. It stores everything under a
user-owned Ledger at `~/.lucid/` and talks to no cloud service. This page gets
the binary onto your host and scaffolds that Ledger.

## Prerequisites

- A POSIX host — **macOS or Linux**. (The supervised scheduler in `deploy/`
  ships a launchd job, so unattended scheduling is macOS-first; the binary and
  every CLI command work on both.)
- **Go 1.26+** and **git** — only if you build from source. A prebuilt release
  binary needs neither.
- No API keys or accounts to start. (Some optional pieces — a chat harness, an
  LLM provider for the agentic Mirror verbs, an opt-in enricher — bring their
  own requirements; see [Optional integrations](#optional-integrations).)

## Install

Pick one path. All three produce the same `lucid` binary.

### 1. Build from source (recommended while iterating)

```sh
git clone https://github.com/mrz1836/lucid.git
cd lucid
magex build          # uses .mage.yaml; emits ./cmd/lucid/lucid
# or, without the mage toolchain:
go build -o lucid ./cmd/lucid
```

The module path is `github.com/mrz1836/lucid` and the entrypoint is
`cmd/lucid/main.go`. `magex build` injects version/commit/build-date via
ldflags; a plain `go build` produces a working `dev` build (its `version`
reports `dev`, which `lucid upgrade` treats as older than any real release).

Put the resulting binary somewhere on your `PATH`, e.g.:

```sh
install -m 0755 lucid /usr/local/bin/lucid   # or ~/bin, ~/.local/bin, …
```

### 2. `go install`

```sh
go install github.com/mrz1836/lucid/cmd/lucid@latest
```

Installs `lucid` into `$(go env GOBIN)` (or `$(go env GOPATH)/bin`). Make sure
that directory is on your `PATH`.

### 3. Release binary + self-upgrade

Download the archive for your platform from the project's GitHub Releases,
unpack it, and place `lucid` on your `PATH`. From then on the binary updates
itself in place:

```sh
lucid upgrade --check     # is a newer release available? (no install)
lucid upgrade             # download, verify SHA-256, atomic swap
```

Channel selection follows the `UPDATE_CHANNEL` environment variable
(`stable | beta | edge`, default `stable`); `--channel` overrides it. The
install target is the resolved path of the running binary — if its directory
isn't writable, `upgrade` exits with a clear error naming the directory rather
than installing elsewhere. See [`commands.md`](commands.md#upgrade) for the full
flag set, including `--managed` (drain-window-aware, for supervised hosts).

## Verify the install

```sh
lucid version            # prints version, commit, build date, Go version, platform
lucid version --json     # the same as a machine-readable object
```

## First-run setup

Scaffold the Ledger:

```sh
lucid init
```

`init` creates `~/.lucid/` and its subtree with owner-only permissions
(directories `0700`, files `0600`) and writes a default `lucid.json`. It is
**idempotent** — run it again and it reports "already present, nothing to do."

You often don't even need it: most stateful commands (`log`, `closeout`,
`mode`, `status`, `obs`, `day`) **self-scaffold on first use**, so capture never
blocks on setup.

### Choosing where the Ledger lives

By default the Ledger is `~/.lucid/`. Override the location with the
`LUCID_HOME` environment variable — useful for a scratch instance or tests:

```sh
LUCID_HOME=/tmp/lucid-scratch lucid init
LUCID_HOME=/tmp/lucid-scratch lucid log "trying it out"
```

What the tree contains and which parts are the must-keep backup set is covered
in [`getting-started.md`](getting-started.md#data--privacy); the full schema for
each directory lives in [`../mvp/data-model.md`](../mvp/data-model.md).

## Optional integrations

These are not required to use the CLI. Each is abstract here — instance wiring
(channels, contacts, schedules) is your own configuration, never checked in.

- **Chat/harness surface.** To drive Lucid from a chat client and unlock the
  agentic Mirror verbs (`/checkin`, `/reflect`, `/ask`), install the harness
  skill and agent definitions ([`../../skills/lucid/SKILL.md`](../../skills/lucid/SKILL.md),
  [`../../agents/lucid/identity.md`](../../agents/lucid/identity.md)). The
  harness shells out to the same `lucid` binary; see
  [`../mvp/local-runtime.md`](../mvp/local-runtime.md). These verbs also need an
  LLM provider configured for the harness.
- **Supervised scheduler.** The Engine's two scheduled jobs — the evening
  **bell** and the morning **tripwire** — plus the enrichment job run under a
  supervisor. Templates for a launchd job and `hush supervise` live in
  `deploy/`. Without a scheduler you can still run the whole loop by hand;
  `lucid closeout` at a terminal finishes the night with or without any daemon.

## Next

Head to [`getting-started.md`](getting-started.md) to run your first day.
