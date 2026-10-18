import {
  Config,
  Context,
  Data,
  Effect,
  FileSystem,
  Layer,
  Option,
  Path,
  String as Str,
} from "effect";
import { ChildProcess, ChildProcessSpawner } from "effect/process";
import { UpnextConfig } from "../config/Config.js";

class CommandFailed extends Data.TaggedError("CommandFailed")<{
  readonly message: string;
}> {}

const appName = "Up Next";

const omarchyGlyph = "󰕃";

const iconFile = "/usr/share/icons/hicolor/scalable/apps/upnext.svg";

export interface Notification {
  readonly title: string;
  readonly body?: string;
  // Opened when the notification is clicked.
  readonly url?: string;
  // Run when the notification is clicked, instead of opening a URL.
  readonly command?: string;
  // Plays the configured sound.
  readonly sound?: boolean;
}

export interface DesktopService {
  readonly notify: (notification: Notification) => Effect.Effect<void>;
  readonly open: (url: string) => Effect.Effect<void>;
}

const shellQuote = (value: string) =>
  `'${Str.replaceAll("'", `'\\''`)(value)}'`;

// Omarchy desktops open streams in the browser, laptops in a web app window.
const browserCommand = (host: Option.Option<string>) =>
  Option.match(host, {
    onNone: () => "xdg-open",
    onSome: (value) => {
      switch (value) {
        case "desktop":
          return "omarchy-launch-browser";
        case "laptop":
          return "omarchy-launch-webapp";
        default:
          return "xdg-open";
      }
    },
  });

export class Desktop extends Context.Service<Desktop, DesktopService>()(
  "Desktop",
) {
  static readonly layer = Layer.effect(
    Desktop,
    Effect.gen(function* () {
      const spawner = yield* ChildProcessSpawner.ChildProcessSpawner;
      const fs = yield* FileSystem.FileSystem;
      const path = yield* Path.Path;
      const config = yield* UpnextConfig;

      const host = yield* Config.option(Config.String("OMARCHY_HOST"));
      const home = yield* Config.String("HOME").pipe(Config.withDefault(""));
      const browser = browserCommand(host);

      // setsid -f hides the launcher's exit code, so a browser with nowhere to
      // show a window would otherwise be logged as opened.
      const waylandDisplay = yield* Config.option(
        Config.String("WAYLAND_DISPLAY"),
      );

      const x11Display = yield* Config.option(Config.String("DISPLAY"));

      const hasDisplay = Option.isSome(
        Option.orElse(waylandDisplay, () => x11Display),
      );

      const icon = (yield* fs
        .exists(iconFile)
        .pipe(Effect.orElseSucceed(() => false)))
        ? Option.some(iconFile)
        : Option.none();

      const iconArgs = (flag: string) =>
        Option.match(icon, {
          onNone: () => [],
          onSome: (file) => [flag, file],
        });

      const run = (command: string, args: ReadonlyArray<string>) =>
        spawner
          .exitCode(
            ChildProcess.make(command, args, {
              stdout: "ignore",
              stderr: "ignore",
            }),
          )
          .pipe(
            Effect.flatMap((code) =>
              code === 0
                ? Effect.void
                : Effect.fail(
                    new CommandFailed({
                      message: `${command} exited with ${code}`,
                    }),
                  ),
            ),
          );

      // setsid detaches the browser, so it outlives the daemon.
      const open = (url: string) =>
        hasDisplay
          ? run("setsid", ["-f", browser, url]).pipe(
              Effect.tap(() => Effect.logInfo("Opened", url)),
              Effect.catch((error) =>
                Effect.logWarning("Could not open", url, error),
              ),
            )
          : Effect.logWarning(
              "Could not open",
              url,
              "no WAYLAND_DISPLAY or DISPLAY, so the service started before the desktop session. Run systemctl --user restart upnext",
            );

      const playSound = Effect.gen(function* () {
        const { soundFile } = yield* config.settings;

        if (Option.isNone(soundFile)) {
          return;
        }

        const file = path.resolve(
          Str.startsWith("~/")(soundFile.value)
            ? path.join(home, soundFile.value.slice(2))
            : soundFile.value,
        );

        if (!(yield* fs.exists(file))) {
          return yield* Effect.logWarning("Sound file not found", file);
        }

        yield* run("paplay", [file]).pipe(
          Effect.catch(() => run("aplay", [file])),
        );
      }).pipe(
        Effect.catchCause((cause) =>
          Effect.logWarning("Could not play the sound", cause),
        ),
      );

      const clickCommand = (notification: Notification) =>
        Option.orElse(Option.fromUndefinedOr(notification.command), () =>
          Option.map(
            Option.fromUndefinedOr(notification.url),
            (url) => `${browser} ${shellQuote(url)}`,
          ),
        );

      const omarchy = (notification: Notification) =>
        run("omarchy", [
          "notification",
          "send",
          "-g",
          omarchyGlyph,
          ...iconArgs("-i"),
          "--app-name",
          appName,
          ...Option.match(clickCommand(notification), {
            onNone: () => [],
            onSome: (command) => ["--exec", command],
          }),
          notification.title,
          ...(notification.body ? [notification.body] : []),
        ]);

      // notify-send can't run anything on click without blocking.
      const notifySend = (notification: Notification) =>
        run("notify-send", [
          "--app-name",
          appName,
          ...iconArgs("--icon"),
          notification.title,
          ...(notification.body ? [notification.body] : []),
        ]);

      const notify = (notification: Notification) =>
        Effect.gen(function* () {
          if (notification.sound) {
            yield* Effect.forkDetach(playSound);
          }

          yield* omarchy(notification).pipe(
            Effect.catch(() => notifySend(notification)),
            Effect.catch((error) =>
              Effect.logWarning(
                "Could not show a notification",
                notification.title,
                error,
              ),
            ),
          );
        });

      return Desktop.of({ notify, open });
    }),
  );
}
