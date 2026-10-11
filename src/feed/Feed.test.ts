import { describe, expect, test } from "bun:test";
import { DateTime } from "effect";
import type {
  Feed,
  FeedItem,
  SourceStatus,
  WatchLaterPlaylist,
} from "@timmo001/effect-upnext";
import type { MediaItem } from "@timmo001/effect-upnext-shared";
import { present, replaceSource, sameFeed } from "./Feed.js";

const live = (
  id: string,
  source: MediaItem["source"],
  extra: {
    readonly tracked?: boolean;
    readonly position?: number;
    readonly viewers?: number;
  },
): FeedItem => {
  const item: MediaItem = {
    id,
    source,
    kind: "live",
    title: id,
    url: `https://example.com/${id}`,
    viewers: extra.viewers,
  };

  return {
    item,
    tracked: extra.tracked ?? false,
    position: extra.position,
    autoOpen: false,
  };
};

describe("replaceSource", () => {
  test("orders tracked live channels by source and position, then the rest by viewers", () => {
    const withYouTube = replaceSource(
      { sources: [], items: [] },
      { source: "youtube", state: "ok" },
      [
        live("youtube-other", "youtube", { viewers: 500 }),
        live("youtube-second", "youtube", { tracked: true, position: 1 }),
        live("youtube-first", "youtube", { tracked: true, position: 0 }),
      ],
    );

    const feed = replaceSource(withYouTube, { source: "twitch", state: "ok" }, [
      live("twitch-busy", "twitch", { viewers: 9000 }),
      live("twitch-quiet", "twitch", { viewers: 20 }),
      live("twitch-second", "twitch", {
        tracked: true,
        position: 1,
        viewers: 1,
      }),
      live("twitch-first", "twitch", {
        tracked: true,
        position: 0,
        viewers: 1,
      }),
    ]);

    expect(present(feed).items.map(({ item }) => item.id)).toEqual([
      "twitch-first",
      "twitch-second",
      "youtube-first",
      "youtube-second",
      "twitch-busy",
      "youtube-other",
      "twitch-quiet",
    ]);
  });
});

describe("present", () => {
  test("shows a saved video only in the watch-later queue", () => {
    const stream = live("youtube:abc", "youtube", { tracked: true });

    const saved: FeedItem = {
      ...stream,
      item: { ...stream.item, kind: "saved" },
    };

    const feed = present({
      sources: [],
      items: [stream, live("youtube:other", "youtube", {}), saved],
    });

    expect(feed.items.map(({ item }) => `${item.kind}:${item.id}`)).toEqual([
      "live:youtube:other",
      "saved:youtube:abc",
    ]);
  });
});

describe("sameFeed", () => {
  const checkedAt = DateTime.makeUnsafe("2026-10-11T09:00:00Z");
  const later = DateTime.makeUnsafe("2026-10-11T09:01:00Z");

  const feed = (
    source: SourceStatus,
    items: ReadonlyArray<FeedItem>,
  ): Feed => ({
    sources: [source],
    items,
  });

  const playlist: WatchLaterPlaylist = {
    url: "https://www.youtube.com/playlist?list=PL1",
  };

  const stream = live("twitch:someone", "twitch", {
    tracked: true,
    position: 0,
    viewers: 10,
  });

  test("ignores a check that only moved checkedAt", () => {
    expect(
      sameFeed(
        feed({ source: "twitch", state: "ok", checkedAt }, [stream]),
        feed({ source: "twitch", state: "ok", checkedAt: later }, [stream]),
      ),
    ).toBe(true);
  });

  test("sees a change to an item, a source or the playlist", () => {
    const before = feed({ source: "twitch", state: "ok", checkedAt }, [stream]);

    expect(
      sameFeed(
        before,
        feed({ source: "twitch", state: "ok", checkedAt: later }, [
          live("twitch:someone", "twitch", {
            tracked: true,
            position: 0,
            viewers: 11,
          }),
        ]),
      ),
    ).toBe(false);

    expect(
      sameFeed(
        before,
        feed(
          {
            source: "twitch",
            state: "error",
            message: "down",
            checkedAt: later,
          },
          [stream],
        ),
      ),
    ).toBe(false);

    expect(
      sameFeed(before, {
        ...feed({ source: "twitch", state: "ok", checkedAt: later }, [stream]),
        watchLaterPlaylist: playlist,
      }),
    ).toBe(false);
  });

  test("compares items with dates by value", () => {
    const uploaded = (at: string): FeedItem => ({
      item: {
        id: "youtube:video",
        source: "youtube",
        kind: "upload",
        title: "Video",
        url: "https://www.youtube.com/watch?v=video",
        publishedAt: DateTime.makeUnsafe(at),
      },
      tracked: true,
      autoOpen: false,
    });

    expect(
      sameFeed(
        feed({ source: "youtube", state: "ok", checkedAt }, [
          uploaded("2026-10-10T12:00:00Z"),
        ]),
        feed({ source: "youtube", state: "ok", checkedAt: later }, [
          uploaded("2026-10-10T12:00:00Z"),
        ]),
      ),
    ).toBe(true);
  });
});
