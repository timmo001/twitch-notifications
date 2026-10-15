# @timmo001/effect-upnext

Effect client and protocol for [Up Next](https://github.com/timmo001/upnext), which gathers Twitch live channels, YouTube uploads and a watch-later queue into one feed and serves it to local apps over a Unix socket.

Use it to talk to a running `upnext serve` from your own Effect app instead of spawning the `upnext` CLI. It works under Bun and Node.

## Install

```bash
bun add @timmo001/effect-upnext @timmo001/effect-upnext-shared effect
npm install @timmo001/effect-upnext @timmo001/effect-upnext-shared effect
npx jsr add @timmo001/effect-upnext @timmo001/effect-upnext-shared
```

`effect` is a peer dependency, so install the same Effect v4 version your app uses. Media types, such as `MediaItem` and `Source`, come from [`@timmo001/effect-upnext-shared`](https://github.com/timmo001/upnext/tree/main/packages/shared); add it when you use them directly.

## Usage

`UpnextClient.layer(path)` connects to the daemon socket. `resolveSocketPath` finds the same socket the CLI uses, so most apps can pass `Option.none()`:

1. The path you pass in, if any
2. `$UPNEXT_SOCK`
3. `$XDG_RUNTIME_DIR/upnext/upnext.sock`
4. `$TMPDIR/upnext-$USER/upnext.sock` (with `/tmp` and `default` as fallbacks)

`resolveSocketPath` needs the platform `Path` service, so provide `NodeServices.layer` from `@effect/platform-node` or `BunServices.layer` from `@effect/platform-bun`.

```ts
import { NodeRuntime, NodeServices } from "@effect/platform-node";
import { resolveSocketPath, UpnextClient } from "@timmo001/effect-upnext";
import { Console, Effect, Option } from "effect";

const program = Effect.gen(function* () {
  const client = yield* UpnextClient;
  const feed = yield* client.GetFeed();

  for (const { item } of feed.items) {
    yield* Console.log(`${item.kind}: ${item.title}`);
  }
});

const main = Effect.gen(function* () {
  const socketPath = yield* resolveSocketPath(Option.none());

  yield* program.pipe(Effect.provide(UpnextClient.layer(socketPath)));
});

main.pipe(Effect.provide(NodeServices.layer), NodeRuntime.runMain);
```

### Read the feed

`GetFeed` returns every source's last status and every item, live first, then upcoming, new uploads and saved items, newest first within each apart from saved items, which follow the watch-later playlist's order and then the order they were saved in. Each item comes with `tracked`, which is false for followed Twitch channels missing from `channels.yml`, and `autoOpen`. When a watch-later playlist is set, `watchLaterPlaylist` has its URL, and its title once the daemon has read it.

### Watch the feed

`WatchFeed` returns a `Stream` that emits the whole feed, then the whole feed again after each change.

```ts
const liveCount = Effect.gen(function* () {
  const client = yield* UpnextClient;

  yield* client.WatchFeed().pipe(
    Stream.runForEach((feed) =>
      Console.log(feed.items.filter(({ item }) => item.kind === "live").length),
    ),
  );
});
```

### Change things

- `Recheck({ source, open })` checks every source, or one, now. With `open`, live channels set to auto-open open even if they were already live.
- `SignIn({ source })` starts signing in to Twitch, or to Google for YouTube. The daemon opens the sign-in page itself and returns its URL in case that fails. Watch the feed for the source's status to become `ok`.
- `AddChannel` and `RemoveChannel` change `channels.yml`. For YouTube, `AddChannel` takes an optional `notify: { live, uploads }`; settings left out keep the channel's current value, or the default for a new channel.
- `ListCandidates({ source })` returns the followed Twitch channels or YouTube subscriptions that aren't in `channels.yml` yet, each with the `name` that `AddChannel` takes. It needs that source signed in.
- `QueueAdd({ url, title })` saves a URL to watch later and returns the saved item.
- `MarkWatched({ ids })` hides YouTube videos or removes saved items, in one state write.

These fail with `SourceError` when a source can't do what was asked, and `MarkWatched` fails with `ItemNotFound` for an unknown ID, without marking any of them.

## Errors

Calls fail with `RpcClientError` when the daemon isn't reachable rather than waiting for it to come back. Long-running watchers exit, so run them under a supervisor (such as systemd) that restarts them.

## Licence

Apache 2.0. See [LICENSE](https://github.com/timmo001/upnext/blob/main/LICENSE).
