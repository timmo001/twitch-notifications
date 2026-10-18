---
title: Running Up Next
description: Run Up Next as a systemd user service, read its logs and keep it running.
---

`upnext serve` keeps the feed. Every other command needs it running, so it normally runs as a systemd user service.

## User service

The packages install `upnext.service` as a user unit. It starts with your desktop session (`graphical-session.target`), after the compositor has passed variables such as `WAYLAND_DISPLAY` and `OMARCHY_HOST` to systemd, so it can open streams in a browser. It doesn't run in a session without a desktop, such as SSH. The Arch package enables it for every user's future desktop sessions. To start it now, or after installing a `.deb` or `.rpm`:

```bash
systemctl --user daemon-reload
systemctl --user enable --now upnext.service
```

Check it's running:

```bash
systemctl --user status upnext.service
journalctl --user -u upnext.service -f
```

A healthy start logs `Listening` with the socket path. The service restarts 5 seconds after it fails.

## Upgrades

The Arch package restarts a running service after an upgrade, so it uses the new binary straight away. For other installs, restart it yourself:

```bash
systemctl --user restart upnext.service
```

Watchers, such as `upnext watch`, exit when the daemon restarts. Run them under something that restarts them, such as a panel's restart interval or a systemd unit.

## Run in the foreground

To troubleshoot, stop the service and run the daemon in a terminal with debug logs:

```bash
systemctl --user stop upnext.service
upnext serve --log-level debug
```

Use `--socket` to run a second daemon beside the service, for example with a different config:

```bash
XDG_CONFIG_HOME=/tmp/upnext-test upnext serve --socket /tmp/upnext-test.sock
upnext feed --socket /tmp/upnext-test.sock
```

## Install the unit by hand

When you build from source, copy the unit and point `ExecStart` at your binary:

```bash
mkdir -p ~/.config/systemd/user
cp .scripts/linux/upnext.service ~/.config/systemd/user/
sed -i "s#/usr/bin/upnext#$(command -v upnext)#" ~/.config/systemd/user/upnext.service
systemctl --user daemon-reload
systemctl --user enable --now upnext.service
```
