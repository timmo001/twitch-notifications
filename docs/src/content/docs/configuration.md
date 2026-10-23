---
title: Configuration
description: Every setting in config.yml and channels.yml, and where Up Next keeps its files.
---

Up Next reads two files from `$XDG_CONFIG_HOME/upnext`, which is usually `~/.config/upnext`. Only `upnext serve` reads them; every other command talks to the daemon socket.

If you used twitch-notifications, Up Next imports its config on first run. See [Migrating from twitch-notifications](/from-twitch-notifications).

## config.yml

```yaml
notify_on_startup: true
sound_file: /usr/share/sounds/freedesktop/stereo/message-new-instant.oga
quiet_hours:
  start: "00:00"
  end: "08:00"
twitch:
  client_id: ${TWITCH_CLIENT_ID}
  client_secret: ${TWITCH_CLIENT_SECRET}
  poll_interval: 60
  quiet_poll_interval: 300
youtube:
  api_key: ${YOUTUBE_API_KEY}
  client_id: ${GOOGLE_CLIENT_ID}
  client_secret: ${GOOGLE_CLIENT_SECRET}
  poll_interval: 600
  quiet_poll_interval: 1800
  watch_later_playlist: ""
```

Every setting is optional.

- `notify_on_startup`: notify about channels that are already live when the daemon starts. Defaults to `true`.
- `sound_file`: a sound to play with each notification.
- `quiet_hours.start` and `quiet_hours.end`: local times, as `HH:MM`, between which each source checks every `quiet_poll_interval` instead of `poll_interval`. The window can run past midnight, such as `23:00` to `07:00`. Off unless both are set.
- `twitch.client_id` and `twitch.client_secret`: your Twitch application's credentials. See [Set up Twitch](/setup/twitch).
- `twitch.poll_interval`: seconds between checks for channels that live notifications don't cover. Defaults to 60.
- `twitch.quiet_poll_interval`: seconds between those checks during quiet hours. Defaults to 300. Live notifications still arrive within seconds.
- `youtube.api_key`: a YouTube Data API key. Without one or a Google sign-in, Up Next still shows new uploads, but can't tell which videos are live or upcoming.
- `youtube.client_id` and `youtube.client_secret`: a Google OAuth desktop client, for signing in to read your subscriptions. See [Set up YouTube](/setup/youtube).
- `youtube.poll_interval`: seconds between YouTube checks. Defaults to 600.
- `youtube.quiet_poll_interval`: seconds between YouTube checks during quiet hours. Defaults to 1800.
- `youtube.watch_later_playlist`: a playlist ID or URL to keep saved YouTube videos in, instead of the local queue. Needs a Google sign-in that can change playlists. See [Use a playlist for watch later](/setup/youtube#use-a-playlist-for-watch-later).

Values can use `$VAR` or `${VAR}` to read environment variables, so you can keep secrets out of the file. The daemon only sees variables set in its own environment, so for the systemd service set them with `systemctl --user edit upnext.service`.

The daemon reads `config.yml` when it starts. Restart it after changing the file:

```bash
systemctl --user restart upnext.service
```

## Twitch

See [Set up Twitch](/setup/twitch) to register an application, sign in and pick channels.

Live notifications are only for channels in `channels.yml`. Twitch's live events cover up to 10 of them, so those show up within seconds. The rest are checked every `poll_interval`. Another app using the same Twitch account's live events can use up that allowance, and Up Next then relies on polling until it's free.

## YouTube

See [Set up YouTube](/setup/youtube) to add channels, get an API key and sign in with Google.

Notifications are only for channels in `channels.yml`, and each channel's `notify` setting picks live streams, uploads or both. Once you've signed in, your other subscriptions' uploads and live streams show too, without notifying you.

Up Next reads each channel's uploads from its RSS feed, which costs no API quota. YouTube's feeds often stop working for a few hours, usually overnight. While they're down, channels in `channels.yml` are read through the API instead, if you've set an API key or signed in, and other subscriptions keep what they last showed until the feeds come back. Setting `quiet_hours` to cover those hours makes the fallback cheaper on quota. See [Quota](/setup/youtube#quota).

## channels.yml

```yaml
twitch:
  - name: some_streamer
    open: true
  - name: another_streamer
youtube:
  - id: UCxxxxxxxxxxxxxxxxxxxxxx
    notify:
      live: true
      uploads: false
```

- `twitch`: Twitch logins, in the order they show in the feed. Up Next also shows every channel you follow that's live, after these.
- `youtube`: YouTube channel IDs, the part after `/channel/` in a channel's URL. Their live streams come after your Twitch channels'.
- `open`: open the channel in your browser as soon as it goes live. Defaults to `false`.
- `notify.live`: YouTube only. Notify when the channel goes live. Defaults to `true`.
- `notify.uploads`: YouTube only. Notify about the channel's new uploads. Defaults to `false`.

`upnext channel add` and `upnext channel remove` change this file for you. Run `upnext channel add` without a name to pick from the channels you follow or subscribe to. When you add YouTube channels in a terminal, it asks which notifications you want for each one, unless you pass `--notify-live` or `--notify-uploads` (or their `--no-` forms). The Omarchy panel uses the defaults.

If you keep `channels.yml` in a dotfiles repository and link it into place with stow, Up Next writes changes through the link, so your repository stays the source.

## State

The daemon keeps your Twitch and Google tokens in `$XDG_STATE_HOME/upnext/state.json`, which is usually `~/.local/state/upnext/state.json`. They belong to that computer, so don't sync this file.

Videos you've marked watched and your watch-later queue go in `$XDG_DATA_HOME/upnext/library.json`, which is usually `~/.local/share/upnext/library.json`. You don't need to edit either file.

The config, state and data directories are only readable by you (`0700`) and the files by you (`0600`).

## Syncing between computers

To share what you've watched and saved between computers, sync the `~/.local/share/upnext` directory with a tool such as Syncthing. The daemon reloads `library.json` when it changes, so a video marked watched on one computer disappears from the other. Before each change, it reads the file again, so it doesn't overwrite what came in from another computer.

If both computers change the library before it syncs, such as when one starts up before it has caught up with the other, Syncthing keeps one copy and saves the other as `library.sync-conflict-*.json`. The daemon adds back anything marked watched in that copy, then deletes it. Other sync tools may keep only one of the changes.

## Socket path

Commands find the daemon socket in this order:

1. `--socket <path>`
2. `$UPNEXT_SOCK`
3. `$XDG_RUNTIME_DIR/upnext/upnext.sock`
4. `$TMPDIR/upnext-$USER/upnext.sock`, falling back to `/tmp` and `default`

`upnext serve` uses the same order to decide where to listen, so the defaults always match.
