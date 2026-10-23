import { BunFileSystem, BunPath } from "@effect/platform-bun";
import { describe, expect, test } from "bun:test";
import { ConfigProvider, Effect, FileSystem, Layer, Path } from "effect";
import { readState, UpnextState } from "./State.js";

const savedItem = {
  id: "link:one",
  source: "link",
  kind: "saved",
  title: "One",
  url: "https://example.com/one",
  publishedAt: "2026-10-01T00:00:00.000Z",
};

const tokens = { twitch: { accessToken: "access", refreshToken: "refresh" } };

// What a file holds, with saved items reduced to their IDs.
const summarise = (file: string) =>
  Effect.map(readState(file), ({ twitch, watched, saved }) => ({
    twitch,
    watched,
    saved: saved?.map((item) => item.id),
  }));

describe("UpnextState", () => {
  test("moves the library out of state.json and merges synced changes", async () => {
    const result = await Effect.runPromise(
      Effect.gen(function* () {
        const fs = yield* FileSystem.FileSystem;
        const path = yield* Path.Path;
        const root = yield* fs.makeTempDirectory({ prefix: "upnext-state-" });
        const stateFile = path.join(root, "state", "upnext", "state.json");
        const libraryFile = path.join(root, "data", "upnext", "library.json");

        yield* fs.makeDirectory(path.dirname(stateFile), { recursive: true });
        yield* fs.writeFileString(
          stateFile,
          JSON.stringify({
            ...tokens,
            watched: ["a", "b"],
            saved: [savedItem],
          }),
        );

        return yield* Effect.gen(function* () {
          const state = yield* UpnextState;

          const moved = {
            state: yield* summarise(stateFile),
            library: yield* summarise(libraryFile),
          };

          // Another machine marks "c" watched and removes the saved item.
          yield* fs.writeFileString(
            libraryFile,
            JSON.stringify({ watched: ["a", "c"], saved: [] }),
          );

          yield* state.update((current) => ({
            ...current,
            watched: [...(current.watched ?? []), "d"],
          }));

          return {
            moved,
            merged: yield* summarise(libraryFile),
            tokens: yield* summarise(stateFile),
          };
        }).pipe(
          Effect.provide(UpnextState.layer),
          Effect.provideService(
            ConfigProvider.ConfigProvider,
            ConfigProvider.fromEnvRecord({
              HOME: root,
              XDG_STATE_HOME: path.join(root, "state"),
              XDG_DATA_HOME: path.join(root, "data"),
            }),
          ),
        );
      }).pipe(
        Effect.scoped,
        Effect.provide(Layer.mergeAll(BunFileSystem.layer, BunPath.layer)),
      ),
    );

    const onlyTokens = { ...tokens, watched: undefined, saved: undefined };

    expect(result.moved).toEqual({
      state: onlyTokens,
      library: {
        twitch: undefined,
        watched: ["a", "b"],
        saved: ["link:one"],
      },
    });

    expect(result.merged).toEqual({
      twitch: undefined,
      watched: ["a", "b", "c", "d"],
      saved: [],
    });

    expect(result.tokens).toEqual(onlyTokens);
  });

  test("adds back what was watched in a Syncthing conflict copy", async () => {
    const result = await Effect.runPromise(
      Effect.gen(function* () {
        const fs = yield* FileSystem.FileSystem;
        const path = yield* Path.Path;
        const root = yield* fs.makeTempDirectory({ prefix: "upnext-state-" });
        const dataDirectory = path.join(root, "data", "upnext");
        const libraryFile = path.join(dataDirectory, "library.json");

        const conflictFile = path.join(
          dataDirectory,
          "library.sync-conflict-20261023-080337-GWRJZI7.json",
        );

        yield* fs.makeDirectory(dataDirectory, { recursive: true });
        yield* fs.writeFileString(
          libraryFile,
          JSON.stringify({ watched: ["a", "b"] }),
        );
        yield* fs.writeFileString(
          conflictFile,
          JSON.stringify({ watched: ["a", "c"] }),
        );

        return yield* Effect.gen(function* () {
          yield* UpnextState;

          return {
            library: yield* summarise(libraryFile),
            conflictLeft: yield* fs.exists(conflictFile),
          };
        }).pipe(
          Effect.provide(UpnextState.layer),
          Effect.provideService(
            ConfigProvider.ConfigProvider,
            ConfigProvider.fromEnvRecord({
              HOME: root,
              XDG_STATE_HOME: path.join(root, "state"),
              XDG_DATA_HOME: path.join(root, "data"),
            }),
          ),
        );
      }).pipe(
        Effect.scoped,
        Effect.provide(Layer.mergeAll(BunFileSystem.layer, BunPath.layer)),
      ),
    );

    expect(result).toEqual({
      library: { twitch: undefined, watched: ["a", "b", "c"], saved: [] },
      conflictLeft: false,
    });
  });
});
