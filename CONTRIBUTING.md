# Development and Git workflow

Create short-lived branches from current `main` (`codex/`, `feat/`, or `fix/`).
Submit a PR; do not push application changes directly to `main`. Require the
`checks` CI status, resolve review conversations, then squash merge and delete
the feature branch. Use descriptive conventional PR titles (`feat:`, `fix:`,
`docs:`, `chore:`). A permanent `develop` branch is unnecessary for this project.

Run `make check`, keep `gofmt` clean, and add regression tests for behavior changes.
CI additionally runs race tests, JS syntax checks, Compose validation, and an image
startup check. Tests must not depend on live animepahe or FlareSolverr.

Keep GitHub Actions pinned to full commit SHAs. PR workflows have read-only
permissions; only the tag release job may write packages and releases. Review
dependency updates, including their minimum Go version and license notices.

## Repository rules

Configure rulesets for `main`: require PRs, the `checks` status, resolved review
threads, linear history, and block deletion/force pushes. A solo maintainer need
not require a second approver. Squash merges should be the normal merge method.
Protect `v*` tags against update and deletion; never move a published tag.
Rules in this document describe policy; enforcement lives in GitHub settings.

## Versioning and releases

The first release is `v0.1.0`. Fixes increment patch, features increment minor,
and incompatible changes must be called out explicitly while the project is 0.x.
Use `vX.Y.Z-rc.N` (N starts at 1) for release candidates. At 1.0, follow semantic
versioning for the stable interface. Tags are the version source of truth; local
builds report `dev`. `pahe version` shows the version and source commit; the UI
footer shows the version. Keep user-visible changes in `CHANGELOG.md`.

1. Merge and verify CI on `main`. Finalize the changelog in a PR before tagging.
2. Fetch current main and create an annotated tag on that commit:

   ```sh
   git switch main
   git pull --ff-only
   git tag -a v0.1.0 -m "Release v0.1.0"
   git push origin v0.1.0
   ```

3. The Release workflow validates the tag and main ancestry, reruns CI, and
   publishes AMD64/ARM64 images to GHCR. Only tag pushes can publish; neither
   PRs nor main pushes publish images.
4. Stable releases produce `:X.Y.Z`; the newest stable version also updates
   `:latest`. Candidates produce only `:X.Y.Z-rc.N`. Releasing an older patch
   must not roll back `latest`. Do not rerun a successful image publication to
   rebuild an existing version: base images may have changed; publish a new tag.
5. A GitHub Release includes generated notes, `casaos.yml` pinned to that version,
   and its checksum. Image builds include provenance and an SBOM.
6. On the first publication, open the GHCR package settings and make the package
   public, then confirm an unauthenticated pull works. A public repository does
   not automatically guarantee a publicly pullable package.

Publication does not deploy to anyone's server. Install exact versions and update
deliberately. Back up config before updates; rollback means selecting the previous
image tag and restoring the matching config backup if its format changed.

Go 1.22 / Alpine 3.20 remain pinned for compatibility in this change. Upgrade and
retest these base versions separately before the first public release.
