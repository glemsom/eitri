# 02 — Fix FileEditCard innerHTML swap eating the outer `<summary>` (browser "Details" label)

**What to build:** When `renderComponent('FileEditCard')` fires, it does `htmx.ajax(… target: wrapper.id, swap: 'innerHTML')`. This replaces **all** children of the outer `<details class="tool-entry-wrapper">`, including its `<summary class="tool-entry">`. The FileEditCard template renders as `<details class="tool-card file-edit-card">` — another `<details>` with its own `<summary>`. The outer `<details>` ends up without any `<summary>` child. Browsers render this with the native default label "Details" (or "Details" in English).

The fix depends on the chosen strategy:

**Option A (recommended):** Change `FileEditCard` template from `<details class="tool-card file-edit-card">` to `<div class="tool-card file-edit-card">`. The outer `<details class="tool-entry-wrapper">` already provides the collapsible structure with its `<summary class="tool-entry">`. The inner div simply contains the edit card body (header, file-path, diff/preview). No collapsible nesting needed.

**Option B:** Change the render endpoint to emit only the *body* of the FileEditCard (the `<div class="file-edit-body">`), keeping the outer `<details>` summary intact.

Choose one approach, implement, verify no leftover `<details>` nesting in eitri-stream.js or file_edit_card.templ.

**Blocked by:** 01 (unambiguous card matching ensures `renderComponent` targets the correct wrapper)

**Status:** ready-for-agent

- [ ] Change `FileEditCard` templ from `<details>` to `<div>` (or render-only-body approach)
- [ ] Verify sidebar shows properly: outer `<summary>` preserved, no "Details" label, inner content renders
- [ ] Verify chat-bubble FileEditCard (rendered inline in message history) still works
- [ ] Check `renderComponentsToHTML` and `handleRender` that might depend on `<details>` structure
- [ ] Check CSS for `.file-edit-card` selectors — adjust selectors if structural class changes
