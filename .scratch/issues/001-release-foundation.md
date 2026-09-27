# Release foundation: GPL licensing, versions, and changelog

**Status:** Resolved

## Goal
Establish the source-level metadata and documentation baseline for Eitri's first public release, `v0.1.0`.

## Scope
- Add the canonical GNU GPL version 3 license text as `LICENSE` and declare the project license as `GPL-3.0-or-later` where repository metadata/documentation calls for it.
- Add `CHANGELOG.md` using Keep a Changelog and Semantic Versioning conventions:
  - an `[Unreleased]` section;
  - a dated `0.1.0` section containing `Initial public release.`;
  - categories/entry rules suitable for user-visible behavior, dependency and compatibility changes, security fixes, and deprecations.
- Change the source fallback version from `0.1.0-dev` to `0.1.1-dev`.
- Update `CONTEXT.md` with the durable release/distribution terminology and policy: GNU/Linux `amd64` static release binary, release tags as the publication boundary, and immutable published releases. Preserve existing glossary language and avoid duplicating implementation detail.

## Acceptance criteria
- `LICENSE` is the complete GPLv3 text and clearly grants `GPL-3.0-or-later` licensing.
- `CHANGELOG.md` has the expected Unreleased and `0.1.0` headings and does not attempt to reconstruct pre-release history.
- A source build without an overriding build-time version reports `0.1.1-dev`.
- `CONTEXT.md` accurately records the release model without contradicting its Linux-only boundary.

## Out of scope
- GitHub Actions, archive construction, installer implementation, and release publication.

## Dependencies
None.
