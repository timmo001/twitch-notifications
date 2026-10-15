import {
  Array as Arr,
  Context,
  DateTime,
  Effect,
  HashSet,
  Layer,
  Option,
  Ref,
  Stream,
  String as Str,
} from "effect";
import { HttpClient } from "effect/http";
import type { MediaItem } from "@timmo001/effect-upnext-shared";
import {
  make as makeYouTubeClient,
  thumbnailUrl,
  videoIdFromUrl,
  videoUrl,
} from "@timmo001/effect-youtube";
import {
  type FeedItem,
  ItemNotFound,
  SourceError,
} from "@timmo001/effect-upnext";
import { FeedStore } from "../feed/Feed.js";
import { UpnextState } from "../state/State.js";
import { YouTubeSource } from "./YouTube.js";

// Enough to hide anything still in a channel's feed, even after marking
// every upload from a long subscription list watched.
const maxWatched = 5000;

const toFeedItem = (item: MediaItem): FeedItem => ({
  item,
  tracked: true,
  autoOpen: false,
});

const parseUrl = (value: string) =>
  Option.filter(
    Option.liftThrowable(() => new URL(Str.trim(value)))(),
    ({ protocol }) => protocol === "http:" || protocol === "https:",
  );

export interface WatchLaterService {
  // Saves a URL. A YouTube video goes in the watch-later playlist when one is
  // set. Saving one that's already saved returns the saved item.
  readonly add: (
    url: string,
    title: Option.Option<string>,
  ) => Effect.Effect<FeedItem, SourceError>;
  // Removes a saved item, and hides a YouTube video from the feed for good.
  readonly markWatched: (
    ids: Arr.NonEmptyReadonlyArray<string>,
  ) => Effect.Effect<void, ItemNotFound | SourceError>;
}

export class WatchLater extends Context.Service<
  WatchLater,
  WatchLaterService
>()("WatchLater") {
  static readonly layer = Layer.effect(
    WatchLater,
    Effect.gen(function* () {
      const state = yield* UpnextState;
      const feed = yield* FeedStore;
      const youtube = yield* YouTubeSource;
      const http = yield* HttpClient.HttpClient;

      // oEmbed needs no API key.
      const youtubeClient = yield* makeYouTubeClient({
        apiKey: Option.none(),
      }).pipe(Effect.provideService(HttpClient.HttpClient, http));

      // The watch-later playlist in its own order, then items saved here in
      // the order they were saved.
      const playlistItems = yield* Ref.make<ReadonlyArray<MediaItem>>([]);

      const local = Effect.map(state.get, (current) => current.saved ?? []);

      const saved = Effect.zipWith(
        Ref.get(playlistItems),
        local,
        (there, here) => Arr.unionWith(there, here, (a, b) => a.id === b.id),
      );

      const publish = Effect.flatMap(saved, (items) =>
        feed.setSaved(Arr.map(items, toFeedItem)),
      );

      yield* publish;

      // A video in the watch-later playlist counts as watched, so its upload
      // stays hidden once it's removed from the playlist on YouTube.
      const markSeen = Effect.fn("WatchLater.markSeen")(function* (
        items: ReadonlyArray<MediaItem>,
      ) {
        const ids = Arr.map(items, (item) => item.id);

        yield* state
          .update((current) => ({
            ...current,
            watched: Arr.takeRight(
              Arr.union(current.watched ?? [], ids),
              maxWatched,
            ),
          }))
          .pipe(
            Effect.catch((error) =>
              Effect.logWarning(
                "Couldn't mark the watch-later playlist watched",
                error.message,
              ),
            ),
          );

        yield* feed.remove(ids);
      });

      yield* youtube.watchLater.pipe(
        Stream.runForEach((items) =>
          Ref.set(playlistItems, items).pipe(
            Effect.andThen(markSeen(items)),
            Effect.andThen(publish),
          ),
        ),
        Effect.forkScoped,
      );

      // Hides videos marked watched on another machine, and shows its saved
      // items.
      yield* state.libraryChanges.pipe(
        Stream.runForEach(() =>
          Effect.gen(function* () {
            yield* feed.remove((yield* state.get).watched ?? []);
            yield* publish;
          }),
        ),
        Effect.forkScoped,
      );

      const youtubeItem = Effect.fn("WatchLater.youtubeItem")(function* (
        videoId: string,
        title: Option.Option<string>,
        savedAt: DateTime.Utc,
      ) {
        const url = videoUrl(videoId);

        const details = yield* youtubeClient.oembed(url).pipe(
          Effect.asSome,
          Effect.catch((error) =>
            Effect.logWarning("Couldn't look up the video", error.message).pipe(
              Effect.as(Option.none()),
            ),
          ),
        );

        return {
          id: `youtube:${videoId}`,
          source: "youtube",
          kind: "saved",
          title: Option.getOrElse(
            Option.orElse(title, () =>
              Option.map(details, (value) => value.title),
            ),
            () => url,
          ),
          url,
          ...Option.match(details, {
            onNone: () => ({}),
            onSome: (value) => ({
              channel: {
                id: value.author_url,
                name: value.author_name,
                url: value.author_url,
              },
            }),
          }),
          thumbnailUrl: thumbnailUrl(videoId),
          publishedAt: savedAt,
        } satisfies MediaItem;
      });

      const add = Effect.fn("WatchLater.add")(function* (
        value: string,
        title: Option.Option<string>,
      ) {
        const url = yield* Effect.fromOption(parseUrl(value)).pipe(
          Effect.mapError(
            () =>
              new SourceError({
                source: "link",
                message: `${value} isn't a web address`,
              }),
          ),
        );

        const wantedTitle = Option.filter(
          Option.map(title, Str.trim),
          Str.isNonEmpty,
        );

        const videoId = videoIdFromUrl(url.href);

        if (Option.isSome(videoId)) {
          const inPlaylist = yield* youtube.saveToWatchLater(videoId.value);

          if (Option.isSome(inPlaylist)) {
            return toFeedItem(inPlaylist.value);
          }
        }

        const existing = Arr.findFirst(yield* saved, (item) =>
          Option.match(videoId, {
            onNone: () => item.url === url.href,
            onSome: (id) => item.id === `youtube:${id}`,
          }),
        );

        if (Option.isSome(existing)) {
          return toFeedItem(existing.value);
        }

        const now = yield* DateTime.now;

        const item = yield* Option.match(videoId, {
          onNone: () =>
            Effect.succeed<MediaItem>({
              id: `link:${crypto.randomUUID()}`,
              source: "link",
              kind: "saved",
              title: Option.getOrElse(wantedTitle, () => url.href),
              url: url.href,
              publishedAt: now,
            }),
          onSome: (id) => youtubeItem(id, wantedTitle, now),
        });

        yield* state
          .update((current) => ({
            ...current,
            saved: Arr.append(current.saved ?? [], item),
          }))
          .pipe(
            Effect.mapError(
              (error) =>
                new SourceError({
                  source: item.source,
                  message: error.message,
                }),
            ),
          );

        yield* publish;

        return toFeedItem(item);
      });

      // Marks nothing unless every ID is a saved item or a YouTube video in
      // the feed.
      const markWatched = Effect.fn("WatchLater.markWatched")(function* (
        ids: Arr.NonEmptyReadonlyArray<string>,
      ) {
        const savedIds = HashSet.fromIterable(
          Arr.map(yield* saved, (item) => item.id),
        );

        const feedIds = HashSet.fromIterable(
          Arr.map((yield* feed.reported).items, ({ item }) => item.id),
        );

        const isYouTube = Str.startsWith("youtube:");

        yield* Option.match(
          Arr.findFirst(
            ids,
            (id) =>
              !HashSet.has(savedIds, id) &&
              !(isYouTube(id) && HashSet.has(feedIds, id)),
          ),
          {
            onNone: () => Effect.void,
            onSome: (id) => Effect.fail(new ItemNotFound({ id })),
          },
        );

        const marked = HashSet.fromIterable(ids);
        const youtubeIds = Arr.filter(ids, isYouTube);

        yield* youtube.removeFromWatchLater(youtubeIds);

        yield* state
          .update((current) => ({
            ...current,
            saved: Arr.filter(
              current.saved ?? [],
              (item) => !HashSet.has(marked, item.id),
            ),
            watched: Arr.takeRight(
              Arr.appendAll(
                Arr.filter(
                  current.watched ?? [],
                  (seen) => !HashSet.has(marked, seen),
                ),
                youtubeIds,
              ),
              maxWatched,
            ),
          }))
          .pipe(
            Effect.mapError(
              (error) => new SourceError({ message: error.message }),
            ),
          );

        yield* feed.remove(ids);
        yield* publish;
      });

      return WatchLater.of({ add, markWatched });
    }),
  );
}
