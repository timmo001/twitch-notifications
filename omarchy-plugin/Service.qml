import QtQuick
import Quickshell
import Quickshell.Io

Item {
  id: root

  property var shell: null
  property bool connected: false
  property var sources: []
  property var items: []
  // The YouTube playlist saved videos go in, with its url and title, or null.
  property var watchLaterPlaylist: null

  // Sent just before a feed update replaces the items, so a panel can keep
  // its place.
  signal itemsAboutToChange()
  property string errorText: "Connecting to Up Next"
  property var actionCommand: []
  property string commandPath: "upnext"
  // Empty uses the daemon's default socket.
  property string socketPath: ""
  property var restartCommand: ["systemctl", "--user", "restart", "upnext.service"]
  property var thumbnails: ({})
  // Thumbnails are decoded at this width, enough for the panel's 64px rows on
  // a 2x display, rather than at the 320px to 440px they're served at.
  readonly property int thumbnailWidth: 160

  // Brand colours, lifted so they read on a dark bar.
  readonly property var sourceColors: ({ twitch: "#a970ff", youtube: "#ff4040" })

  readonly property bool restarting: restartFeedback.running
  readonly property bool actionBusy: actionProcess.running || restarting
  readonly property bool canRecheck: connected && !actionBusy
  // The latest feed's entry for each item ID, so rows can show viewer counts
  // that change every check without being rebuilt.
  readonly property var entriesById: {
    var map = {}
    for (var i = 0; i < items.length; i++) map[items[i].item.id] = items[i]
    return map
  }
  readonly property var liveItems: items.filter(function(entry) { return entry.item.kind === "live" })
  readonly property var trackedLive: liveItems.filter(function(entry) { return entry.tracked === true })
  readonly property int liveCount: trackedLive.length
  readonly property int twitchLiveCount: trackedLive.filter(function(entry) { return entry.item.source === "twitch" }).length
  readonly property int youtubeLiveCount: trackedLive.filter(function(entry) { return entry.item.source === "youtube" }).length
  readonly property int newUploadCount: items.filter(function(entry) {
    return entry.item.kind === "upload" && entry.item.source === "youtube" && entry.tracked === true
  }).length
  readonly property var attentionSources: sources.filter(function(status) {
    return status.state === "auth-required" || status.state === "error"
  })
  readonly property var sourceNames: ({ twitch: "Twitch", youtube: "YouTube" })
  // Problems to flag in the bar: sources needing attention, and a feed the
  // panel couldn't read while still connected.
  readonly property var issues: {
    var lines = attentionSources.map(function(status) {
      var name = sourceNames[status.source] || status.source
      if (status.state === "auth-required") return name + " needs you to sign in again"
      return name + ": " + (status.message || "error")
    })
    if (connected && errorText !== "") lines.push(errorText)
    return lines
  }
  // What's live and new, for the bar tooltip and the panel header.
  readonly property string summary: {
    var parts = []
    if (twitchLiveCount > 0) parts.push(twitchLiveCount + " live on Twitch")
    if (youtubeLiveCount > 0) parts.push(youtubeLiveCount + " live on YouTube")
    if (newUploadCount > 0)
      parts.push(newUploadCount + " new upload" + (newUploadCount === 1 ? "" : "s") + " on YouTube")
    return parts.length > 0 ? parts.join(" · ") : "Nothing new"
  }
  // live, new, active or inactive, for the bar widget.
  readonly property string statusState: !connected ? "inactive"
    : (liveCount > 0 ? "live" : (newUploadCount > 0 ? "new" : "active"))

  function command(args) {
    return socketPath ? [commandPath, "--socket", socketPath].concat(args) : [commandPath].concat(args)
  }

  // Whether two source lists say the same thing. checkedAt moves on every
  // check and nothing here shows it.
  function sameSources(a, b) {
    if (a.length !== b.length) return false
    for (var i = 0; i < a.length; i++) {
      if (a[i].source !== b[i].source || a[i].state !== b[i].state
          || (a[i].message || "") !== (b[i].message || "")) return false
    }
    return true
  }

  function samePlaylist(a, b) {
    if (!a || !b) return a === b
    return a.url === b.url && (a.title || "") === (b.title || "")
  }

  // Sources and the playlist keep their objects while they say the same
  // thing, so the panel only rebuilds its sections when they change.
  function applyFeed(line) {
    var text = String(line || "").trim()
    if (!text) return
    try {
      var feed = JSON.parse(text)
      var nextSources = Array.isArray(feed.sources) ? feed.sources : []
      var nextItems = Array.isArray(feed.items) ? feed.items : []
      var nextPlaylist = feed.watchLaterPlaylist || null
      itemsAboutToChange()
      if (!sameSources(sources, nextSources)) sources = nextSources
      items = nextItems
      if (!samePlaylist(watchLaterPlaylist, nextPlaylist)) watchLaterPlaylist = nextPlaylist
      connected = true
      errorText = ""
      pruneThumbnails()
    } catch (error) {
      errorText = "Invalid feed from Up Next"
    }
  }

  function disconnect() {
    connected = false
    errorText = "Up Next is unavailable"
  }

  function thumbnailFor(item) {
    if (!item || !item.thumbnailUrl) return null
    var thumbnail = thumbnails[item.id]
    if (!thumbnail) {
      thumbnail = thumbnailComponent.createObject(root, { sourceWidth: thumbnailWidth })
      thumbnails[item.id] = thumbnail
    }
    thumbnail.thumbnailUrl = String(item.thumbnailUrl)
    return thumbnail
  }

  // Live previews change while a stream runs; other thumbnails don't.
  function refreshThumbnails() {
    for (var i = 0; i < liveItems.length; i++) {
      var thumbnail = thumbnails[liveItems[i].item.id]
      if (thumbnail) thumbnail.refresh()
    }
  }

  function pruneThumbnails() {
    var ids = {}
    for (var i = 0; i < items.length; i++) ids[items[i].item.id] = true
    for (var id in thumbnails) {
      if (ids[id]) continue
      thumbnails[id].destroy()
      delete thumbnails[id]
    }
  }

  Component {
    id: thumbnailComponent
    Thumbnail {}
  }

  function runAction(command) {
    if (actionProcess.running) return
    actionCommand = command
    actionProcess.running = true
  }

  function recheck(openLive) {
    if (!canRecheck) return
    runAction(command(openLive ? ["recheck", "--open"] : ["recheck"]))
  }

  function restart() {
    if (actionBusy) return
    restartFeedback.restart()
    runAction(restartCommand)
  }

  // Takes one item or a list, and marks them in a single call.
  function markWatched(items) {
    var list = [].concat(items).filter(function(item) { return !!item })
    if (list.length === 0 || !connected) return
    Quickshell.execDetached(command(["watched"].concat(list.map(function(item) { return String(item.id) }))))
  }

  // Adds the item's channel to channels.yml. Twitch takes the login, which is
  // in the item id, and YouTube the channel ID.
  function addChannel(item) {
    if (!item || !connected) return
    var name = item.source === "twitch"
      ? String(item.id).replace(/^twitch:/, "")
      : String(item.channel.id)
    Quickshell.execDetached(command(["channel", "add", String(item.source), name]))
  }

  // Saves the item to watch later, which moves it out of its other section.
  function watchLater(item) {
    if (!item || !connected) return
    Quickshell.execDetached(command(["queue", "add", String(item.url)]))
  }

  function signIn(source) {
    Quickshell.execDetached(command(["auth", String(source)]))
  }

  function openUrl(url) {
    var launcher = Quickshell.env("OMARCHY_HOST") === "desktop"
      ? "xdg-open" : "omarchy-launch-webapp"
    Quickshell.execDetached([launcher, url])
  }

  // Streams the feed now and after every change. The process exits when the
  // daemon stops, and reconnects shortly after.
  Process {
    id: watchProcess
    running: true
    command: root.command(["watch", "--json"])
    // The watcher is a Bun program, whose garbage collector otherwise wakes
    // every second while it waits for the next feed. Every 5 seconds still
    // hands memory back soon after a feed arrives.
    environment: ({ BUN_GC_TIMER_INTERVAL: "5000" })
    stdout: SplitParser {
      onRead: function(line) { root.applyFeed(line) }
    }
    stderr: StdioCollector { waitForEnd: true }
    onExited: {
      root.disconnect()
      reconnect.restart()
    }
  }

  Process {
    id: actionProcess
    command: root.actionCommand
    stdout: StdioCollector { waitForEnd: true }
    stderr: StdioCollector { waitForEnd: true }
  }

  Timer {
    id: reconnect
    interval: root.restarting ? 500 : 5000
    onTriggered: if (!watchProcess.running) watchProcess.running = true
  }

  Timer {
    id: restartFeedback
    interval: 5000
  }
}
