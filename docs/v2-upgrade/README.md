# Charm v2 upgrade guides

Reference copies of the upstream `UPGRADE_GUIDE_V2.md` files for the four
Charm v2 modules. The v1 → v2 port (issues #145–#149) is complete: all code
moved onto the v2 module paths, the v1 Charm modules and termenv were dropped
from `go.mod` (`go mod tidy`), and the migration's behavioral parity
(key strings, wheel scrolling, alt screen, colors, markdown) is covered by the
test suite + the manual TUI smoke test. These guides stay as reference material
for future Charm work.

| Library              | Module path                 | Version in go.mod | Source                                                |
| -------------------- | --------------------------- | ----------------- | ----------------------------------------------------- |
| bubbletea            | `charm.land/bubbletea/v2`   | v2.0.8            | https://github.com/charmbracelet/bubbletea            |
| bubbles              | `charm.land/bubbles/v2`     | v2.1.1            | https://github.com/charmbracelet/bubbles              |
| glamour              | `charm.land/glamour/v2`     | v2.0.1            | https://github.com/charmbracelet/glamour              |
| lipgloss             | `charm.land/lipgloss/v2`    | v2.0.6            | https://github.com/charmbracelet/lipgloss             |

Each file is named `<library>-UPGRADE_GUIDE_V2.md` and mirrors the upstream
`main` branch as of 2026-08-13.
