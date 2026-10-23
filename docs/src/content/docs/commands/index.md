---
title: Commands
description: Every upnext command, argument and flag, generated from the CLI's help.
---

<!-- Generated from src/index.ts by `mise run docs:gen`. Do not edit by hand. -->

Each command has its own page with its help, as `upnext <command> --help` prints it.

| Command | Alias |
| --- | --- |
| [`serve`](/commands/serve) | None |
| [`feed`](/commands/feed) | None |
| [`watch`](/commands/watch) | None |
| [`recheck`](/commands/recheck) | None |
| [`auth`](/commands/auth) | None |
| [`channel`](/commands/channel) | None |
| [`queue`](/commands/queue) | None |
| [`watched`](/commands/watched) | None |

## Global flags

```text
DESCRIPTION
  Twitch live channels, YouTube uploads and a watch-later queue in one feed

USAGE
  upnext <subcommand> [flags]

FLAGS
  --socket string    Path to the daemon socket (default: $UPNEXT_SOCK, then $XDG_RUNTIME_DIR/upnext/upnext.sock)

GLOBAL FLAGS
  --help, -h                                                          Show help information
  --version, -v                                                       Show version information
  --wizard                                                            Start wizard mode for a command
  --completions <bash|zsh|fish|powershell|sh|pwsh>                    Print shell completion script (choices: bash, zsh, fish, powershell, sh, pwsh)
  --log-level <all|trace|debug|info|warn|warning|error|fatal|none>    Sets the minimum log level (choices: all, trace, debug, info, warn, warning, error, fatal, none)

SUBCOMMANDS
  serve      Run the daemon and serve the feed on its socket
  feed       Print what's live, new and saved
  watch      Print the feed, then again after each change
  recheck    Check sources now instead of waiting
  auth       Sign in to a source that needs it
  channel    Add or remove followed channels
  queue      Manage the watch-later queue
  watched    Hide YouTube videos or remove saved items
```
