# Declared toolset: three dependency tiers

Eitri is a single static binary whose agent prompt promises a fixed toolset
unconditionally, so a run must never start on an incomplete toolset. We split
dependencies into three tiers with distinct boot behavior: a **declared
toolset** — the hard substrate `bwrap` (bubblewrap) and `bash`, plus the
declared tools `rg` (ripgrep), `curl`, `lynx`, `patch`, and `python3` — that
Eitri verifies at boot and refuses to start without, naming every missing tool
with a per-distro install hint; **soft dependencies** — `git` and a browser
launcher such as `xdg-open` — that are opportunistic, never gated at boot,
and surface only if the agent reaches for them (a missing `git` yields a
single non-fatal boot notice; a missing browser backend surfaces only when
`open_in_browser` runs); and a **base toolset** — the coreutils `grep`, `sed`,
`awk`, `cat`, `nl`, `diff` — assumed present without a check. Checking the
declared tier fatal-first keeps the prompt's unconditional promises honest,
while leaving soft and base tiers ungated keeps installs minimal on slim
hosts.