---
title: Omarchy panel
description: See the feed from the Omarchy bar, open what's live and mark things watched.
---

The `timmo.upnext` plugin adds a bar widget and a panel to Omarchy. It reads the feed from the daemon, so `upnext.service` needs to be running.

## Install

```bash
omarchy plugin add https://github.com/timmo001/omarchy-upnext.git
```

Accept the prompt to enable the plugin, then pick the widget it should sit beside in the bar. It goes in the centre section by default. If you had `timmo.twitch` installed, remove it with `omarchy plugin remove timmo.twitch`.

## The widget

The widget shows up to three counts, each in its source's colour:

1. Twitch channels from `channels.yml` that are live.
2. YouTube channels from `channels.yml` that are live.
3. New uploads from your YouTube channels, after a gap.

Counts of zero are left out. When nothing is live and there's nothing new, the widget hides. Hover over the bar to reveal it. On a vertical bar, or with **Counts to show** set lower in the widget's settings, the last counts drop off first.

- Click to open the panel.
- Middle-click to recheck every source.
- Right-click to restart `upnext.service`.

## The panel

The panel shows the feed in sections:

- **Live**: your channels from `channels.yml`, Twitch first, each in the order you listed them.
- **Followed**: other channels you follow or subscribe to that are live, by viewers. Collapsed when none are live.
- **Upcoming**: scheduled streams from your YouTube channels. Collapsed when there are none.
- **Watch later**: links you've saved with `upnext queue add` or the item menu, and the videos in your [watch-later playlist](/setup/youtube#use-a-playlist-for-watch-later) if you've set one. Its last row opens the playlist on YouTube. Collapsed when it's empty.
- **New uploads**: the last 7 days of uploads from your YouTube channels.
- **Other uploads**: uploads from your other YouTube subscriptions, once you've [signed in with Google](/setup/youtube#sign-in-with-google). Collapsed when there are none.

Every live row shows its viewer count and category.

- Type to filter, use Up and Down to move, and press Enter to open the selected item. A video in your watch-later playlist opens in the playlist, so the next one plays after it. Press Ctrl+Enter, or Ctrl-click, to open the video on its own.
- Press Enter on a section heading, or click it, to collapse or expand it.
- Press Shift+Enter, or click the checkmark on a video, to mark a YouTube video or a saved item watched. The checkmark on a section heading marks every video in that section watched, or every matching one while you're filtering.
- Right-click an item, or press the Menu key or Shift+F10, for more options: open it, open its channel, add the channel to your channels if you don't track it yet, save a YouTube video to watch later, or mark it watched. Saving moves the video into **Watch later**, out of its other section.
- Press Ctrl+R to recheck.
- If a source needs you to sign in, it shows at the top. Select it to run `upnext auth`.

The panel follows `upnext watch --json`, so it updates as soon as the feed changes. When the daemon restarts, the panel reconnects within a few seconds.

## Keyboard shortcuts

The plugin doesn't bind any keys itself. Add binds that call its IPC methods. In a Lua `bindings.lua`:

```lua
o.bind("CTRL + ALT + T", "Up Next: Twitch", "omarchy-shell timmo.upnext twitch")
o.bind("CTRL + ALT + Y", "Up Next: YouTube", "omarchy-shell timmo.upnext youtube")
```

Or in `hyprland.conf`:

```ini
bindd = CTRL ALT, T, Up Next: Twitch, exec, omarchy-shell timmo.upnext twitch
bindd = CTRL ALT, Y, Up Next: YouTube, exec, omarchy-shell timmo.upnext youtube
```

`twitch` opens the panel on live channels and `youtube` scrolls to new uploads, with the first one selected. Pressing the same bind again closes the panel.

## IPC

The plugin exposes the `timmo.upnext` IPC target:

| Method | Does |
| --- | --- |
| `open`, `show` | Opens the panel |
| `close`, `hide` | Closes the panel |
| `toggle` | Opens or closes the panel |
| `twitch` | Opens the panel on live channels, or closes it if it's already there |
| `youtube` | Opens the panel on new uploads, or closes it if it's already there |
| `recheck` | Checks every source now |
| `restart` | Restarts `upnext.service` |

```bash
omarchy-shell timmo.upnext youtube
```
