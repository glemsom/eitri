# Build and verify the static GNU/Linux amd64 release artifact

**Status:** Resolved

## Goal
Make the production artifact a reproducible, fully static GNU/Linux `amd64` Eitri binary and exercise that exact build in GitHub CI.

## Scope
- Define a production build/package target that:
  - uses `CGO_ENABLED=0`, `GOOS=linux`, and `GOARCH=amd64`;
  - embeds the release version without a leading `v` (`v0.1.0` -> `0.1.0`);
  - emits `eitri_0.1.0_linux_amd64.tar.gz` for a release version;
  - places only `eitri` and `LICENSE` at the archive root;
  - uses the existing normal-build stripping behavior unless it conflicts with the release contract.
- Ensure local non-release builds retain useful Git/source version behavior while release builds do not embed an unhelpful `git describe` value.
- Add production-build validation to CI on pull requests and `main`:
  - build the production `linux/amd64` artifact;
  - assert the ELF is fully static (`ldd` must report it is not a dynamic executable);
  - inspect archive contents so the public artifact contract cannot drift.
- Keep the existing `go vet`, race-test, and informational coverage checks intact.

## Acceptance criteria
- The package name for version `0.1.0` is exactly `eitri_0.1.0_linux_amd64.tar.gz`.
- Extracting the archive yields exactly `eitri` and `LICENSE` at its root.
- `eitri --version` from the release build prints `0.1.0`.
- `ldd` validates the release binary as non-dynamic.
- The production build/validation runs in CI for both PRs and pushes to `main`.

## Out of scope
- Creating GitHub Releases and implementing the shell installer.

## Dependencies
- [001-release-foundation.md](001-release-foundation.md) (`LICENSE` and version baseline).
