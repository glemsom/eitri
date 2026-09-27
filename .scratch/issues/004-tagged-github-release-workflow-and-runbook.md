# Publish tagged GitHub releases and document the release runbook

**Status:** Resolved

## Goal
Publish a verified `vX.Y.Z` tag as a GitHub Release whose artifact and release notes match Eitri's documented release contract.

## Scope
- Add a tracked Bash helper that extracts exactly one matching release section from `CHANGELOG.md` for a supplied version; fail on absent or malformed headings. Test it against representative changelog fixtures/cases.
- Add a GitHub Actions release workflow triggered by stable SemVer tags only (`v[0-9]+.[0-9]+.[0-9]+`).
- Before publishing, require the tagged commit to be reachable from `main` and rerun the complete quality gate:
  - declared runtime dependencies;
  - `go vet`;
  - race-enabled tests;
  - informational coverage;
  - production static build, static-link assertion, and archive-content validation.
- Publish through `gh release create` using `GITHUB_TOKEN`, with job-scoped `contents: write` and read-only defaults elsewhere.
- Create a non-prerelease GitHub Release titled `Eitri <version>`, upload `eitri_<version>_linux_amd64.tar.gz`, and use the extracted changelog section verbatim for its body. Do not generate GitHub notes.
- Add `docs/releases.md` covering:
  - release preparation without requiring a pull request;
  - annotated `vX.Y.Z` tag creation;
  - GitHub `v*` tag-protection configuration and release-maintainer authorization;
  - mainline-reachability requirement, outputs, and verification;
  - the rule that published tags/assets are never changed and fixes use a new patch version.
- Link the runbook from README.

## Acceptance criteria
- Invalid tags, tags not reachable from `main`, missing changelog entries, failed verification, or archive failures produce no GitHub Release.
- A valid `v0.1.0` release creates a normal release titled `Eitri 0.1.0`, uploads only the versioned archive, and uses the `0.1.0` changelog section as its body.
- Workflow permissions are least-privilege: only the publishing job has `contents: write`.
- The runbook states that tag signing and commit signing are not required, while `v*` tag protection is.

## Out of scope
- Checksum/signature/SBOM/provenance publication and support for prereleases or non-Linux platforms.

## Dependencies
- [001-release-foundation.md](001-release-foundation.md) (changelog and policy baseline).
- [002-production-linux-amd64-build-and-ci.md](002-production-linux-amd64-build-and-ci.md) (release artifact and verification target).
- [003-bash-release-installer-and-install-docs.md](003-bash-release-installer-and-install-docs.md) (README/runbook links; publication itself may be implemented independently once its artifact dependency is satisfied).
