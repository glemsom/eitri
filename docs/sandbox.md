# The bash sandbox

`bash` is the only tool that touches your machine, so its boundary is a contract.
This page documents that boundary, how to widen it, and where its edges are. The
code that owns it is `internal/tools/sandbox.go` (the bwrap invocation) and
`internal/tools/registry.go` (the writable set it binds).

## What the sandbox is

Every sandboxed `bash` call is a fresh bubblewrap process. Eitri builds this
argument vector:

| Argument | Effect |
| --- | --- |
| `--die-with-parent` | The namespace dies with Eitri, so a crash cannot leak it. |
| `--share-net` | The host network is shared — see [Network](#network). |
| `--unshare-pid` | A private process namespace; `ps` and `kill` cannot see the host. |
| `--ro-bind / /` | The host root, read-only. |
| `--ro-bind <temp>/etc-ssh-config.d /etc/ssh/ssh_config.d` | A caller-owned copy of the system SSH drop-ins — see [SSH](#ssh). |
| `--proc /proc` | A `procfs` scoped to the private PID namespace. |
| `--dev /dev` | A `devtmpfs` replacing the host `/dev`. |
| `--tmpfs /dev/shm` | A private writable `/dev/shm`, which `devtmpfs` has none of. |
| `--bind <workspace> <workspace>` | The workspace, read-write. |
| `--bind <temp> <temp>` | The session temp, read-write. |
| `--bind <p> <p>` | One pair per configured extra-writable path. |
| `--setenv TMPDIR/TEMP/TMP <temp>` | The session temp is `$TMPDIR`. |
| `--chdir <workspace>` | The working directory is the workspace. |
| `/bin/bash -c <command>` | The command itself. |

Eitri runs the command in a user namespace, so the effective identity is your
own — reads are bounded by your permissions, and writes by the mounts above.

## The writable set

By default exactly two paths are writable:

| Path | Why |
| --- | --- |
| The **workspace** | The session's declared scope, writable by design. |
| The **session temp** | The per-session `$TMPDIR` for ephemeral artifacts. |

Everything else — the whole host root, **including `$HOME`** — is bound
read-only. Reads succeed where your user has permission; writes fail with
`Read-only file system`.

That is the right default, but it breaks every tool that keeps state under
`$HOME`, which on a systems-engineering host is most of the interesting ones:

| Tool | State it needs |
| --- | --- |
| `helm` | `~/.config/helm`, `~/.cache/helm` |
| `terraform` | `~/.terraform.d/plugins`, `TF_PLUGIN_CACHE_DIR` |
| `az`, `aws`, `gcloud` | `~/.azure`, `~/.aws`, `~/.config/gcloud` |
| `docker`, `podman` | `~/.docker`, `~/.config/containers` |
| `kubectl` | `~/.kube/cache` (reads work without it) |

## Widening it: `extra_writable_paths`

Add a path to the config and the sandbox binds it read-write:

```json
{
  "extra_writable_paths": ["/home/you/.kube", "/home/you/.config/helm"]
}
```

In the TUI, `/settings` → **Writable paths** manages the same list with a folder
picker; the config file is what CI reads, so set `EITRI_CONFIG` to point a
pipeline at its own copy.

The model is told the resolved writable set, so it does not have to discover the
boundary by writing outside it. Each run carries a per-run directive beside the
working-directory statement:

```text
## Working directory
You are operating in the workspace `/srv/work`. Resolve all relative paths against it.

## Write permissions
Writes are confined to `/srv/work`, `/home/you/.eitri/sessions/<guid>/tmp`, `/home/you/.kube`. Every other path is read-only.
```

`Registry.WritablePaths` is the single source of both that sentence and the
bwrap argument vector, so the statement cannot drift from what is enforced.

Under `--yolo-unsafe` the setting is inert — no sandbox is built, so there is
nothing to widen — and the run reports no writable set at all. The `bash` tool
description is what declares the absent sandbox; the run never implies a
boundary it does not enforce.

### Every configured path must exist

This is the sharp edge. A configured path that is missing — a typo, or a mount
that dropped out — makes bubblewrap refuse to start, so **every** `bash` call in
the run fails with:

```text
bwrap: Can't find source path /home/you/.kube: No such file or directory
```

Nothing validates the list at boot today, so the error surfaces per call rather
than once, up front. Until it does, keep the list to paths that exist on every
host that runs the job, and prefer a symlink to a stable location over a path
that depends on a live mount.

## Network

`--share-net` shares the host network stack: the run has the same egress your
user has, to every destination, on every port. This is deliberate — `curl` is a
declared tool and the `web-access` skill depends on it — and it means there is
no egress chokepoint inside a run. Combined with a writable workspace, treat any
untrusted repository or fetched page as able to reach whatever credentials your
user can read.

## SSH

System SSH drop-ins in `/etc/ssh/ssh_config.d` are copied into the session temp
and bound over the system location, dereferencing symlinks on the way. A drop-in
pointing into a host-root-owned directory (such as
`/usr/lib/systemd/ssh_config.d/`) is unreadable inside the namespace under the
user mapping; the copy is caller-owned and reads normally. Unix-socket
authentication — including `$SSH_AUTH_SOCK` — works under the read-only root.

## `--yolo-unsafe`

`--yolo-unsafe` builds no sandbox at all: `bash` runs directly as your user, and
the writable-set sentence is omitted rather than falsely claimed. The rest of
this page describes the sandboxed default only. Treat it as "the model may run
anything", and use it only with trusted prompts and workloads.

## Out of scope

The sandbox constrains namespaces and the writable set. It does not bound CPU,
memory, or process count — time is the only resource limit, 120 seconds by
default and 3600 at the ceiling.
