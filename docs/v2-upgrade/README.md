# Charm v2 upgrade guides

Reference copies of the upstream `UPGRADE_GUIDE_V2.md` files for the four
Charm v2 modules, captured for the v1 → v2 port passes (see issue #144 and
follow-ups #145–#149).

The v2 modules are already acquired in `go.mod` alongside the v1 modules;
these guides are the reference material the port passes work from.

| Library              | Module path                 | Version in go.mod | Source                                                |
| -------------------- | --------------------------- | ----------------- | ----------------------------------------------------- |
| bubbletea            | `charm.land/bubbletea/v2`   | v2.0.8            | https://github.com/charmbracelet/bubbletea            |
| bubbles              | `charm.land/bubbles/v2`     | v2.1.1            | https://github.com/charmbracelet/bubbles              |
| glamour              | `charm.land/glamour/v2`     | v2.0.1            | https://github.com/charmbracelet/glamour              |
| lipgloss             | `charm.land/lipgloss/v2`    | v2.0.6            | https://github.com/charmbracelet/lipgloss             |

Each file is named `<library>-UPGRADE_GUIDE_V2.md` and mirrors the upstream
`main` branch as of 2026-08-13.
