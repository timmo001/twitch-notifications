---
title: upnext queue
description: Arguments and flags for every upnext queue command.
sidebar:
  label: queue
---

<!-- Generated from src/index.ts by `mise run docs:gen`. Do not edit by hand. -->

Every `upnext queue` command and its help, as `--help` prints it. Each also accepts the [global flags](/commands#global-flags).

## `upnext queue`

```text
DESCRIPTION
  Manage the watch-later queue

USAGE
  upnext queue <subcommand> [flags]

INHERITED FLAGS
  --socket string    Path to the daemon socket (default: $UPNEXT_SOCK, then $XDG_RUNTIME_DIR/upnext/upnext.sock)
```

## `upnext queue add`

```text
DESCRIPTION
  Save a URL to watch later

USAGE
  upnext queue add [flags] <url>

ARGUMENTS
  url string    What to watch later

FLAGS
  --title string    Title to show instead of the one looked up
  --json            Print the saved item as JSON

INHERITED FLAGS
  --socket string    Path to the daemon socket (default: $UPNEXT_SOCK, then $XDG_RUNTIME_DIR/upnext/upnext.sock)
```
