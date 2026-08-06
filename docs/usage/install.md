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

### Install the binary (recommended)

Install the latest prebuilt release into `~/.local/bin` — a user-writable directory, so
no `sudo`, and `lucid update` can self-update in place afterward:

```sh
VER=$(curl -fsSL https://api.github.com/repos/mrz1836/lucid/releases/latest | grep '"tag_name"' | cut -d'"' -f4 | tr -d v)
OS=$(uname -s | tr '[:upper:]' '[:lower:]'); ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
mkdir -p ~/.local/bin
curl -fsSL "https://github.com/mrz1836/lucid/releases/download/v${VER}/lucid_${VER}_${OS}_${ARCH}.tar.gz" | tar -xzf - -C ~/.local/bin lucid
```

Make sure `~/.local/bin` is on your `PATH` (add `export PATH="$HOME/.local/bin:$PATH"`
to your shell profile if needed). From then on the binary updates itself in place — see
[Keeping it up to date](#keeping-it-up-to-date).

### Build from source

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
reports `dev`, which `lucid update` treats as older than any real release). Put
the resulting binary on your `PATH`, e.g. `install -m 0755 lucid ~/.local/bin/lucid`.

## Keeping it up to date

`lucid update` (alias `lucid upgrade`) downloads the latest release, verifies its
SHA-256 checksum against the published `lucid_<ver>_checksums.txt`, and atomically
replaces the running binary — no `sudo` when it lives in `~/.local/bin`:

```sh
lucid update --check     # is a newer release available? (no install)
lucid update             # download, verify SHA-256, atomic swap
lucid update --force     # reinstall the latest even if already current
```

Every other command also runs a passive, cached background check and prints a one-line
"a new version is available" notice, silenced by `LUCID_NO_UPDATE_CHECK=1` (or the
shared `NO_UPDATE_CHECK` / `CI`). A binary owned by another installer (`go install`'s
`~/go/bin`, a Homebrew prefix) is refused rather than overwritten. See
[`commands.md`](commands.md#update) for the full flag set.

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
