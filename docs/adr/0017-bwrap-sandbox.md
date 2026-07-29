# 0017 — bwrap sandbox for bash tool

**Status**: Accepted (amended ADR-0017a — startup detection and caching added)

## Context

The `bash` tool executes shell commands on the host via `os/exec.Command`
with no isolation. This makes Eitri vulnerable to prompt-injection-based
shell attacks — `rm -rf /`, data exfiltration via `curl`, cryptominers,
and similar malicious commands.

At the same time, the agent needs network and filesystem access to do its
job: installing dependencies, cloning repositories, running builds, and
invoking `gh` or `curl` against external APIs. The sandbox should be a
**defense-in-depth** layer that limits damage without preventing
legitimate workflows.

Additionally, Eitri targets unprivileged users on shared Linux systems.
The solution must work without root, setuid binaries, or kernel modules.

## Decision

Use **bubblewrap** (`bwrap`) — an unprivileged user-namespace sandbox
that uses Linux user namespaces, mount namespaces, and PID namespaces to
isolate processes. Bubblewrap is installed by default on many modern
Linux distributions and requires no elevated privileges.

Eitri exposes two sandbox profiles via the settings UI:

| Profile   | Behaviour                                                       |
|-----------|----------------------------------------------------------------|
| `none`    | No sandboxing. Command runs directly via `bash -c`.             |
| `default` | Command runs inside a bwrap sandbox (see argument rationale).   |

The **default** profile is enabled at initial startup
(`sandbox.DefaultConfig()`). It falls back to direct execution when:

- bwrap is not installed (`exec.LookPath("bwrap")` fails)
- bwrap is installed but not usable (`BwrapIsUsable()` returns false, e.g. on
  GitHub Actions where user namespaces are unavailable)
- The OS is not Linux (`runtime.GOOS != "linux"`)
- The profile is explicitly set to `none`

## Bwrap argument rationale

`WrapCommand` in `internal/sandbox/sandbox.go` builds the following
bwrap argument list for `ProfileDefault`:

```
bwrap \
  --die-with-parent \
  --new-session \
  --unshare-pid \
  [--unshare-net] \
  --ro-bind / / \
  --bind <workspace> <workspace> \
  --bind /tmp /tmp \
  --dev /dev \
  --proc /proc \
  [--bind <extra> <extra> ...] \
  --chdir <workspace> \
  -- bash -c <command>
```

### `--die-with-parent`

Kills the sandbox process if its parent (Eitri) dies. Prevents orphaned
process trees from accumulating when the user closes the browser tab or
Eitri crashes.

### `--new-session`

Creates a new session for the sandbox process, preventing TIOCSTI
terminal injection attacks (CVE-2017-5226) where a child process can
inject keystrokes into the parent's terminal.

### `--unshare-pid`

Gives the sandbox a separate PID namespace. The sandbox cannot see or
signal host processes, reducing the attack surface for process
enumeration and manipulation.

### `[--unshare-net]`

Only added when `Config.Network` is `false` (default is `true`). Network
is enabled by default because most workflows require network access
(`go build`, `npm install`, `pip install`, `gh`, `curl`). When disabled,
the sandbox has no network connectivity at all.

### `--ro-bind / /`

Makes the entire root filesystem available read-only. This is simpler
and more robust than enumerating specific paths — it catches all
distribution-specific locations without needing to maintain a list.

### `--bind <workspace> <workspace>`

Makes the workspace directory writable, so code writes, build artifacts,
and file tool outputs land on disk and persist across commands.

### `--bind /tmp /tmp`

Preserves the host `/tmp` as a writable temporary directory inside the
sandbox. Avoids shadowing the workspace if it happens to live under
`/tmp`.

### `--dev /dev`

Mounts a minimal `devtmpfs` with essential device nodes (`null`, `zero`,
`random`, `urandom`, `fd`, `stdin`, `stdout`, `stderr`). Keeps the
device surface small.

### `--proc /proc`

Mounts a `procfs` scoped to the sandbox's PID namespace. Required for
many shell utilities and runtime operations.

### `--bind <extra> <extra> ...`

User-specified extra writable paths, configured via the `extra_writable_paths`
setting. Each path is bound as a writable `--bind` mount. Useful for
toolchains, caches, or shared directories.

### `--chdir <workspace>`

Sets the initial working directory to the workspace, matching the
expectation of tool callers.

## System design

```
┌────────────────────────────────────────────────────────────┐
│  Eitri process                                              │
│                                                              │
│  config.Config                                              │
│    └─ Sandbox sandbox.Config   ◄── settings UI checkbox     │
│         ├─ Profile: "default"|"none"                         │
│         ├─ Network: true|false                               │
│         └─ ExtraWritablePaths: [...]                         │
│                                                              │
│  RunConfig → buildBaseToolRegistry()                         │
│    └─ NewBashTool(workspace, timeout, sandboxConfig)         │
│         └─ BashTool.Call()                                   │
│              └─ sandbox.WrapCommand(workspace, cmd, cfg)     │
│                   ├─ [bwrap] ... -- bash -c <command>       │
│                   └─ [bash] -c <command>  (fallback)        │
└────────────────────────────────────────────────────────────┘
```

The sandbox config flows from the global `config.Config` through
`runconfig.RunConfig` into `BashTool`. The settings UI provides a
checkbox to enable/disable sandboxing and a textarea for extra writable
paths. The `WrapCommand` function is the single choke-point where all
bash tool invocations pass through.

## Consequences

Positive:

- Defense-in-depth against prompt injection: even if the LLM emits a
  destructive command, the sandbox limits what it can read, write, or
  execute.
- Works for unprivileged users — no root, no setuid, no kernel
  compilation.
- Simple two-profile model (`none` / `default`) with automatic fallback.
- Transparent to the agent — no changes needed in tool callers.
- Extra writable paths give users an escape hatch for legitimate needs.

Negative / limitations at time of writing:

- **Home directory is read-only** under `--ro-bind / /`. Commands like
  `go install`, `pip install --user`, `gh auth login`, or anything that
  writes to `~/.config`, `~/.cache`, or `~/.local` will fail unless the
  user adds those paths via extra writable paths.
- **File tools are NOT sandboxed** — `read`, `write`, `edit`, and `grep` still operate on the host directly with path validation but
  without sandbox isolation.
- **Environment variables leak** into the sandbox (no `--clearenv`).
  If `$TMPDIR` points to a read-only path, tools that rely on temporary
  files may fail.
- **No seccomp filtering** — all system calls are allowed.
- **Per-session sandbox configuration** is not yet supported — the
  sandbox setting is global.
- **Network is on by default**, reducing isolation but required for most
  workflows.
- **Per-invocation overhead** — a new bwrap process is created for every
  bash tool call. No session reuse.

### Startup detection

`BwrapAvailable()` (added in ADR-0017a) caches the bwrap usability check in
a `sync.OnceValue` so the `exec.Command` probe runs at most once per process
lifetime. The result is logged at startup and surfaced in the Settings UI
as a badge. This gives the user immediate feedback about whether sandboxing
is active, and makes the fallback path discoverable.

When the user explicitly sets `"profile": "none"` in config, a startup
warning explains that commands will run without isolation.

### Caching

`WrapCommand` uses the cached `BwrapAvailable()` instead of calling
`BwrapIsUsable()` on every invocation. This avoids repeated `exec.Command`
calls for a check whose result cannot change during the process lifetime.

## Future possibilities

- **Per-session sandbox profiles**: allow different sandbox settings per
  chat session (e.g. strict for untrusted repositories, relaxed for
  trusted ones).
- **Seccomp syscall allowlisting**: reduce the kernel attack surface by
  only permitting syscalls needed for common shell operations.
- **Overlayfs for workspace**: mount the workspace as an overlay so
  changes are ephemeral unless explicitly committed (try-before-commit).
- **Sandboxed file tools**: extend bwrap wrapping to `read`, `write`,
  `edit`, `grep`, and `glob` tools for full sandbox coverage.
- **Reusable sandbox sessions**: keep a long-running bwrap process and
  execute commands inside it, avoiding per-invocation setup overhead.
- **Environment cleanup**: add `--clearenv` or a curated allowlist of
  environment variables to reduce information leaks.

## References

- [bubblewrap README](https://github.com/containers/bubblewrap) —
  official documentation and source
- [CVE-2017-5226](https://github.com/containers/bubblewrap/issues/142) —
  TIOCSTI terminal injection vulnerability mitigated by `--new-session`
- [koder sandbox implementation](https://github.com/lkarlslund/koder/blob/main/internal/sandbox/bwrap.go) —
  inspiration for the `--ro-bind / /` default-read-only strategy
- [bubblewrap manual page](https://man.archlinux.org/man/bwrap.1) —
  argument reference
- `internal/sandbox/sandbox.go` — Eitri's implementation
- `internal/sandbox/sandbox_test.go` — test suite covering fallback,
  argument structure, integration, and edge cases
