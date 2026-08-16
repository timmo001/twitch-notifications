import type { MenuItem, MenuVariant, NotifyConfig, ViewId } from "./types.js";

// --- Helpers ---

function item(
  id: string,
  icon: string,
  title: string,
  description: string,
  action: MenuItem["action"],
  variants?: readonly MenuVariant[],
  keywords?: readonly string[],
): MenuItem {
  return {
    id,
    icon,
    title,
    description,
    action,
    ...(variants && { variants }),
    ...(keywords && { keywords }),
  };
}

function cmd(command: string, wait = true): MenuItem["action"] {
  return { type: "command", cmd: command, wait };
}

function silent(command: string): MenuItem["action"] {
  return { type: "silent", cmd: command };
}

function notify(command: string, config: NotifyConfig): MenuItem["action"] {
  return { type: "notify", cmd: command, notify: config };
}

function view(viewId: ViewId): MenuItem["action"] {
  return { type: "view", viewId };
}

function submenu(menuId: string): MenuItem["action"] {
  return { type: "submenu", menuId };
}

function replace(command: string): MenuItem["action"] {
  return { type: "replace", cmd: command };
}

// --- Main menu ---

const mainItems: readonly MenuItem[] = [
  item(
    "serve",
    "󰐊",
    "Run Server",
    "Start the Twitch notification daemon",
    replace("twitch-notifications serve"),
    undefined,
    ["start", "daemon", "run", "server", "launch", ":run", ":serve", "go"],
  ),

  item(
    "channels",
    "󰊗",
    "Channels",
    "Manage watched Twitch channels",
    submenu("channels"),
    undefined,
    ["channel", "add", "remove", "twitch", "watch", ":ch", "streams", "list"],
  ),

  item(
    "recheck",
    "󰑐",
    "Recheck",
    "Trigger a recheck for live channels",
    notify("twitch-notifications -recheck", {
      id: "recheck",
      progress: "Rechecking live channels...",
      success: "Recheck complete",
    }),
    undefined,
    ["refresh", "update", "check", "live", ":rc", "poll", "reload"],
  ),

  item(
    "status",
    "󰋼",
    "Status",
    "Show whether the daemon is active",
    cmd("twitch-notifications -status"),
    undefined,
    ["active", "inactive", "running", "daemon", ":st", "health", "info"],
  ),

  item(
    "config",
    "󰒓",
    "Open Config",
    "Edit the configuration file in your editor",
    cmd(
      '${EDITOR:-${VISUAL:-nano}} "$(twitch-notifications -config-path 2>/dev/null || echo ~/.config/twitch-notifications/config.yaml)"',
    ),
    undefined,
    ["edit", "settings", "preferences", "yaml", ":e", "cfg", "opts", "prefs"],
  ),

  item("quit", "󰩈", "Quit", "Exit the menu", { type: "quit" }, undefined, [
    ":q",
    ":wq",
    ":qa",
    "exit",
    "quit",
    "close",
    "bye",
  ]),
];

// --- Channels submenu ---

const channelItems: readonly MenuItem[] = [
  item(
    "channels.add",
    "󰐕",
    "Add Channel",
    "Add or update a watched channel",
    cmd("twitch-notifications channel add"),
    undefined,
    ["new", "watch", "follow", "subscribe", ":add", "track", "join"],
  ),

  item(
    "channels.remove",
    "󰍴",
    "Remove Channel",
    "Remove a watched channel",
    cmd("twitch-notifications channel remove"),
    undefined,
    ["delete", "unwatch", "unfollow", "unsubscribe", ":rm", "drop", "leave"],
  ),

  item(
    "channels.open",
    "󰏫",
    "Open Channels",
    "Edit the channels file in your editor",
    cmd(
      "${EDITOR:-${VISUAL:-nano}} ~/.config/twitch-notifications/channels.yml",
    ),
    undefined,
    ["edit", "open", "yaml", ":e", "file", "list"],
  ),
];

// --- Registries ---

/** Top-level main menu items */
export const mainMenuItems: readonly MenuItem[] = mainItems;

/** Map of submenu ID → items */
export const submenus: Map<string, readonly MenuItem[]> = new Map([
  ["channels", channelItems],
]);

/** Display titles for submenu breadcrumbs */
export const submenuTitles: Map<string, string> = new Map([
  ["channels", "Channels"],
]);

/** Flat map of every menu item by its ID (main items + all submenu items) */
export const menuItemsById: Map<string, MenuItem> = new Map();

function registerItems(items: readonly MenuItem[]): void {
  for (const m of items) {
    menuItemsById.set(m.id, m);
  }
}

registerItems(mainItems);
registerItems(channelItems);
