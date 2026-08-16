# Twitch Live Notifier

A Go daemon application that monitors Twitch channels you follow and sends desktop notifications when they go live, with optional auto-opening of streams in the background.

## Features

- **Real-time Notifications**: Uses Twitch EventSub WebSocket for instant notifications when streams go live
- **Desktop Notifications**: Uses Omarchy's notification helper when available, with direct DBus fallback
- **Auto-Open Streams**: Configurable list of channels that automatically open in your browser
- **Followed Channels**: Automatically monitors all channels you follow on Twitch
- **Background Daemon**: Runs as a background service with graceful shutdown

## Prerequisites

- [mise](https://mise.jdx.dev/)
- Omarchy for styled notifications (optional; direct DBus notifications are used as a fallback)
- Twitch Developer Application credentials (Client ID and Client Secret)
- Twitch OAuth access token with `user:read:follows` scope

## Installation

On Arch Linux with the signed `timmo` repository configured:

```bash
sudo pacman -S twitch-notifications-git
```

For development, clone the repository:

```bash
git clone https://github.com/timmo001/twitch-notifications.git
cd twitch-notifications
```

Install the pinned tools and project dependencies:

```bash
mise install
mise run deps
```

Build the application and TUI:

```bash
mise run build:all
```

For development, run the notification daemon in the background through
pitchfork:

```bash
mise run serve:start
mise run serve:status
mise run serve:logs
mise run serve:stop
```

The development wrapper temporarily stops the packaged daemon and restores it
when the development daemon stops.

## Configuration

1. Create a Twitch Developer Application:
   - Go to <https://dev.twitch.tv/console/apps>
   - Create a new application
   - Note your Client ID and Client Secret

2. Configure your Twitch Application:
   - Go to your application settings in the Twitch Developer Console
   - Add `http://localhost:8080/oauth/callback` as a redirect URI
   - This is required for the OAuth flow to work

3. Configure the application:
   - On first run, the application will automatically create `config/config.yaml` from `config/config.example.yaml`
   - Watched channels are stored separately in `channels.yml` next to your config file.
   - Edit `config/config.yaml` to add your credentials:

   ```yaml
    # Twitch API credentials
    # You can use environment variables like ${TWITCH_CLIENT_ID}
    twitch:
        client_id: "${TWITCH_CLIENT_ID}"
        client_secret: "${TWITCH_CLIENT_SECRET}"
    ```

   - Edit `channels.yml` to manage watched channels manually, or use `twitch-notifications channel add`.
   - Note: `config/config.yaml` is gitignored and will not be committed to the repository

4. Set environment variables (recommended):

   ```bash
   export TWITCH_CLIENT_ID="your_client_id"
   export TWITCH_CLIENT_SECRET="your_client_secret"
   ```

5. Obtain OAuth tokens separately and set them in `config/config.yaml`:
   - Required scope: `user:read:follows`
   - Redirect URI: `http://localhost:8080/oauth/callback`
   - Set either `access_token`, or both `access_token` and `refresh_token`

## Usage

### Manage Channels

Add or update a watched channel from the CLI:

```bash
twitch-notifications channel add
twitch-notifications channel add pirateSoftware
twitch-notifications channel add pirateSoftware true
twitch-notifications channel add false
twitch-notifications channel remove
twitch-notifications channel remove pirateSoftware
```

- The channel name and `true`/`false` `open` value are both optional.
- Any missing value is collected with an interactive prompt.
- If the channel already exists, the command updates that entry instead of failing.
- `channel remove` accepts a channel name or opens an interactive picker when omitted.
- Use `-config /path/to/config.yaml` to write to a non-default config.
- If a notifier instance is already running, channel changes trigger its normal restart flow so the new watch list is applied immediately.

### First-Time Setup

1. Configure your Client ID and Client Secret in `config/config.yaml`
2. Add your watched channels with `twitch-notifications channel add` or by editing `channels.yml`
3. Add your Twitch OAuth token values to `config/config.yaml`
4. Start the notifier

### Running as a Daemon

Run the application in the foreground (for testing):

```bash
./twitch-notifications -config config/config.yaml
```

For production, you can run it as a background process:

```bash
nohup ./twitch-notifications -config config/config.yaml > twitch-notifications.log 2>&1 &
```

### Read Status

Print structured daemon and channel state for desktop panels and other clients:

```bash
twitch-notifications --status-json
```

The command returns configured channels with live channels first. `state` is
`live`, `active`, or `inactive`, and `autoOpen` reflects each channel's
`channels.yml` setting:

```json
{
  "active": true,
  "state": "live",
  "liveCount": 1,
  "channels": [
    {
      "login": "pirateSoftware",
      "title": "Stream title",
      "live": true,
      "autoOpen": false
    }
  ]
}
```

### Systemd Service (Optional)

Create a systemd service file at `/etc/systemd/system/twitch-notifications.service`:

```ini
[Unit]
Description=Twitch Live Notifier
After=network.target

[Service]
Type=simple
User=your-username
WorkingDirectory=/path/to/twitch-notifications
ExecStart=/path/to/twitch-notifications/twitch-notifications -config /path/to/twitch-notifications/config/config.yaml
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Then enable and start the service:

```bash
sudo systemctl enable twitch-notifications
sudo systemctl start twitch-notifications
```

## How It Works

1. **Authentication**: The application authenticates with Twitch using your OAuth token
2. **Channel Discovery**: Fetches all channels you follow using the Helix API
3. **EventSub Connection**: Establishes a WebSocket connection to Twitch's EventSub service
4. **Subscription**: Subscribes to `stream.online` events for each followed channel
5. **Notifications**: When a channel goes live:
   - Sends a desktop notification with channel name, stream title, and game
   - Optionally opens the stream in your browser if configured
6. **Reconnection**: Automatically handles reconnections and resubscriptions

## Configuration Options

### Watched Channels

Add channel names (case-insensitive) to the `watched_channels` list in `channels.yml`. These channels will:

- Be subscribed to first (prioritized over other channels)
- Help reduce rate limiting by focusing on important channels first

Each channel is specified as an object with a `name` field and an `open` property:

```yaml
watched_channels:
    - name: alveussanctuary
      open: true   # Automatically opens browser when this channel goes live
    - name: maya
      open: false  # Sends notification but doesn't open browser (default)
    - name: theprimeagen
      open: false  # Explicitly disable auto-opening
```

The `open` property controls auto-opening behavior:

- `open: true` - Automatically opens browser when this channel goes live
- `open: false` - Sends notification but doesn't open browser (default)

Gracefully restart the running daemon and open configured live channels during its initial check:

```bash
twitch-notifications --restart --open
```

### Custom Config Path

Specify a custom configuration file path:

```bash
./twitch-notifications -config /path/to/custom/config.yaml
```

## Troubleshooting

### No Notifications Appearing

- If Omarchy notifications fail, check that `omarchy notification send` works; the app falls back to direct DBus notifications
- Check that your notification daemon is running
- Verify your access token is valid and has the correct scopes

### Connection Issues

- Check your internet connection
- Verify your Twitch API credentials are correct
- Ensure your access token hasn't expired (tokens typically expire after 60 days)

### No Channels Found

- Verify you follow at least one channel on Twitch
- Check that your access token has the `user:read:follows` scope
- Review the logs for API errors

## Rate Limits

Twitch API has rate limits:

- EventSub WebSocket: Up to 3 connections with 300 subscriptions each
- Helix API: Varies by endpoint, typically 800 requests per minute

The application includes delays between subscription requests to avoid rate limiting.

## Subscription Cost Limits & Hybrid Monitoring

**Important**: Twitch EventSub uses a cost-based subscription system. By default, applications using a user access token have a `max_total_cost` limit of **10 subscriptions**.

- Each `stream.online` subscription costs **1 point**
- Default limit: **10 subscriptions** (10 channels)
- This limit applies per user/client ID combination

### Hybrid Monitoring (EventSub + Polling)

To work around the 10 channel limit, the application uses a **hybrid approach**:

1. **First 10 channels** (by order in config) → **EventSub WebSocket** (real-time, instant notifications)
2. **Channels 11+** → **Polling via Helix API** (configurable interval, default 60 seconds)

This means you can monitor **unlimited channels** - the first 10 get instant notifications, while additional channels are checked periodically.

#### How It Works

```
┌─────────────────────────────────────────────────────┐
│               watched_channels (from config)        │
│                        │                            │
│  Channels 1-10  ──────►  EventSub (real-time)       │
│                          └── stream.online events   │
│                                                     │
│  Channels 11+   ──────►  Poller (60s interval)      │
│                          └── GET /helix/streams     │
│                                                     │
│  Both ──► Desktop notification + optional browser   │
└─────────────────────────────────────────────────────┘
```

#### Configuring Poll Interval

You can customize the polling interval in your `config.yaml`:

```yaml
# Polling interval in seconds for overflow channels (channels 11+)
# Default is 60 seconds. Lower values = more responsive but more API calls
poll_interval: 60
```

#### Channel Priority

**Order matters!** Channels are processed in the order they appear in `watched_channels`:

- Put your most important channels first (positions 1-10) for instant notifications
- Less critical channels can be placed after position 10 for polling-based monitoring

Example:

```yaml
watched_channels:
    # Priority channels (EventSub - instant notifications)
    - name: favorite_streamer_1
      open: true
    - name: favorite_streamer_2
      open: true
    # ... channels 3-10 ...
    
    # Overflow channels (Polling - checked every 60 seconds)
    - name: occasional_watch_1
      open: false
    - name: occasional_watch_2
      open: false
```

### Why am I limited to 10 channels?

For personal applications with only one user (yourself), you're limited to 10 EventSub subscriptions by default. This is not a hard limit - it can be increased through:

1. **User Growth**: As more users authorize your application, the limit automatically increases
2. **Broadcaster Authentication**: If broadcasters authenticate your app, their subscriptions cost 0 points (unlimited for those channels)
3. **Limit Increase Request**: Contact Twitch Developer Support to request a higher limit for personal use

### Checking Your Limit

The application will automatically detect and handle the 10 channel limit. Check your logs to see:

- Which channels are using EventSub (real-time)
- Which channels are being polled (overflow)

Example log output:

```
Channel split: 10 channels via EventSub (real-time), 5 channels via polling
Poller started for 5 overflow channels (polling every 1m0s)
```

## Token Refresh

The application automatically refreshes access tokens if a refresh token is available. For long-running daemons, ensure you have a refresh token configured.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE).

## Contributing

[Add contribution guidelines if applicable]
