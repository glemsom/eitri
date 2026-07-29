# 0022 — Save-only settings drafts

**Status:** Accepted
**Date:** 2026-07-29

## Context

Settings currently mix explicit Save with automatic persistence for some fields, such as model selection. That creates unclear user expectations and races: users can believe changes are still draft while parts of the config have already been written, and provider-dependent fields like Base URL and Model can fall out of sync.

## Decision

Settings config edits live in a Settings draft and are persisted only by Save. Provider changes update dependent draft fields immediately — notably Base URL — but do not write config until Save validates the whole draft and persists it atomically. Base URL is always visible and editable, built-in Providers reset it to their default endpoint on provider change, and Custom OpenAI clears it so the user must enter the endpoint intentionally.

Test Connection validates the current draft without saving by calling provider model discovery with the draft Provider endpoint and credentials. It verifies that credentials work, refreshes available Models, and marks the selected Model as verified or unverified for the current draft. Save performs the same provider/model validation before persisting, so the user may either Test Connection first or Save directly.

## Consequences

Field-level autosave is intentionally not used in the Settings config form. The UI must show dirty state, support Revert, warn before discarding unsaved Settings drafts, and make it clear that saved config is applied to the next agent run rather than mutating an active run mid-stream.
