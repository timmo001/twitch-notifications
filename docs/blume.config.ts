import { docsConfig } from "@timmo001/docs-kit/blume";
import { defineConfig } from "blume";
import commandPages from "./commands-sidebar.json" with { type: "json" };

export default defineConfig(
  docsConfig({
    title: "Up Next",
    description:
      "Twitch live channels, YouTube uploads and a watch-later queue in one feed.",
    site: "upnext.timmo.dev",
    github: { owner: "timmo001", repo: "upnext" },
    navigation: {
      sidebar: [
        "/",
        "/install",
        {
          label: "Setup",
          items: ["/setup/twitch", "/setup/youtube", "/omarchy"],
        },
        "/configuration",
        "/running",
        "/libraries",
        "/privacy",
        {
          label: "Commands",
          root: "/commands",
          items: commandPages.filter((page) => page !== "/commands"),
          display: "group",
          collapsed: true,
        },
        {
          label: "Migrations",
          items: ["/from-twitch-notifications"],
        },
      ],
    },
  }),
);
