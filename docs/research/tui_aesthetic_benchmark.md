# AI Agent TUI Aesthetic Benchmark

**Scope:** Visual design of terminal user interfaces for AI coding agents — layout, typography, color, status animation, and interactive polish. Model quality, tool capability, speed, and backend features are explicitly out of scope.

**Method:** Primary-source evidence where available. For open-source tools (OpenCode, Codex CLI, Pi, Gemini CLI, DeepSeek-TUI/CodeWhale, Aider, Goose) findings were pulled directly from source code — theme token files, layout constants, glyph charts, and rendering components. For closed-source Claude Code, findings come from official product documentation (theme token reference, statusline, fullscreen rendering, interactive-mode references) and observed usage. Screenshots were reviewed where obtainable. Scores are a designer's judgment on a 1–100 scale across four equal 25-point axes; treat them as a calibrated opinion, not a measurement.

### How the agents were tested and scored

**Testing procedure.** No interactive runtime sessions were run against any agent. Evaluation was static and evidence-based, in four passes:

1. **Source inspection.** For each open-source TUI, the rendering layer was located and read: theme token files (`opencode.json`, Pi's `dark.json`, Gemini's `default-dark.ts`), layout constants (Codex `ui_consts.rs`, OpenCode panel/padding props), glyph/spinner definitions (CodeWhale `glyphs.rs`, OpenCode `spinner.tsx`, Codex `frames/`), and animation components (`shimmer.rs`, `ambient_life.rs`, `bg-pulse.tsx`). Claims about colors, borders, spinners, and layout cite these files directly.
2. **Documentation review.** Official docs were the primary source for closed-source Claude Code (interactive-mode, statusline, fullscreen-rendering, and theme-token references) and for Goose's 2.x TUI (release blog with bundled screenshot).
3. **Screenshot review.** A small set of official screenshots was examined (Goose 2.x TUI, Claude Code statusline examples). Screenshots were corroboration only — animated behavior (spinner timing, shimmer sweeps, the CodeWhale ocean) was assessed from the code that drives it, not from motion capture.
4. **Scoring.** Each agent was scored on the four 25-point axes using anchored rubrics, then summed to a 1–100 Visual Score.

**Scoring rubric (25 pts per axis).**

| Axis | 0–10 (weak) | 11–18 (competent) | 19–25 (excellent) |
|------|-------------|-------------------|-------------------|
| **Layout & Spatial Hierarchy** | Raw scrollback stream; no panels, borders, or padding discipline | Some structure; boxes and gutters present but cluttered | Alt-buffer canvas; carded transcript; fixed composer; footer/statusline; directional borders; consistent spacing |
| **Color & Theme Consistency** | Ad-hoc ANSI colors; rainbow soup; no theme system | Named roles (user/error/diff); single palette; no tokens | Semantic token system; 15+ bundled themes; light/dark + daltonized presets; hot-reload; live-preview picker; one accent + gray ramps |
| **Status & Animation** | No status; blinking cursor or spinner default | Standard spinner; working label; basic stream layout | Braille/shimmer spinners at ~80 ms; tool-call framing with timers; statusline telemetry; reduced-motion gate; animated fallbacks |
| **Interactive Elements & Ergonomics** | Plain `>` prompt; no hints | Prompt_toolkit-style input; some keybinding hints | Composer with mode-colored border; vim mode; dropdown pickers with hover; command palette; platform-aware key hints; mouse support |

**Limitations.** (a) Static review cannot capture feel: flicker, hover latency, streaming jitter, and real-world clutter under long agentic runs are unverified. (b) Claude Code and Goose 2.x were evaluated without runtime access; their scores rest on documented behavior. (c) Animation quality was judged from implementation (frame sets, phase math, motion budgets) rather than perception. (d) Scores are a single designer's calibrated judgment — treat the leaderboard as an opinion with an auditable evidence trail, not a measurement.

---

## 1. Executive Summary

The AI agent TUI is converging on a recognizable visual grammar, roughly twelve months after the category exploded. The terminal — once a place of raw streaming text — is being treated as a *canvas*: alternate-screen buffers (the "fullscreen" pattern pioneered by vim/htop) have replaced naive scrollback printing; fixed input boxes dock to the bottom; transcripts collapse into expandable tool-call cards; and a statusline/footer strip carries session telemetry.

Five trends define the modern look:

1. **Semantic color tokens, not raw ANSI.** The best tools (Claude Code, Pi, OpenCode, Gemini CLI) define named tokens — `accent`, `border`, `diffAdded`, `userMessageBg` — and ship full theme systems with live preview pickers. Color is *design system*, not decoration. "Rainbow soup" is now a known failure mode; the winning palettes use one brand accent, gray ramps for hierarchy, and color strictly for semantic states (error, success, tool, diff).

2. **Message bubbles and tool-call cards.** User messages get a subtle background fill (`#343541`-style grays), assistant output stays on the base background, and tool calls become bordered cards with a label, dimmed file paths, and elapsed time. The card is the fundamental unit of the modern agent transcript.

3. **A one-line status strip with real data.** The bottom of the screen is prime real estate: spinner + working label, model name, elapsed time, token/context meter, git branch, interrupt hint. Claude Code and Pi both make it *scriptable* — the statusline runs a shell command and renders arbitrary telemetry.

4. **ASCII/Unicode craftsmanship.** Box-drawing with directional borders (OpenCode renders only the leading edge of a panel), half-block logos, braille spinners at 80 ms, shimmer gradients, and carefully chartered glyph sets (`● ○ ▸ ▎ ✓ ✕ ◆ ⏸`). The best projects define a glyph charter and an ASCII fallback rather than sprinkling characters ad hoc.

5. **Motion with restraint — and an off switch.** Shimmer, pulsing, and even ambient background life (DeepSeek-TUI's underwater fish school) exist, but the leaders pair every animation with a `prefers-reduced-motion`-style kill switch and a static fallback. Motion earns points when it *informs* (working vs. idle); it loses points when it decorates.

---

## 2. Scorecard Leaderboard

| Rank | TUI | Paradigm | Primary Visual Strengths | Score |
|------|-----|----------|--------------------------|-------|
| 1 | **Claude Code** | Fullscreen dashboard (alt-buffer transcript + fixed composer + statusline/footer + diff panel) | Deepest theme token system (shimmer accents, 8 subagent colors, daltonized presets), collapsible tool cards, scriptable statusline, mouse ergonomics | **93** |
| 2 | **OpenCode** | Minimalist stream with directional-border panels + sidebars | 30+ curated themes, distinctive peach/charcoal default, per-agent colors, airy spacing, braille spinner with toggle | **88** |
| 3 | **Codex CLI** | Composer-centric multi-pane (history cells + exec cells + overlays) | Cyan-accent restraint, tinted message bubbles, live theme picker, ASCII art spinners, terminal pets | **85** |
| 4 | **Pi** | Pane-based workbench (transcript + footer telemetry + select lists) | Most complete semantic token map (thinking-level borders, tool-state backgrounds), ChatGPT-gray bubbles, scriptable footer | **84** |
| 5 | **Gemini CLI** | Session browser + composer with gradient branding | 15+ builtin themes, `ThemedGradient` accents, half-block ASCII logo, dense status row of indicators | **80** |
| 6 | **DeepSeek-TUI (CodeWhale)** | Ambient maximalist stream | Unique animated ocean-life backdrop, disciplined glyph charter, density-aware composer | **74** |
| 7 | **Goose** | Split-panel transcript (1.x ratatui → 2.x TypeScript TUI) | Clean markdown + syntax highlighting; design language in transition | **72** |
| 8 | **Aider** | Classic minimalist single-stream | Best-in-class diff contrast; honest, functional, deliberately plain | **60** |

---

## 3. Detailed TUI Evaluations

### 3.1 Claude Code — 93/100 (Anthropic)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 23 | Fullscreen alt-buffer rendering (vim/htop-style) with a fixed bottom composer, collapsible tool-call cards, transcript mode (`Ctrl+o`), and a diff panel. Clear stacking: transcript → tool cards → statusline → footer badges. |
| Color & Theme Consistency | 24 | The most complete documented token system in the category: brand `claude` accent, `claudeShimmer`, plan-mode borders, diff background + word-level tokens, daltonized presets (`dark-daltonized`, `light-daltonized`), 8 named subagent colors, auto light/dark detection. |
| Status & Animation | 23 | Spinner is a shimmer *gradient* (paired base+shimmer tokens), custom scriptable statusline, `/usage` meter with `rate_limit_fill` tokens, PR-status footer badges, prompt suggestions. |
| Interactive Elements & Ergonomics | 23 | Vim modes, `?` help panel, `/` and `@` dropdown pickers with hover, mouse capture (click-to-expand tool results, in-app selection, double/triple-click word/line selection), voice input, `/theme` live-preview picker. |

**Visual breakdown.** Fullscreen mode draws the conversation on the alternate screen buffer: only visible messages render, the input box never moves, and collapsed tool results show as one-line cards that expand on click. The footer stacks a customizable statusline above built-in badges — keyboard hints (`esc to interrupt`, `? for shortcuts`, `hold space to speak`), model, and clickable link badges (e.g., a `PR #446` link with a colored underline encoding review state). Messages in fullscreen get background fills (`userMessageBackground`), with distinct fills for bash entries and memory entries. The prompt input shows an accent-colored border that changes by mode (`promptBorder`, `planMode`, `autoAccept`, `bashBorder`), and autocomplete suggestions render as a dropdown list under the input.

**Standout visual features.**
- **Tokenized everything:** every pixel color is a documented, overridable token; custom themes are JSON files hot-reloaded from `~/.claude/themes/`, and plugins can ship themes.
- **Shimmer variants:** each accent has a paired lighter shimmer color so the spinner reads as an animated gradient, not a blinking char.
- **Subagent color coding:** parallel tasks render in eight named colors (`blue_FOR_SUBAGENTS_ONLY` etc.) for instant visual separation.
- **Playful detail:** typing `ultrathink` renders the keyword in a seven-color rainbow gradient with shimmer.
- **Accessibility:** daltonized presets, reduced-motion support, screen-reader mode.

**Drawbacks.** Tool-call cards can stack densely in long agentic runs; the classic (non-fullscreen) renderer is noticeably dated and flickers. The rainbow/ultrathink flourish is charming but sits at the edge of restraint. Closed source means the token system is the contract — custom themes are the only way to push the palette further.

---

### 3.2 OpenCode — 88/100 (SST / anomalyco)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 22 | Directional borders — panels render only their leading edge (`border: ["left"]` / `["top"]`), producing an "open panel" look with unusual air. Diff viewer splits a file tree from the diff with panel rails. Generous padding (2-col input gutter). |
| Color & Theme Consistency | 23 | Full semantic theme object (`primary`, `backgroundPanel`, `borderActive`, `diffAddedBg`, …) with 30+ hand-ported themes (catppuccin, tokyonight, dracula, gruvbox, kanagawa, flexoki, …) and light/dark pairs. Default "opencode" theme: near-black `#0a0a0a` background, peach `#fab283` primary, blue `#5c9cf5` secondary, purple `#9d7cd8` accent — an unusual, brandable default. |
| Status & Animation | 22 | Braille spinner (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`, 80 ms) in muted text color, `⋯` static fallback, a user-facing `animations_enabled` toggle, animated background pulse art on the startup/upsell screens, todo tracking. |
| Interactive Elements & Ergonomics | 21 | Command palette, dialog overlays for model/provider/theme/workspace/session lists, path autocomplete with frecency ranking, per-agent metadata row inside the prompt panel. |

**Visual breakdown.** The transcript is a clean single stream; the composer is a left-bordered box whose border color changes to the *active agent's* color in multi-agent sessions, with a metadata row (agent name, model, `auto` mode) above the input. Panels use directional borders — a diff sidebar draws a vertical rail with edge caps (`┐`-style glyphs at ends) rather than a full box. The logo is a two-tone ASCII lockup with half-block shadow tinting (`▀`/`▄` blended against background at 25%).

**Standout visual features.**
- **Per-agent border coloring** — the prompt frame visually "belongs" to whoever is answering.
- **Theming as a first-class product**: `/theme`-style dialog with live switching; themes are plain JSON with a published schema.
- **Spacing discipline:** padded prompt, gutter alignment, low-density hierarchy — never cramped.
- **Fade/alpha compositing** for agent metadata during streaming (colors fade in as output arrives).

**Drawbacks.** The extreme airiness wastes vertical space on dense agent runs; the peach default divides opinion (it reads warm/novel but can clash with cool terminal themes). Sidebars and dialogs occasionally add chrome that the minimal stream doesn't strictly need.

---

### 3.3 Codex CLI — 85/100 (OpenAI)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 21 | Composer docked at the bottom with history cells above (messages, exec cells, plans, approvals). Status row sits *above* the composer to avoid vertical churn. Overlays for approvals and pickers float over the transcript. |
| Color & Theme Consistency | 22 | Textbook restraint: cyan bold accent on dark, white-at-12%-alpha tint for user message "bubbles", table separators blended to 20% alpha so rules never compete with content, light-background detection swaps accent to `(0,95,135)`. Theme picker supports `.tmTheme` files with a live side-by-side preview and cancel-restore. |
| Status & Animation | 21 | Custom ASCII-art spinner frames (the OpenAI flower in letters; a grid/blocks animation), `shimmer` text sweeps, `motion.rs` activity indicator with a `ReducedMotionIndicator`, status widget with elapsed time (`0s`, `1m 00s`) and a `└ `-prefixed details tree, and terminal **pets** (ASCII creatures, sixel-capable) rendered in the corner. |
| Interactive Elements & Ergonomics | 21 | Vim-mode textarea, styled key hints (`⌥ + ` / `alt + ` / `ctrl + ` rendered as spans), file-search and skill popups, config editor, fuzzy pickers, history search, transcript view. |

**Visual breakdown.** The left gutter reserves 2 columns (`▌ `) for user message markers, and all live cells align to it — a quiet but effective alignment device. User messages sit on a barely-visible tinted background; assistant text streams with markdown rendering and inline syntax highlighting. Diff lines are rendered with dedicated style contexts (added/removed/word-level) and line-number gutters. The composer grows with content and hosts slash-command completion. The palette is *terminal-native*: it probes the terminal's true color level and degrades gracefully from 24-bit → 256 → 16 → plain dim.

**Standout visual features.**
- **Theme picker with live preview** — navigate the list and the syntax theme swaps in real time, Esc restores.
- **Pets.** An ASCII animal in the corner of the terminal is the single most memorable visual in the category — playful, optional, and technically serious (sixel/kitty image protocol).
- **Perceptual color math**: `color.rs` implements CIE76 Lab distance and alpha blending to compute readable separators and backgrounds.
- **Token-usage chart** with its own palette module.

**Drawbacks.** The aesthetic is utilitarian — "clean engineering" rather than "designed." Panels and popups can feel boxy; no branded default palette (accent is plain cyan); the message-bubble tint is so subtle it's easy to miss. Pets and shimmer are charming but sit on a comparatively drab base.

---

### 3.4 Pi — 84/100 (earendil-works)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 21 | Pane-based workbench: transcript + footer telemetry strip + select-list overlays (sessions, models, themes, settings). Dynamic border component adapts to width. Clear component library (`box`, `v-stack`, `scroll-view`, `select-list`). |
| Color & Theme Consistency | 22 | The deepest semantic token map in the survey: 60+ tokens covering message backgrounds, **six thinking-level border colors** (`thinkingLow` `#5f87af` → `thinkingXhigh` `#d183e8`), tool pending/success/error backgrounds (`#282832` / `#283228` / `#3c2828`), full markdown + syntax-highlight tokens, diff tokens, scrollbar, search-match. Hot-reloaded custom themes with a JSON schema. |
| Status & Animation | 21 | Loader components with accent spinner + muted message; status indicators for working / retry (with **countdown timer** and interrupt hint) / compaction / branch summary; footer shows pwd, token counts (compact `1.2k`/`3M` formatting), context usage, git branch. |
| Interactive Elements & Ergonomics | 20 | Keybinding hints rendered from a central keymap (`ctrl+t`-style, platform-aware `option` on macOS), fuzzy search, alt-screen search, autocomplete, select-lists, settings lists, image support in terminal, model/theme/session selectors. |

**Visual breakdown.** Pi's default dark theme is a ChatGPT-adjacent palette: text `#d4d4d4` on near-black, user messages on `#343541` gray bubbles, accent `#8abeb7` (teal) with blue `#5f87ff` borders and cyan `#00d7ff` active borders. Tool executions are color-coded *by outcome* — pending, success, error each get their own background. Thinking blocks get border colors scaled by reasoning effort, so a "deep think" visibly changes the frame color. Markdown is fully themed (headings, links, code blocks with borders, quotes, list bullets).

**Standout visual features.**
- **Thinking-level borders:** effort is encoded as color, turning a hidden model property into readable UI state.
- **Outcome-coded tool cards:** status colors live on backgrounds, not just text.
- **Scriptable footer** with token/context telemetry and compact formatting.
- **Design-system rigor**: schema-validated themes (`theme-schema.json`), var-reference indirection, optional `export` for custom formats.

**Drawbacks.** The palette is safe to the point of being derivative (ChatGPT grays); the teal/blue/cyan trio is pleasant but not distinctive; interactive surface is broad but deeper states (settings lists) lean functional. Default dark theme uses ANSI-index values (`#00d7ff`-style 256-color indices) rather than full 24-bit in places.

---

### 3.5 Gemini CLI — 80/100 (Google)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 20 | Session-browser + composer architecture: an ASCII-logo header, a dense StatusRow (loading, context usage, approval/shell/raw-markdown indicators), toasts, queued-message display, suggestions dropdown; `HalfLinePaddedBox` for fine spacing control. Narrow-terminal breakpoints reflow the header and status row. |
| Color & Theme Consistency | 21 | 15+ builtin themes (tokyonight, dracula, solarized, ayu, shades-of-purple, github colorblind…) across dark/light with a `no-color` mode, semantic tokens (`text`, `border`, `status`, `ui`), and full highlight.js syntax color mapping per theme. |
| Status & Animation | 20 | `CliSpinner` (ink-spinner) with a `showSpinner` setting, phrase cycler for working labels, loading/context-usage displays, shell-mode and approval-mode indicators, background-task display. |
| Interactive Elements & Ergonomics | 19 | `ThemedGradient` branding, suggestions with keyboard navigation, `@`-command autocomplete, reverse-history search, vim mode, model picker, session switcher with search, `AboutBox`. |

**Visual breakdown.** The header is a half-block ASCII logo (`▝▜▄ ▗▟▀`-style) rendered through `ThemedGradient` — a two-color gradient pulled from the active theme, with a special vertically-symmetric variant for macOS Terminal.app whose line padding would otherwise break the half-block art. The StatusRow is a dense strip of live indicators above the composer: working phrase, context usage, approval mode, shell mode, raw-markdown mode, tips. Input is a full Ink composer with suggestions displayed above or below depending on buffer.

**Standout visual features.**
- **Gradient accents** — `ink-gradient` over theme colors is rare in this category and gives the header genuine brand polish.
- **Terminal-aware art**: a dedicated logo variant for Apple Terminal's line-height quirk, "scanline" intentionality.
- **Indicator density**: mode badges (approval/shell/raw-markdown) make the composer's current state legible at a glance.
- **Theme breadth with semantics**: themes map to semantic tokens, so any palette stays coherent.

**Drawbacks.** The session-browser/menu screens are utilitarian lists; the StatusRow is information-dense to the point of clutter at narrow widths; gradient use is confined to branding, so the transcript itself reads plain. The experience is "solid web-adjacent polish" more than "terminal-native craft."

---

### 3.6 DeepSeek-TUI / CodeWhale — 74/100 (Hmbown)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 19 | Composer with density modes (compact/comfortable/spacious) that bound growth instead of forcing height, quiet-rule vs. enclosed-panel chrome, command palette, context menu, hotbar, footer, hover layers. Real layout engineering, occasionally at the cost of simplicity. |
| Color & Theme Consistency | 17 | Strong *glyph* charter (below) and a user-theme JSON schema, but the runtime look is maximalist; the palette competes with the animation layer rather than supporting it. |
| Status & Animation | 20 | **Ambient ocean life**: a school of fish, occasional jellyfish, bubbles, and a rare whale cameo swim behind the transcript with parallax depth layers, sin² wave motion, deliberately de-synced phase clocks, and full respect for reduced motion (zero marks painted). Braille-fill fallbacks when Braille glyphs are unavailable. |
| Interactive Elements & Ergonomics | 18 | Command palette, context menus, hover layers, hotbar, focus texture, keybinding system with platform-aware hints, ASCII fallback for every decorative glyph. |

**Visual breakdown.** The most opinionated TUI in the survey. Where others keep the background flat, CodeWhale paints an animated underwater field *behind the text*: fish swim on wrap-around paths (facing = velocity), a single jellyfish visits ~1/5 of each ~5-minute cycle dimmer than everything else, bubbles glint on raised-cosine curves. Motion is engineered, not slapped on: two clocks drive it (wall-clock for speed, transcript width for placement), frames are never requested just for animation, and a per-frame budget counter exists. The glyph charter is genuinely disciplined: `●` current, `○` available, `▸` selection, `▎` user marker, `▏` transcript rail, `✓`/`✕` done/failed, `◆` attention, `⏸` paused, plus role marks (`■` builder, `◇` reviewer, `▲` synthesizer) — with a full ASCII fallback map (`╭→+`, `●→.`).

**Standout visual features.**
- **The ocean.** Nothing else in the category animates the background. It is memorable, technically impressive, and a category of one.
- **Semantic glyph charter with ASCII degradation** — decorative characters are a design system, not accidents.
- **Density-aware composer chrome** — borders shed before content when space shrinks.

**Drawbacks.** The ocean is the flaw as much as the feature: it is the definition of visual noise, adds cognitive load behind long transcripts, and its charm fades across a workday. The palette leans plain dark while the animation layer demands attention, so the two layers argue. Density modes + palette + hover layers + hotbar add up to a heavy surface for a single-stream chat.

---

### 3.7 Goose — 72/100 (Block)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 18 | 1.x ratatui TUI: split-pane transcript with separated thought/action/tool regions. 2.x TypeScript TUI (beta): cleaner single-stream with markdown + syntax highlighting. Design language is mid-transition. |
| Color & Theme Consistency | 18 | Consistent dark theme with accent colors for thinking vs. tool actions in 1.x; 2.x ships a neutral modern look. No published token system to date. |
| Status & Animation | 18 | Standard working indicators, streaming output, tool-call framing. Nothing distinctive. |
| Interactive Elements & Ergonomics | 18 | Session management, slash commands, extension UI; 2.x adds rendered markdown and syntax-highlighted code blocks. Functional, not novel. |

**Visual breakdown.** The 1.x TUI (Rust/ratatui) presented a two-region transcript: the assistant's *thoughts* and *actions* in visually distinct panels, with a header carrying model/provider info and a status line below. The 2.x rewrite (TypeScript, ACP-based, `npx @aaif/goose`) moves to a single-stream chat with rendered markdown and syntax-highlighted code — the screenshot in the 2.0 announcement shows a clean, modern dark interface focused on tool activity and file diffs. The design direction is right (leaner, web-fluent), but it is a beta mid-flight.

**Standout visual features.**
- Distinct thought-vs-action framing in 1.x — an early and influential pattern for separating reasoning from tool use.
- 2.x's streamlined single-stream is a step toward the OpenCode/Claude Code grammar.

**Drawbacks.** Nothing visually memorable — no branded palette, no signature motion, no glyph system. The 1.x UI showed its ncurses-era lineage (boxy panels, default borders); the 2.x TUI is clean but generic. Two rewrites in a year means the aesthetic identity has never settled.

---

### 3.8 Aider — 60/100 (Paul Gauthier)

**Score breakdown**

| Axis | Pts | Why |
|------|-----|-----|
| Layout & Spatial Hierarchy | 14 | Linear stream, no panels, no alt-buffer. A header block lists repo files and model info; the prompt is `edit_format> ` with a file list above it. Honest and dense, but architecturally 2015. |
| Color & Theme Consistency | 16 | No theme system; colors are named ANSI roles (`user_input` blue, `tool_error` red, `tool_warning` `#FFA500`, `assistant_output` blue) on the terminal's own palette. The **diff rendering** is the exception: green/red added/removed lines with near-universal contrast — the category's benchmark for diff legibility. |
| Status & Animation | 15 | Minimal by design: a waiting indicator and session timer; streaming text; no spinners, no cards, no meters. |
| Interactive Elements & Ergonomics | 15 | prompt_toolkit input with **Pygments syntax highlighting inside the input line**, modal cursor shape, vi mode, multiline, threaded autocomplete, completion-menu styling. Underappreciated input ergonomics on a bare stage. |

**Visual breakdown.** `Aider v0.x` prints a banner, a file list, then streams conversation with colored roles. The prompt is a plain `>` prefixed by the edit format name (e.g., `udiff> `). Diffs are rendered inline with added/removed line colors. It inherits the terminal's theme entirely — no background fills, no borders, no cards, no statusline. The famous tradeoff: "it looks like 2015, and it is deliberately that way."

**Standout visual features.**
- **Diff contrast discipline** — added/removed lines are readable in any terminal theme; word-level highlighting in search/repair flows.
- **Syntax-highlighted prompt input** — typing *is* the polish; the input lexes your message as you write it.

**Drawbacks.** No hierarchy, no status, no theming, no motion. The file-list header is a wall of path text. Everything the modern grammar adds — cards, borders, tokens, spinners — is absent. As the category's origin point it's indispensable history, but visually it anchors the bottom of this benchmark.

---

## 4. Modern TUI Design Playbook

Synthesis of the patterns that separate a 90s-feeling agent TUI from a modern one.

### 4.1 Layout: build a canvas, not a log

- **Use the alternate screen buffer.** Claude Code's fullscreen mode, vim/htop-style, eliminates flicker, keeps the composer fixed, and makes the terminal a designed surface. Scrollback-printing is the signature of a legacy tool.
- **Dock the composer.** Bottom-anchored input with a border that encodes mode (Claude Code: `promptBorder`/`planMode`/`bashBorder`; OpenCode: per-agent border color). Status row sits *above* the composer (Codex) so it doesn't jump during streaming.
- **Card the transcript.** User message = subtle background fill (≈12% white over dark, e.g. `#343541`); tool call = bordered card with label + dim path + elapsed time, collapsible (Claude Code, Codex, Pi). Assistant text stays on the base background — the contrast between filled and unfilled regions does the hierarchy work.
- **Reserve a one-line footer/status strip.** Spinner, working label, model, elapsed time, token meter, git branch, interrupt hint. Make it scriptable (Claude Code `/statusline`, Pi footer) — telemetry as UI.
- **Prefer directional borders.** OpenCode's leading-edge-only borders read as "open panels" and cut visual noise versus full boxes. When full boxes are needed, use them sparingly (Codex overlays, Goose 1.x shows the boxy cost).

### 4.2 Color: tokens, ramps, and one accent

- **Semantic tokens everywhere.** `accent`, `border`, `textMuted`, `diffAdded`, `userMessageBg`. The token *name* is the contract; terminals degrade through 24-bit → 256 → 16-color → none (Codex's `terminal_palette` probing is the model to copy).
- **One brand accent + gray ramps.** Claude Code's single `claude` token, OpenCode's peach on `#0a0a0a`, Pi's teal. Hierarchy from a 6–8 step gray ramp, color reserved for meaning: success/error/warning/tool/diff.
- **Diff colors are the one place to be loud.** Green/red (or theme-aware added/removed) with background fills and word-level highlights (Claude Code's `diffAddedWord`, Pi's `toolDiffAdded`). Aider proves low-effort diff coloring still wins.
- **Theme as a product:** 15–30+ bundled themes, JSON custom themes hot-reloaded, live-preview picker with cancel-restore (Codex), daltonized presets (Claude Code). If a user can restyle your TUI without restarting, it's a design system.
- **Avoid rainbow soup** — but one sanctioned exception is fine (Claude Code's `ultrathink` gradient is a wink, not a palette).

### 4.3 Status & animation: motion that informs

- **Braille spinners at ~80 ms in muted color** (OpenCode, Pi), with a static `⋯` fallback and a user-facing animation toggle. Never a blinking default char.
- **Shimmer/phase-based motion** beats frame-churn: Claude Code's shimmer gradient sweeps, Codex's time-synced shimmer text, CodeWhale's sin² waves. All respect reduced motion — CodeWhale paints *zero* marks under reduced-motion, and Codex/Claude Code gate animation.
- **Encode state as color**: Pi's thinking-level borders and tool-outcome backgrounds; Claude Code's mode-colored input borders. The color of a frame should tell you what's happening.
- **Animate only where the user looks.** Spinners at the working label, shimmer on the loading token, subtle pulse on startup art. The one cautionary tale: CodeWhale's ocean — brilliant, and proof that ambient animation costs attention.

### 4.4 Interactive & ergonomic polish

- **One consistent keybinding hint system**, rendered from a central keymap with platform-aware modifiers (`⌥` on macOS, `alt` on Linux — Codex, Pi).
- **Dropdowns/palette with hover highlight** (Claude Code's mouse capture: pointer on hover, click-to-accept, click-to-expand tool results, in-app selection). If you render a list, make it clickable.
- **Live-preview pickers** for anything visual (theme, model, session) — the instant-revert preview is the gold standard interaction.
- **Autocomplete inside the input** with Pygments-style lexing (Aider) and file-path dropdowns (Claude Code, OpenCode's frecency).
- **Vim mode** as table stakes (Claude Code, Codex, Gemini, Aider) — terminal users expect it.

### 4.5 The checklist for "modern"

| Pattern | Old | Modern |
|---|---|---|
| Buffer | Scrollback print | Alt-screen, fixed composer |
| Message | Plain text | Bubble fills + cards |
| Tool call | Raw command dump | Collapsible card, dim path, timer |
| Colors | Raw ANSI per line | Semantic tokens + theme system |
| Borders | Full boxes everywhere | Directional edges, sparse |
| Motion | Blinking cursor | Braille spinner, shimmer, reduced-motion gate |
| Status | Nothing | Scriptable statusline + footer badges |
| Input | `> ` | Highlighted composer, mode-colored border, vim, dropdowns |

If a TUI hits all eight, it reads as modern; each miss reads as a generation older.
