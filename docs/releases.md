# Releases

A published release is immutable: never move a published release tag or replace its
assets. Publish fixes as a new patch version instead.

## Prepare a release

Prepare the release on `main`; a pull request is not required for the release
preparation itself. Choose the next stable semantic version and add its dated
`## [X.Y.Z] - YYYY-MM-DD` section to `CHANGELOG.md`. The section becomes the GitHub
Release body verbatim. Run the full quality gate locally, including `go vet`,
race-enabled tests, coverage, Bash script tests, and the production package
validation.

The release workflow accepts only a stable `vX.Y.Z` tag whose peeled commit is
reachable from `origin/main`. Ensure the matching changelog section exists before
tagging.

## Tag and publish

Create and push an annotated tag from the verified commit on `main`:

```sh
git tag -a vX.Y.Z -m 'Release vX.Y.Z'
git push origin vX.Y.Z
```

Tag signing and commit signing are not required.

Pushing the tag starts verification. GitHub Actions tag globs cannot express an
exact stable SemVer pattern, so the workflow uses the `v*` candidate trigger and
its runtime stable `vX.Y.Z` gate prevents invalid candidates from publishing.
It installs Eitri's declared runtime dependencies, reruns the complete quality
gate, builds and validates the exact
versioned release binary archive, and extracts the changelog section. Only after
that succeeds does the publishing job create the normal GitHub Release titled
`Eitri X.Y.Z`, with `eitri_X.Y.Z_linux_amd64.tar.gz` as its only asset and the
extracted changelog section as its body. The workflow does not publish prereleases
or generated GitHub notes.

Verify the resulting release page has the expected title, verbatim notes, and only
the versioned Linux `amd64` archive. If any verification fails, no GitHub Release
is created.
