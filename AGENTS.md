## Release Process

## Commit Quality Gate

Before every feature commit, run `make check` and ensure it passes.

When doing a production release, follow this sequence:

1. Ensure you are on the `main` branch first.
2. Fetch and fast-forward `main` to `origin/main` (`git fetch origin main && git pull --ff-only origin main`).
3. Determine the version bump and update the h2 version:
   - If unsure what bump to use, ask the user.
   - Use semver rules:
     - `patch` for non-breaking changes.
     - for pre-1.0 versions: `minor` for breaking changes.
     - for 1.0+ versions: `major` for breaking changes.
4. Update `docs/CHANGELOG.md` with changes since the last version tag and its tagged commit.
5. Run all test suites and ensure they pass.
6. Run `make check` and ensure it passes.
7. Run `make build-release` and ensure it passes.
8. Tag the current `HEAD` with the release version.
