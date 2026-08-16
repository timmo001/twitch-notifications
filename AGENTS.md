# TWITCH-NOTIFICATIONS AGENTS

Instructions for coding agents working in this repository.

## Scope

- This repo is the source code for `twitch-notifications` and its private
  rolling package, `twitch-notifications-git`.
- Packaging is part of the normal workflow for this repo.
- Do not publish package artifacts from here directly to machines; publish through the private pacman repo.

## Build And Package

- Build: `mise run build:all`
- Test: `mise run test`
- Lint: `mise run lint:all`
- Package: `mise run package:arch`
- Never install this application with `go install`. It can leave a stale binary in the Go bin directory that takes precedence over the packaged version.
- Install or update the application only through the private pacman package workflow below.

## Background Development Server

- Run `mise run serve:start` to build and start the development daemon through
  pitchfork. Do not launch the daemon in the foreground from an agent command.
- The wrapper temporarily stops the packaged daemon and restores it when the
  development daemon stops.
- Use `mise run serve:status`, `mise run serve:logs`, `mise run serve:restart`,
  and `mise run serve:stop` to manage it.

## Publish Workflow

Preferred path:

- Run `mise run package:arch`
- Run `dot private-pkg-publish --skip-build twitch-notifications-git`
- Add `--install` when the package should also be installed locally.
- This commits and pushes `~/repos/private-arch-repo` by default; use `--no-git` only when the user wants a local-only publish.

Manual path:

1. Run `mise run package:arch`
2. Copy the `twitch-notifications-git` runtime package from `dist/` into
   `~/repos/private-arch-repo`
3. Run `repo-add ~/repos/private-arch-repo/timmo-private.db.tar.gz ~/repos/private-arch-repo/*.pkg.tar.zst`
4. Commit and push `~/repos/private-arch-repo`

## Notes

- Omit `*-debug-*.pkg.tar.zst` from the private package repo unless the user explicitly wants debug packages published.
- `main` publishes only the rolling `twitch-notifications-git` package. Reserve
  the base `twitch-notifications` name for a future stable release channel.
- `dot private-pkg-publish` removes the older runtime artifact for this package, regenerates the repo db, syncs the mirror, refreshes pacman metadata, and commits/pushes the private package repo by default.
- If this repo becomes public and gains maintained workflows for AUR or other public package publication, stop publishing it to the private pacman repo and switch the documented workflow to the public package path instead.

## Safety

- Do not commit or push unless explicitly requested.
