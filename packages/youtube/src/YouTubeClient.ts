import {
  Array as Arr,
  Context,
  Effect,
  Layer,
  Option,
  Redacted,
  Schema,
  String as Str,
} from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "effect/http";
import { XMLParser } from "fast-xml-parser";
import { type FeedEntry, type PlaylistEntry, VideoDetails } from "./Media.js";
import { YouTubeAuthError, YouTubeError } from "./YouTubeError.js";

const feedEndpoint = "https://www.youtube.com/feeds/videos.xml";

const videosEndpoint = "https://www.googleapis.com/youtube/v3/videos";

const subscriptionsEndpoint =
  "https://www.googleapis.com/youtube/v3/subscriptions";

const playlistItemsEndpoint =
  "https://www.googleapis.com/youtube/v3/playlistItems";

const playlistsEndpoint = "https://www.googleapis.com/youtube/v3/playlists";

const oembedEndpoint = "https://www.youtube.com/oembed";

// videos.list takes up to 50 IDs per call.
const maxPerRequest = 50;

// Tag values stay strings, so a title like "1984" isn't read as a number.
// Each entry's media group holds the full video description and more, none
// of which is used, so it's kept as text rather than parsed.
const parser = new XMLParser({
  ignoreAttributes: true,
  parseTagValue: false,
  isArray: (name) => name === "entry",
  stopNodes: ["feed.entry.media:group"],
});

const FeedXml = Schema.Struct({
  feed: Schema.Struct({
    entry: Schema.optional(
      Schema.Array(
        Schema.Struct({
          "yt:videoId": Schema.String,
          "yt:channelId": Schema.String,
          title: Schema.String,
          author: Schema.Struct({ name: Schema.String }),
          published: Schema.DateTimeUtcFromString,
        }),
      ),
    ),
  }),
});

const VideosResponse = Schema.Struct({
  items: Schema.Array(VideoDetails),
});

// A channel the signed-in account subscribes to.
export const Subscription = Schema.Struct({
  channelId: Schema.String,
  title: Schema.String,
});

export type Subscription = typeof Subscription.Type;

const SubscriptionsResponse = Schema.Struct({
  items: Schema.Array(
    Schema.Struct({
      snippet: Schema.Struct({
        title: Schema.String,
        resourceId: Schema.Struct({ channelId: Schema.String }),
      }),
    }),
  ),
  nextPageToken: Schema.optional(Schema.String),
});

const ApiErrorBody = Schema.Struct({
  error: Schema.Struct({
    message: Schema.String,
    errors: Schema.optional(
      Schema.Array(Schema.Struct({ reason: Schema.optional(Schema.String) })),
    ),
  }),
});

const PlaylistItemResource = Schema.Struct({
  id: Schema.String,
  snippet: Schema.Struct({
    title: Schema.String,
    // When the video was added to the playlist.
    publishedAt: Schema.DateTimeUtcFromString,
    playlistId: Schema.String,
    videoOwnerChannelId: Schema.optional(Schema.String),
    videoOwnerChannelTitle: Schema.optional(Schema.String),
    resourceId: Schema.Struct({ videoId: Schema.optional(Schema.String) }),
  }),
});

const UploadsResponse = Schema.Struct({
  items: Schema.Array(
    Schema.Struct({
      snippet: Schema.Struct({
        title: Schema.String,
        publishedAt: Schema.DateTimeUtcFromString,
        videoOwnerChannelId: Schema.optional(Schema.String),
        videoOwnerChannelTitle: Schema.optional(Schema.String),
        resourceId: Schema.Struct({ videoId: Schema.optional(Schema.String) }),
      }),
      contentDetails: Schema.optional(
        Schema.Struct({
          videoPublishedAt: Schema.optional(Schema.DateTimeUtcFromString),
        }),
      ),
    }),
  ),
});

// Feeds return 15 uploads, so the fallback reads as many.
const feedLength = 15;

const PlaylistsResponse = Schema.Struct({
  items: Schema.Array(
    Schema.Struct({ snippet: Schema.Struct({ title: Schema.String }) }),
  ),
});

const PlaylistItemsResponse = Schema.Struct({
  items: Schema.Array(PlaylistItemResource),
  nextPageToken: Schema.optional(Schema.String),
});

const toPlaylistEntry = ({
  id,
  snippet,
}: typeof PlaylistItemResource.Type): Option.Option<PlaylistEntry> =>
  Option.map(Option.fromUndefinedOr(snippet.resourceId.videoId), (videoId) => {
    const entry: PlaylistEntry = {
      itemId: id,
      playlistId: snippet.playlistId,
      videoId,
      title: snippet.title,
      addedAt: snippet.publishedAt,
    };

    if (snippet.videoOwnerChannelId === undefined) {
      return entry;
    }

    return {
      ...entry,
      channel: {
        id: snippet.videoOwnerChannelId,
        name: snippet.videoOwnerChannelTitle ?? snippet.videoOwnerChannelId,
      },
    };
  });

export const OEmbed = Schema.Struct({
  title: Schema.String,
  author_name: Schema.String,
  author_url: Schema.String,
  thumbnail_url: Schema.optional(Schema.String),
});

export type OEmbed = typeof OEmbed.Type;

export interface YouTubeClientOptions {
  // Needed to look up videos without signing in. Feeds and oEmbed work
  // without one.
  readonly apiKey: Option.Option<Redacted.Redacted>;
  // From a Google sign-in. Reads subscriptions, and looks up videos when
  // there's no API key.
  readonly accessToken?: Option.Option<Redacted.Redacted>;
}

export interface YouTubeClientService {
  // Whether videos can be looked up, with an API key or a sign-in.
  readonly hasApiKey: boolean;
  // The latest 15 uploads, newest first, from a channel's RSS feed.
  readonly channelFeed: (
    channelId: string,
  ) => Effect.Effect<ReadonlyArray<FeedEntry>, YouTubeError>;
  // The same uploads as `channelFeed`, read from the channel's uploads
  // playlist through the API, for when the feeds are down. Costs 1 unit of
  // quota and needs an API key or a sign-in.
  readonly channelUploads: (
    channelId: string,
  ) => Effect.Effect<ReadonlyArray<FeedEntry>, YouTubeError>;
  // Live state and times for each video, 50 at a time. Needs an API key or a
  // sign-in.
  readonly videos: (
    videoIds: ReadonlyArray<string>,
  ) => Effect.Effect<ReadonlyArray<VideoDetails>, YouTubeError>;
  // Every channel the signed-in account subscribes to. Needs a sign-in.
  readonly subscriptions: Effect.Effect<
    ReadonlyArray<Subscription>,
    YouTubeError | YouTubeAuthError
  >;
  // A playlist's title, or None when there's no such playlist. Needs a
  // sign-in for a private playlist.
  readonly playlistTitle: (
    playlistId: string,
  ) => Effect.Effect<Option.Option<string>, YouTubeError | YouTubeAuthError>;
  // Every video in a playlist, in playlist order, leaving out deleted and
  // private ones. Needs a sign-in for a private playlist.
  readonly playlistItems: (
    playlistId: string,
  ) => Effect.Effect<
    ReadonlyArray<PlaylistEntry>,
    YouTubeError | YouTubeAuthError
  >;
  // Adds a video to the end of a playlist. Needs a sign-in that allows
  // changing playlists.
  readonly addToPlaylist: (
    playlistId: string,
    videoId: string,
  ) => Effect.Effect<PlaylistEntry, YouTubeError | YouTubeAuthError>;
  // Removes an entry, by its `itemId`, from its playlist. Needs a sign-in
  // that allows changing playlists.
  readonly removeFromPlaylist: (
    itemId: string,
  ) => Effect.Effect<void, YouTubeError | YouTubeAuthError>;
  readonly oembed: (url: string) => Effect.Effect<OEmbed, YouTubeError>;
}

const failed = (context: string) => (cause: { message: string }) =>
  new YouTubeError({ message: `${context}: ${cause.message}` });

const checkStatus = (
  context: string,
  response: HttpClientResponse.HttpClientResponse,
) =>
  response.status >= 200 && response.status < 300
    ? Effect.succeed(response)
    : HttpClientResponse.schemaBodyJson(ApiErrorBody)(response).pipe(
        Effect.map(({ error }) => ({
          detail: `: ${error.message}`,
          reason: Arr.findFirst(error.errors ?? [], ({ reason }) =>
            Option.fromUndefinedOr(reason),
          ),
        })),
        Effect.orElseSucceed(() => ({
          detail: "",
          reason: Option.none<string>(),
        })),
        Effect.flatMap(({ detail, reason }) =>
          Effect.fail(
            new YouTubeError({
              message: `${context} failed with status ${response.status}${detail}`,
              status: response.status,
              ...Option.match(reason, {
                onNone: () => ({}),
                onSome: (value) => ({ reason: value }),
              }),
            }),
          ),
        ),
      );

// A rejected sign-in, or one that doesn't allow what was asked, needs
// signing in again.
const signInFailures = <A, R>(
  effect: Effect.Effect<A, YouTubeError, R>,
): Effect.Effect<A, YouTubeError | YouTubeAuthError, R> =>
  effect.pipe(
    Effect.catchIf(
      (error) =>
        error.status === 401 ||
        (error.status === 403 && error.reason === "insufficientPermissions"),
      (error) =>
        Effect.fail(
          new YouTubeAuthError({
            message:
              error.status === 401
                ? "Google rejected the sign-in"
                : "The Google sign-in doesn't allow changing playlists",
          }),
        ),
    ),
  );

export const make = Effect.fn("YouTubeClient.make")(function* (
  options: YouTubeClientOptions,
) {
  const http = (yield* HttpClient.HttpClient).pipe(
    HttpClient.retryTransient({ times: 3 }),
  );

  const fetch = (
    context: string,
    request: HttpClientRequest.HttpClientRequest,
  ) =>
    http.execute(request).pipe(
      Effect.mapError(failed(context)),
      Effect.flatMap((response) => checkStatus(context, response)),
    );

  const channelFeed = (channelId: string) => {
    const context = `read the feed for ${channelId}`;

    return fetch(
      context,
      HttpClientRequest.get(feedEndpoint).pipe(
        HttpClientRequest.setUrlParam("channel_id", channelId),
      ),
    ).pipe(
      Effect.flatMap((response) =>
        response.text.pipe(
          Effect.flatMap((xml) =>
            Effect.try(() => parser.parse(xml)).pipe(
              Effect.flatMap(Schema.decodeUnknownEffect(FeedXml)),
            ),
          ),
          Effect.mapError(failed(context)),
        ),
      ),
      Effect.map(({ feed }) =>
        Arr.map(feed.entry ?? [], (entry): FeedEntry => ({
          videoId: entry["yt:videoId"],
          channelId: entry["yt:channelId"],
          channelName: entry.author.name,
          title: entry.title,
          publishedAt: entry.published,
        })),
      ),
      Effect.withSpan("YouTubeClient.channelFeed"),
    );
  };

  // The key goes in a header, so it never shows up in a logged URL. Without
  // a key, the sign-in's token is used instead.
  const apiKey = Option.filter(options.apiKey, (key) =>
    Str.isNonEmpty(Redacted.value(key)),
  );

  const accessToken = Option.filter(
    options.accessToken ?? Option.none(),
    (token) => Str.isNonEmpty(Redacted.value(token)),
  );

  const bearer = (token: Redacted.Redacted) =>
    HttpClientRequest.setHeader(
      "Authorization",
      `Bearer ${Redacted.value(token)}`,
    );

  const authenticate = Option.orElse(
    Option.map(apiKey, (key) =>
      HttpClientRequest.setHeader("X-Goog-Api-Key", Redacted.value(key)),
    ),
    () => Option.map(accessToken, bearer),
  );

  const videos = (videoIds: ReadonlyArray<string>) =>
    Option.match(authenticate, {
      onNone: () =>
        Effect.fail(
          new YouTubeError({
            message: "looking up videos needs an API key or a sign-in",
          }),
        ),
      onSome: (withAuth) =>
        Effect.forEach(
          Arr.chunksOf(Arr.dedupe(videoIds), maxPerRequest),
          (chunk) =>
            fetch(
              "look up videos",
              HttpClientRequest.get(videosEndpoint).pipe(
                HttpClientRequest.setUrlParams({
                  part: "snippet,liveStreamingDetails",
                  id: Arr.join(chunk, ","),
                  maxResults: String(maxPerRequest),
                }),
                withAuth,
              ),
            ).pipe(
              Effect.flatMap((response) =>
                HttpClientResponse.schemaBodyJson(VideosResponse)(
                  response,
                ).pipe(Effect.mapError(failed("read videos"))),
              ),
              Effect.map(({ items }) => items),
            ),
        ).pipe(Effect.map(Arr.flatten)),
    }).pipe(Effect.withSpan("YouTubeClient.videos"));

  // A channel's uploads playlist has the channel's ID with UU in place of UC.
  const channelUploads = (channelId: string) =>
    Option.match(authenticate, {
      onNone: () =>
        Effect.fail(
          new YouTubeError({
            message: "reading uploads needs an API key or a sign-in",
          }),
        ),
      onSome: (withAuth) => {
        const context = `read the uploads for ${channelId}`;

        return fetch(
          context,
          HttpClientRequest.get(playlistItemsEndpoint).pipe(
            HttpClientRequest.setUrlParams({
              part: "snippet,contentDetails",
              playlistId: `UU${Str.startsWith("UC")(channelId) ? channelId.slice(2) : channelId}`,
              maxResults: String(feedLength),
            }),
            withAuth,
          ),
        ).pipe(
          Effect.flatMap((response) =>
            HttpClientResponse.schemaBodyJson(UploadsResponse)(response).pipe(
              Effect.mapError(failed(context)),
            ),
          ),
          Effect.map(({ items }) =>
            Arr.getSomes(
              Arr.map(items, ({ snippet, contentDetails }) =>
                Option.map(
                  Option.fromUndefinedOr(snippet.resourceId.videoId),
                  (videoId): FeedEntry => ({
                    videoId,
                    channelId: snippet.videoOwnerChannelId ?? channelId,
                    channelName:
                      snippet.videoOwnerChannelTitle ??
                      snippet.videoOwnerChannelId ??
                      channelId,
                    title: snippet.title,
                    // When the playlist entry was added, unless YouTube
                    // says when the video was published.
                    publishedAt:
                      contentDetails?.videoPublishedAt ?? snippet.publishedAt,
                  }),
                ),
              ),
            ),
          ),
        );
      },
    }).pipe(Effect.withSpan("YouTubeClient.channelUploads"));

  const subscriptionsPage = (
    token: Redacted.Redacted,
    pageToken: Option.Option<string>,
  ): Effect.Effect<
    ReadonlyArray<Subscription>,
    YouTubeError | YouTubeAuthError
  > =>
    fetch(
      "read subscriptions",
      HttpClientRequest.get(subscriptionsEndpoint).pipe(
        HttpClientRequest.setUrlParams({
          part: "snippet",
          mine: "true",
          maxResults: String(maxPerRequest),
          ...Option.match(pageToken, {
            onNone: () => ({}),
            onSome: (value) => ({ pageToken: value }),
          }),
        }),
        bearer(token),
      ),
    ).pipe(
      signInFailures,
      Effect.flatMap((response) =>
        HttpClientResponse.schemaBodyJson(SubscriptionsResponse)(response).pipe(
          Effect.mapError(failed("read subscriptions")),
        ),
      ),
      Effect.flatMap(({ items, nextPageToken }) => {
        const page = Arr.map(items, ({ snippet }): Subscription => ({
          channelId: snippet.resourceId.channelId,
          title: snippet.title,
        }));

        return Option.match(Option.fromUndefinedOr(nextPageToken), {
          onNone: () => Effect.succeed(page),
          onSome: (next) =>
            Effect.map(subscriptionsPage(token, Option.some(next)), (rest) =>
              Arr.appendAll(page, rest),
            ),
        });
      }),
    );

  const subscriptions = Option.match(accessToken, {
    onNone: () =>
      Effect.fail(
        new YouTubeAuthError({
          message: "reading subscriptions needs a sign-in",
        }),
      ),
    onSome: (token) => subscriptionsPage(token, Option.none()),
  }).pipe(Effect.withSpan("YouTubeClient.subscriptions"));

  const withSignIn = <A>(
    action: string,
    use: (
      token: Redacted.Redacted,
    ) => Effect.Effect<A, YouTubeError | YouTubeAuthError>,
  ) =>
    Option.match(accessToken, {
      onNone: () =>
        Effect.fail(
          new YouTubeAuthError({ message: `${action} needs a sign-in` }),
        ),
      onSome: use,
    });

  const playlistPage = (
    token: Redacted.Redacted,
    playlistId: string,
    pageToken: Option.Option<string>,
  ): Effect.Effect<
    ReadonlyArray<PlaylistEntry>,
    YouTubeError | YouTubeAuthError
  > =>
    fetch(
      "read the playlist",
      HttpClientRequest.get(playlistItemsEndpoint).pipe(
        HttpClientRequest.setUrlParams({
          part: "snippet",
          playlistId,
          maxResults: String(maxPerRequest),
          ...Option.match(pageToken, {
            onNone: () => ({}),
            onSome: (value) => ({ pageToken: value }),
          }),
        }),
        bearer(token),
      ),
    ).pipe(
      signInFailures,
      Effect.flatMap((response) =>
        HttpClientResponse.schemaBodyJson(PlaylistItemsResponse)(response).pipe(
          Effect.mapError(failed("read the playlist")),
        ),
      ),
      Effect.flatMap(({ items, nextPageToken }) => {
        // Deleted and private videos have no channel.
        const page = Arr.filter(
          Arr.getSomes(Arr.map(items, toPlaylistEntry)),
          ({ channel }) => channel !== undefined,
        );

        return Option.match(Option.fromUndefinedOr(nextPageToken), {
          onNone: () => Effect.succeed(page),
          onSome: (next) =>
            Effect.map(
              playlistPage(token, playlistId, Option.some(next)),
              (rest) => Arr.appendAll(page, rest),
            ),
        });
      }),
    );

  const playlistItems = (playlistId: string) =>
    withSignIn("reading a playlist", (token) =>
      playlistPage(token, playlistId, Option.none()),
    ).pipe(Effect.withSpan("YouTubeClient.playlistItems"));

  const playlistTitle = (playlistId: string) =>
    withSignIn("reading a playlist", (token) =>
      fetch(
        "read the playlist",
        HttpClientRequest.get(playlistsEndpoint).pipe(
          HttpClientRequest.setUrlParams({ part: "snippet", id: playlistId }),
          bearer(token),
        ),
      ).pipe(
        signInFailures,
        Effect.flatMap((response) =>
          HttpClientResponse.schemaBodyJson(PlaylistsResponse)(response).pipe(
            Effect.mapError(failed("read the playlist")),
          ),
        ),
        Effect.map(({ items }) =>
          Option.map(Arr.head(items), ({ snippet }) => snippet.title),
        ),
      ),
    ).pipe(Effect.withSpan("YouTubeClient.playlistTitle"));

  const addToPlaylist = (playlistId: string, videoId: string) =>
    withSignIn("adding to a playlist", (token) =>
      fetch(
        "add to the playlist",
        HttpClientRequest.post(playlistItemsEndpoint).pipe(
          HttpClientRequest.setUrlParam("part", "snippet"),
          HttpClientRequest.bodyJsonUnsafe({
            snippet: {
              playlistId,
              resourceId: { kind: "youtube#video", videoId },
            },
          }),
          bearer(token),
        ),
      ).pipe(
        signInFailures,
        Effect.flatMap((response) =>
          HttpClientResponse.schemaBodyJson(PlaylistItemResource)(
            response,
          ).pipe(Effect.mapError(failed("read the added playlist item"))),
        ),
        Effect.flatMap((item) =>
          Effect.fromOption(toPlaylistEntry(item)).pipe(
            Effect.mapError(
              () =>
                new YouTubeError({
                  message: `YouTube didn't add ${videoId} as a video`,
                }),
            ),
          ),
        ),
      ),
    ).pipe(Effect.withSpan("YouTubeClient.addToPlaylist"));

  const removeFromPlaylist = (itemId: string) =>
    withSignIn("removing from a playlist", (token) =>
      fetch(
        "remove from the playlist",
        HttpClientRequest.delete(playlistItemsEndpoint).pipe(
          HttpClientRequest.setUrlParam("id", itemId),
          bearer(token),
        ),
      ).pipe(signInFailures, Effect.asVoid),
    ).pipe(Effect.withSpan("YouTubeClient.removeFromPlaylist"));

  const oembed = (url: string) =>
    fetch(
      "look up the video",
      HttpClientRequest.get(oembedEndpoint).pipe(
        HttpClientRequest.setUrlParams({ url, format: "json" }),
      ),
    ).pipe(
      Effect.flatMap((response) =>
        HttpClientResponse.schemaBodyJson(OEmbed)(response).pipe(
          Effect.mapError(failed("read the video details")),
        ),
      ),
      Effect.withSpan("YouTubeClient.oembed"),
    );

  return {
    hasApiKey: Option.isSome(authenticate),
    channelFeed,
    channelUploads,
    videos,
    subscriptions,
    playlistTitle,
    playlistItems,
    addToPlaylist,
    removeFromPlaylist,
    oembed,
  } satisfies YouTubeClientService;
});

export class YouTubeClient extends Context.Service<
  YouTubeClient,
  YouTubeClientService
>()("@timmo001/effect-youtube/YouTubeClient") {
  static readonly layer = (options: YouTubeClientOptions) =>
    Layer.effect(YouTubeClient, Effect.map(make(options), YouTubeClient.of));
}
