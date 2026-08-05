# 0017 — bwrap sandbox for bash tool

**Status**: Accepted (amended ADR-0017a — startup detection and caching)

## Context

The `bash` tool executes shell commands on the host via `os/exec.Command` with no isolation, leaving Eitri vulnerable to prompt-injection-based shell attacks (`rm -rf /`, data exfiltration via `curl`, cryptominers). The agent still needs network and filesystem access to do its job: installing dependencies, cloning repositories, running builds, invoking `gh` or `curl`. The sandbox is a **defense-in-depth** layer that limits damage without preventing legitimate workflows — and it must work for unprivileged users on shared Linux systems: no root, no setuid, no kernel modules.

## Decision

Use **bubblewrap** (`bwrap`) — an unprivileged user-namespace sandbox, installed by default on many Linux distributions. Two profiles via the settings UI: `none` (direct `bash -c`) and `default` (bwrap, enabled at initial startup via `sandbox.DefaultConfig()`). Falls back to direct execution when bwrap is missing, installed but unusable (e.g. no user namespaces on CI), the OS is not Linux, or the profile is explicitly `none`.

`WrapCommand` in `internal/sandbox/sandbox.go` builds:

```
bwrap \
  --die-with-parent \
  --new-session \
  --unshare-pid \
  [--unshare-net] \
  --ro-bind / / \
  --bind <workspace> <workspace> \
  --bind <ephemeral-tmpdir> /tmp \
  --dev /dev \
  --proc /proc \
  [--bind <extra> <extra> ...] \
  --chdir <workspace> \
  -- bash -c <command>
```

### Argument rationale

- `--die-with-parent` — kills the sandbox when Eitri dies; no orphaned process trees when the user closes the tab or Eitri crashes.
- `--new-session` — prevents TIOCSTI terminal injection (CVE-2017-5226), where a child injects keystrokes into the parent's terminal.
- `--unshare-pid` — separate PID namespace; the sandbox cannot see or signal host processes.
- `--unshare-net` — only when `Config.Network` is `false` (default `true`, since most workflows need network: `go build`, `npm install`, `gh`, `curl`).
- `--ro-bind / /` — entire root filesystem read-only; simpler and more robust than enumerating specific paths.
- `--bind <workspace> <workspace>` — workspace writable so code writes, build artifacts, and file tool outputs land on disk.
- `--bind <ephemeral-tmpdir> /tmp` — a fresh temp dir under `/tmp` mounted as `/tmp` inside the sandbox, giving temp-file isolation between commands.
- `--dev /dev`, `--proc /proc` — minimal `devtmpfs` device nodes (`null`, `zero`, `random`, `urandom`, `fd`, stdio); procfs scoped to the sandbox's PID namespace.
- `--bind <extra> <extra> ...` — user-configured `extra_writable_paths` (toolchains, caches, shared directories).
- `--chdir <workspace>` — initial working directory matches tool-caller expectations.

The sandbox config flows from global `config.Config` through `runconfig.RunConfig` into `BashTool`; `WrapCommand` is the single choke-point through which all bash tool invocations pass.

## Startup detection and caching (ADR-0017a)

`BwrapAvailable()` caches the bwrap usability probe in a `sync.OnceValue` — the `exec.Command` check runs at most once per process lifetime, is logged at startup, and is surfaced in the Settings UI as a badge, giving immediate feedback on whether sandboxing is active and making the fallback discoverable. An explicit `"profile": "none"` config logs a startup warning that commands run without isolation. `WrapCommand` uses the cached result — the check cannot change during the process lifetime.

## Consequences

Positive:

- Defense-in-depth against prompt injection; works for unprivileged users; simple two-profile model with automatic fallback; transparent to the agent; extra writable paths as an escape hatch for legitimate needs.

Limitations at time of writing:

- **Home directory is read-only** under `--ro-bind / /` — `go install`, `pip install --user`, `gh auth login`, or anything writing to `~/.config`, `~/.cache`, or `~/.local` fails unless those paths are added via extra writable paths.
- **File tools are NOT sandboxed** — `read`, `write`, `edit`, `grep` operate on the host directly with path validation but no isolation.
- **Environment variables leak** (no `--clearenv`); a `$TMPDIR` pointing at a read-only path breaks tools that rely on temp files.
- **No seccomp filtering** — all syscalls allowed.
- **Network on by default**, reducing isolation; per-session sandbox configuration unsupported (setting is global); per-invocation bwrap process, no session reuse.

## References

- [bubblewrap README](https://github.com/containers/bubblewrap)
- [CVE-2017-5226](https://github.com/containers/bubblewrap/issues/142) — TIOCSTI terminal injection mitigated by `--new-session`
- [koder sandbox implementation](https://github.com/lkarlslund/koder/blob/main/internal/sandbox/bwrap.go) — inspiration for the `--ro-bind / /` default-read-only strategy
- [bubblewrap manual page](https://man.archlinux.org/man/bwrap.1)
