# TUI AGENTS

Instructions for AI coding agents working in the `tui/` subdirectory.

## Overview

`twitch-notifications-tui` is the interactive TUI menu for `twitch-notifications`. Built with Bun + TypeScript on `@opentui/core` and `effect`, compiled to a standalone binary via `bun build --compile`. The Go binary discovers and launches this TUI automatically in interactive terminal sessions.

## Tech stack

- **Runtime**: Bun (not Node)
- **Language**: TypeScript (strict, ESNext, bundler module resolution)
- **TUI framework**: `@opentui/core`
- **Effect**: `effect` v4 beta — program entry point only (`Effect.gen`, `Effect.runPromise`)
- **Fuzzy search**: `fuse.js` — weighted fuzzy matching in `MenuList`

## Commands

```sh
bun run dev        # Run with --watch for development
bun run build      # Compile to standalone binary at dist/twitch-notifications-tui
bunx tsc --noEmit  # Typecheck
```

Or from the project root: `mise run build:tui`, `mise run run:tui`.

## Menu system

- `src/menu.ts` — menu item definitions using helpers (`item()`, `cmd()`, `silent()`, `notify()`, `submenu()`, `replace()`). Registered in `mainMenuItems`, `submenus`, and `menuItemsById`.
- `src/types.ts` — `MenuItem`, `MenuAction` (discriminated union with `command`, `silent`, `notify`, `view`, `submenu`, `replace`, `quit`), `MenuVariant`, `ViewId`.
- Adding a menu item: define in `menu.ts`, add to the appropriate array, register submenus in `submenus` and `submenuTitles` maps, call `registerItems()`.

## Key action types

- `command` — suspend TUI, run with inherited stdio, optionally wait for keypress, resume TUI
- `replace` — destroy TUI, exec command, exit process (used for "Run Server")
- `notify` — run in background, show toast progress/result (used for "Recheck")

## Conventions

- PascalCase for classes/components, camelCase for utilities
- All local imports use `.js` extensions
- Logging to stderr with `[twitch-notifications-tui:*]` prefix
- Menu item IDs are dot-separated: `"serve"`, `"channels.add"`
