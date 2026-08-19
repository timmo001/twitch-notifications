# Twitch Notifications for Omarchy

An Omarchy bar widget and panel for Twitch live status and notification
controls. It shows configured channels, opens streams or recent broadcasts,
and controls the local Twitch Notifications daemon.

## Requirements

- Omarchy Quattro
- `twitch-notifications`, `twitch-notifications-recheck`, and
  `twitch-notifications-restart` available on `PATH`
- A configured Twitch Notifications daemon

The daemon owns Twitch credentials and channel configuration. Follow the
[Twitch Notifications setup guide][setup] before enabling this plugin.

## Install

Review the repository, then add the plugin:

```bash
omarchy plugin add \
  https://github.com/timmo001/omarchy-twitch-notifications.git
```

Accept the prompt to enable the plugin during installation.

For an unattended install from a repository you already trust:

```bash
omarchy plugin add \
  https://github.com/timmo001/omarchy-twitch-notifications.git \
  --enable --yes
```

## Use

Select the widget to open its panel. Type to filter the actions and channels,
use Up and Down to move through the list, press Enter to select an item, and
press Escape to clear the filter or close the panel. Press Ctrl+R to recheck
notifications.

Middle-click the widget to recheck notifications. Right-click it to restart
the daemon.

The plugin exposes the `timmo.twitch` shell IPC target with `refresh`,
`recheck`, `restart`, `open`, `close`, `show`, `hide`, and `toggle` methods:

```bash
omarchy-shell shell toggle timmo.twitch
```

## Settings

- `primaryOnly`: show the widget only on the selected output
- `primaryOutput`: optional output name used when `primaryOnly` is enabled;
  the first available output is used when this is empty or unavailable
- `revealOnHover`: reveal the normally hidden active state while hovering the
  bar

Twitch credentials, watched channels, auto-open preferences, polling,
notification sounds, and notification behaviour remain in the application's
`~/.config/twitch-notifications/config.yaml` and `channels.yml` files. They are
not plugin settings and are never stored in this repository.

## Update

Review and apply the next fast-forward update:

```bash
omarchy plugin update timmo.twitch
```

## Remove

```bash
omarchy plugin remove timmo.twitch
```

Removing the plugin does not remove Twitch Notifications or its configuration.

## Validate from source

```bash
omarchy plugin validate .
```

## Security

This plugin runs unsandboxed inside `omarchy-shell` when enabled. Review its
source before installing it.

The plugin runs these local commands:

```text
twitch-notifications --status-json
twitch-notifications --followed-live-json
twitch-notifications --recheck --open
twitch-notifications-recheck
twitch-notifications-restart
xdg-open <Twitch URL>
omarchy-launch-webapp <Twitch URL>
```

It polls local daemon status every five seconds. The daemon, not the plugin,
reads and updates Twitch OAuth tokens, accesses Twitch APIs, sends desktop
notifications, plays configured sounds, and applies channel auto-open rules.
The plugin opens public Twitch URLs selected by the user and loads live preview
images from Twitch's public CDN. It does not read credentials directly, write
Omarchy configuration, run privileged commands, or install software.

[setup]: https://github.com/timmo001/twitch-notifications#configuration
