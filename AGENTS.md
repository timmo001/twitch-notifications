# TWITCH-NOTIFICATIONS AGENTS

Instructions for coding agents working in this repository.

## Scope

- This repo is the source code for `twitch-notifications` and its public
  rolling package, `twitch-notifications-git`.
- Packaging is part of the normal workflow for this repo.
- Do not publish package artifacts from here directly to machines; publish
  through the signed `timmo` pacman repository.

## Build And Package

- Build: `mise run build:all`
- Test: `mise run test`
- Lint: `mise run lint:all`
- Package: `mise run package:arch`
- Never install this application with `go install`. It can leave a stale binary in the Go bin directory that takes precedence over the packaged version.
- Install or update the application only through the public package workflow
  below.

## Background Development Server

- Run `mise run serve:start` to build and start the development daemon through
  pitchfork. Do not launch the daemon in the foreground from an agent command.
- The wrapper temporarily stops the packaged daemon and restores it when the
  development daemon stops.
- Use `mise run serve:status`, `mise run serve:logs`, `mise run serve:restart`,
  and `mise run serve:stop` to manage it.
- Run `mise run dev:start` to test the development daemon and local panel
  together. Use the matching `dev:status`, `dev:logs`, `dev:restart`, and
  `dev:stop` tasks so the installed panel and packaged daemon are restored.

## Publish Workflow

- Push an allowlisted source change to `main`, or dispatch
  `.github/workflows/publish-arch-git.yml` manually.
- The workflow builds the exact source SHA through
  `timmo001/workflows/.github/workflows/build-arch-package.yml`, then dispatches
  the validated package to `timmo001/arch-repo`.
- Install or update only after publication succeeds, through pacman from the
  signed `timmo` repository.
- `mise run package:arch` remains available for local package validation only.

## Notes

- `main` publishes only the rolling `twitch-notifications-git` package. Reserve
  the base `twitch-notifications` name for a future stable release channel.
- Publication requires the repository secret `ARCH_REPO_DISPATCH_TOKEN`, scoped
  only to dispatching `timmo001/arch-repo`.
- The public package allowlist lives in
  `timmo001/arch-repo/config/packages.json`.

## Safety

- Do not commit or push unless explicitly requested.
