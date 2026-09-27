# Add the Bash release installer and public installation guidance

## Goal
Let GNU/Linux amd64 users install the latest stable Eitri release, or a pinned release, into `~/.local/bin` with one Bash command.

## Scope
- Add a tracked `install.sh` with Bash as its interpreter/runtime.
- Default behavior:
  - discover the latest stable GitHub Release through the GitHub Releases API, then download its only versioned `eitri_<version>_linux_amd64.tar.gz` asset;
  - install into `~/.local/bin`;
  - overwrite an existing `eitri` target without prompting;
  - use a temporary file/directory and atomic rename for the final installed binary.
- Support `--version 0.1.0` to fetch `releases/download/v0.1.0/eitri_0.1.0_linux_amd64.tar.gz` and an install-directory option.
- Detect required commands and report actionable errors. Do not use `sudo`, modify shell startup files, or prompt interactively.
- Print the installed version and path. When the target directory is absent from `PATH`, print (but do not apply) the required `PATH` export command.
- Update README installation material with:
  - the latest-stable curl-to-Bash command;
  - a version-pinned command whose script URL and archive version are both pinned;
  - a link to release procedure documentation once available.
- Add focused tests or testable seams for argument parsing, URL selection, and failure/success behavior without downloading a real GitHub release in CI.

## Acceptance criteria
- `install.sh --version 0.1.0` selects the expected tag and artifact name.
- Default selection discovers the latest stable GitHub Release through the GitHub Releases API and downloads its only versioned archive asset.
- Successful installation overwrites an existing target atomically and preserves executable mode.
- A missing prerequisite or invalid option exits nonzero with a useful message.
- README documents both latest and deterministic pinned installation paths.

## Out of scope
- Checksums, signatures, SBOMs, and automatic edits to user shell configuration.

## Dependencies
- [002-production-linux-amd64-build-and-ci.md](002-production-linux-amd64-build-and-ci.md) (public archive name and layout).
