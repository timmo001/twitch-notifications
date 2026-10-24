import QtQuick
import QtQuick.Controls
import Quickshell
import qs.Commons
import qs.Commons as Commons
import qs.Ui

Panel {
  id: root
  moduleName: "timmo.upnext"
  ipcTarget: "timmo.upnext"
  manageIpc: false

  property var anchorItem: null
  property var hostWidget: null
  property var service: null

  readonly property var barIdentity: hostWidget || root
  readonly property color contentForeground: bar ? bar.foreground : Commons.Color.foreground
  readonly property string contentFontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property var sourceColors: service ? service.sourceColors : ({ twitch: "#a970ff", youtube: "#ff4040" })
  readonly property color liveColor: sourceColors.twitch
  readonly property var sourceIcons: ({ twitch: "󰕃", youtube: "󰗃", link: "󰌹" })
  readonly property int actionCount: 5
  readonly property var actionLabels: [
    "Recheck",
    "Open live auto-open channels",
    "Open Twitch following",
    "Open YouTube subscriptions",
    "Restart Up Next"
  ]
  readonly property var actionIcons: ["󰑐", "󰏌", "󰕃", "󰗃", "󰜉"]
  readonly property var actionUrls: [
    "",
    "",
    "https://www.twitch.tv/directory/following/live",
    "https://www.youtube.com/feed/subscriptions",
    ""
  ]
  // Live channels from channels.yml, in its order, then other followed
  // channels by viewers. Uploads split the same way. Each section lists the
  // sources it comes from, so it can show it's loading until they report.
  readonly property var sections: [
    { kind: "live", title: "LIVE", sources: ["twitch", "youtube"], empty: "No one you track is live" },
    { kind: "followed", title: "FOLLOWED", sources: ["twitch"], empty: "No one else you follow is live" },
    { kind: "upcoming", title: "UPCOMING", sources: ["youtube"], empty: "No upcoming streams" },
    { kind: "saved", title: "WATCH LATER", sources: [], empty: "Nothing saved for later" },
    { kind: "upload", title: "NEW UPLOADS", sources: ["youtube"], empty: "All caught up" },
    { kind: "other", title: "OTHER UPLOADS", sources: ["youtube"], empty: "All caught up" }
  ]
  readonly property var defaultExpanded: ({ live: true, followed: true, upcoming: false, upload: true, other: false, saved: true })
  property var expanded: defaultExpanded
  // The source the panel was last opened on, so its bind can close it again.
  property string focusedSource: ""
  // Refreshed while the panel is open, so relative times stay current.
  property double now: Date.now()
  // The item the right-click menu is open on.
  property var menuEntry: null

  // The rows the feed calls for. panelRows only takes them over when they
  // list something different, so a check that only moved viewer counts
  // doesn't rebuild every row. Rows read those counts from the latest feed.
  readonly property var latestRows: buildPanelRows()
  property var panelRows: []
  onLatestRowsChanged: syncRows()
  Component.onCompleted: syncRows()
  readonly property var filteredHeaderActions: filterRows("header-action")
  readonly property var filteredActions: filterRows("action")
  readonly property var filteredAttention: filterRows("attention")
  readonly property var filteredSections: buildSections()
  readonly property var navigationRows: buildNavigationRows()

  function sourceIcon(source) {
    return sourceIcons[source] || sourceIcons.link
  }

  function sourceColor(source) {
    return sourceColors[source] || contentForeground
  }

  function ago(iso, at) {
    var minutes = Math.floor((at - Date.parse(iso)) / 60000)
    if (!(minutes >= 0)) return ""
    if (minutes < 60) return minutes + "m ago"
    if (minutes < 60 * 24) return Math.floor(minutes / 60) + "h ago"
    return Math.floor(minutes / (60 * 24)) + "d ago"
  }

  function startsAt(iso, at) {
    var date = new Date(iso)
    if (isNaN(date.getTime())) return "Upcoming"
    var sameDay = date.toDateString() === new Date(at).toDateString()
    return "Starts " + (sameDay ? "" : date.toLocaleDateString(Qt.locale(), "ddd d MMM") + " ")
      + date.toLocaleTimeString(Qt.locale(), "HH:mm")
  }

  function viewers(count) {
    if (!(count >= 0)) return ""
    var text = count < 1000 ? String(count)
      : (count < 10000 ? (count / 1000).toFixed(1).replace(/\.0$/, "") + "K"
        : (count < 1000000 ? Math.round(count / 1000) + "K"
          : (count / 1000000).toFixed(1).replace(/\.0$/, "") + "M"))
    return "󰈈 " + text
  }

  // The row's third line, as of `at`.
  function detailFor(entry, at) {
    var item = entry.item
    var published = item.publishedAt ? String(item.publishedAt) : ""
    if (item.kind === "live")
      return [viewers(item.viewers), item.category || "Live"]
        .filter(function(part) { return part !== "" }).join(" · ")
    if (item.kind === "upcoming") return startsAt(published, at)
    if (item.kind === "saved") return published ? "Saved " + ago(published, at) : "Saved"
    return ago(published, at)
  }

  // What the filter matches besides the names: a live stream's category.
  // Viewer counts and times change too often to be worth rebuilding the rows
  // for.
  function filterDetailFor(entry) {
    return entry.item.kind === "live" ? (entry.item.category || "") : ""
  }

  function sectionFor(entry) {
    var kind = entry.item.kind
    if (kind === "live") return entry.tracked ? "live" : "followed"
    if (kind === "upload") return entry.tracked ? "upload" : "other"
    return kind
  }

  function buildPanelRows() {
    var rows = []
    for (var i = 0; i < actionCount; i++) {
      rows.push({
        key: "action:" + i,
        kind: i === 0 || i === 4 ? "header-action" : "action",
        section: "action",
        actionIndex: i,
        primaryText: actionLabels[i],
        secondaryText: ""
      })
    }
    var attention = service ? service.attentionSources : []
    for (var j = 0; j < attention.length; j++) {
      var status = attention[j]
      rows.push({
        key: "attention:" + status.source,
        kind: "attention",
        section: "attention",
        value: status,
        primaryText: status.source,
        secondaryText: status.message || status.state
      })
    }
    // The daemon already orders the feed.
    var items = service && service.connected ? service.items : []
    for (var k = 0; k < items.length; k++) {
      var entry = items[k]
      var item = entry.item
      rows.push({
        key: "item:" + item.id,
        kind: "item",
        section: sectionFor(entry),
        value: entry,
        primaryText: item.channel ? item.channel.name : item.title,
        secondaryText: item.channel ? item.title : "",
        tertiaryText: filterDetailFor(entry)
      })
    }
    var playlist = service && service.connected ? service.watchLaterPlaylist : null
    if (playlist && playlist.url) {
      rows.push({
        key: "playlist",
        kind: "playlist",
        section: "saved",
        value: playlist,
        primaryText: playlist.title
          ? "Open “" + playlist.title + "” playlist on YouTube"
          : "Open your watch-later playlist on YouTube",
        secondaryText: ""
      })
    }
    return rows
  }

  function sameRows(a, b) {
    if (a.length !== b.length) return false
    for (var i = 0; i < a.length; i++) {
      var x = a[i]
      var y = b[i]
      if (x.key !== y.key || x.kind !== y.kind || x.section !== y.section
          || x.primaryText !== y.primaryText || x.secondaryText !== y.secondaryText
          || (x.tertiaryText || "") !== (y.tertiaryText || "")) return false
    }
    return true
  }

  function syncRows() {
    if (!panelRows || !sameRows(panelRows, latestRows)) panelRows = latestRows
  }

  function filterRows(kind) {
    return filterController.filteredModel.filter(function(entry) { return entry.kind === kind })
  }

  // What an empty section says: that its sources haven't reported yet, that
  // they failed, or that there's nothing in it.
  function sectionStatus(section) {
    var statuses = service ? service.sources : []
    var loading = !(service && service.connected)
    var failed = []
    for (var i = 0; i < section.sources.length; i++) {
      var source = section.sources[i]
      var status = statuses.find(function(each) { return each.source === source })
      if (!status) loading = true
      else if (status.state === "error" || status.state === "auth-required")
        failed.push(source === "youtube" ? "YouTube" : "Twitch")
    }
    if (loading) return { loading: true, text: "Loading…" }
    if (failed.length > 0) return { loading: false, text: "Couldn't check " + failed.join(" or ") }
    return { loading: false, text: section.empty }
  }

  // Disabled sources' sections are left out. The rest stay, even when empty,
  // unless a filter is narrowing them down.
  function sectionEnabled(section) {
    if (section.sources.length === 0) return true
    var statuses = service ? service.sources : []
    return section.sources.some(function(source) {
      var status = statuses.find(function(each) { return each.source === source })
      return !status || status.state !== "disabled"
    })
  }

  function buildSections() {
    if (service && !service.connected && service.errorText !== "") return []
    var items = filterRows("item")
    var links = filterRows("playlist")
    return sections.filter(sectionEnabled).map(function(section) {
      var rows = items.filter(function(entry) { return entry.section === section.kind })
      var shown = filterController.filterText || expanded[section.kind]
      var link = links.find(function(entry) { return entry.section === section.kind }) || null
      var status = sectionStatus(section)
      return {
        kind: section.kind,
        title: section.title,
        count: rows.length,
        loading: rows.length === 0 && status.loading,
        emptyText: status.text,
        toggleKey: "toggle:" + section.kind,
        markAllKey: "mark-all:" + section.kind,
        // What "mark all as watched" covers: the section's videos that match
        // the filter, collapsed or not. Watch later has no mark all.
        markable: section.kind === "saved"
          ? []
          : rows.filter(function(entry) { return canMarkWatched(entry.value) }),
        rows: shown ? rows : [],
        // A last row that opens the section's playlist, such as Watch later's.
        link: shown ? link : null
      }
    }).filter(function(section) { return section.count > 0 || section.link || !filterController.filterText })
  }

  function buildNavigationRows() {
    var rows = filteredHeaderActions.concat(filteredActions, filteredAttention)
    for (var i = 0; i < filteredSections.length; i++) {
      var section = filteredSections[i]
      if (!filterController.filterText)
        rows.push({ key: section.toggleKey, kind: "toggle", section: section.kind })
      if (section.markable.length > 0)
        rows.push({ key: section.markAllKey, kind: "mark-all", section: section.kind })
      rows = rows.concat(section.rows)
      if (section.link) rows.push(section.link)
    }
    return rows
  }

  function markAllWatched(kind) {
    if (!service) return
    var section = filteredSections.find(function(each) { return each.kind === kind })
    if (!section) return
    service.markWatched(section.markable.map(function(entry) { return entry.value.item }))
  }

  function toggleSection(kind) {
    var next = Object.assign({}, expanded)
    next[kind] = !next[kind]
    expanded = next
  }

  // Followed, Upcoming, Watch later and Other uploads open when there's
  // something in them, and start collapsed when there isn't.
  function openingExpanded() {
    var next = Object.assign({}, defaultExpanded)
    var collapsible = ["followed", "upcoming", "saved", "other"]
    for (var i = 0; i < collapsible.length; i++) {
      var kind = collapsible[i]
      next[kind] = panelRows.some(function(entry) {
        return entry.kind === "item" && entry.section === kind
      })
    }
    return next
  }

  function open() {
    now = Date.now()
    expanded = openingExpanded()
    focusedSource = ""
    filterController.reset()
    if (service) service.refreshThumbnails()
    controller.show()
    Qt.callLater(function() {
      panelFlick.contentY = 0
      filterController.forceActiveFocus()
    })
  }

  // Opens on Twitch live channels or YouTube uploads, with the first one
  // selected and its section scrolled to the top.
  function openOn(source) {
    var wanted = source === "twitch" ? ["live", "followed"] : ["upload", "other"]
    var expand = source === "twitch" ? ["live"] : ["upload"]
    open()
    focusedSource = source
    var next = Object.assign({}, expanded)
    for (var i = 0; i < expand.length; i++) next[expand[i]] = true
    expanded = next
    Qt.callLater(function() {
      for (var j = 0; j < filteredSections.length; j++) {
        var section = filteredSections[j]
        if (wanted.indexOf(section.kind) < 0) continue
        var first = section.rows.length > 0 ? section.rows[0].key : section.toggleKey
        filterController.cursorIndex = filterController.indexForKey(first)
        var sectionItem = sectionRepeater.itemAt(j)
        if (sectionItem) {
          var point = sectionItem.mapToItem(contentColumn, 0, 0)
          panelFlick.contentY = Math.max(0, Math.min(point.y, panelFlick.contentHeight - panelFlick.height))
        }
        return
      }
    })
  }

  function toggleSource(source) {
    if (opened && focusedSource === source) close()
    else openOn(source)
  }

  function close() {
    controller.hide()
  }

  function toggle() {
    if (opened) close()
    else open()
  }

  function switchPanel(direction) {
    if (bar && typeof bar.switchPanelFrom === "function")
      return bar.switchPanelFrom(barIdentity, direction)
    return false
  }

  function cursorItem() {
    return itemForEntry(filterController.selectedEntry())
  }

  // Where the pointer last selected a row from. Qt resends hover each frame
  // when rows move under a still pointer, so scrolling or a feed update
  // would otherwise move the selection to whatever row ends up under it.
  property point hoverPoint: Qt.point(-1, -1)

  function hoverSelect(area, mouse, key) {
    hoverSelectAt(area.mapToGlobal(mouse.x, mouse.y), key)
  }

  function hoverSelectAt(point, key) {
    if (point.x === hoverPoint.x && point.y === hoverPoint.y) return
    hoverPoint = point
    filterController.cursorIndex = filterController.indexForKey(key)
  }

  function itemForEntry(entry) {
    if (!entry) return null
    if (entry.kind === "header-action") return actionsHeader
    if (entry.kind === "action") return actionRepeater.itemAt(filteredActions.indexOf(entry))
    if (entry.kind === "attention") return attentionRepeater.itemAt(filteredAttention.indexOf(entry))
    for (var i = 0; i < filteredSections.length; i++) {
      var section = filteredSections[i]
      if (section.kind !== entry.section) continue
      var sectionItem = sectionRepeater.itemAt(i)
      if (!sectionItem) return null
      if (entry.kind === "toggle" || entry.kind === "mark-all") return sectionItem.heading
      if (entry.kind === "playlist") return sectionItem.linkRow
      return sectionItem.rowAt(section.rows.indexOf(entry))
    }
    return null
  }

  function scrollCursorIntoView() {
    var item = cursorItem()
    if (!item) return
    var point = item.mapToItem(contentColumn, 0, 0)
    if (point.y < panelFlick.contentY) panelFlick.contentY = point.y
    else if (point.y + item.height > panelFlick.contentY + panelFlick.height)
      panelFlick.contentY = point.y + item.height - panelFlick.height
  }

  Timer {
    id: revealTimer
    interval: 0
    onTriggered: root.scrollCursorIntoView()
  }

  // The row at the top of the view when a feed update arrives, and how far
  // down the view it was. The view is moved back to it as the rebuilt rows
  // are laid out, rather than jumping.
  property var scrollAnchor: null

  function captureScrollAnchor() {
    scrollAnchor = null
    if (!opened) return
    var rows = navigationRows
    for (var i = 0; i < rows.length; i++) {
      var item = itemForEntry(rows[i])
      if (!item) continue
      var y = item.mapToItem(contentColumn, 0, 0).y
      if (y + item.height <= panelFlick.contentY) continue
      scrollAnchor = { key: rows[i].key, offset: y - panelFlick.contentY }
      anchorTimer.restart()
      return
    }
  }

  function restoreScrollAnchor() {
    if (!scrollAnchor) return
    var anchorKey = scrollAnchor.key
    var entry = navigationRows.find(function(row) { return row.key === anchorKey })
    var item = itemForEntry(entry)
    if (!item) return
    var y = item.mapToItem(contentColumn, 0, 0).y - scrollAnchor.offset
    panelFlick.contentY = Math.max(0, Math.min(y, panelFlick.contentHeight - panelFlick.height))
  }

  Connections {
    target: root.service
    function onItemsAboutToChange() { root.captureScrollAnchor() }
  }

  // Rows lay out over a few passes, so the anchor is kept briefly.
  Timer {
    id: anchorTimer
    interval: 250
    onTriggered: root.scrollAnchor = null
  }

  Timer {
    interval: 60000
    running: root.opened
    repeat: true
    onTriggered: {
      root.now = Date.now()
      if (root.service) root.service.refreshThumbnails()
    }
  }

  function activateAction(index) {
    if (!service) return
    if ((index === 0 || index === 1) && !service.canRecheck) return
    if (index === 0) service.recheck(false)
    else if (index === 1) {
      service.recheck(true)
      close()
    } else if (index === 4) service.restart()
    else {
      service.openUrl(actionUrls[index])
      close()
    }
  }

  // Saved items, and any YouTube video, live or not. Twitch streams can't be.
  function canMarkWatched(entry) {
    return entry.item.kind === "saved" || entry.item.source === "youtube"
  }

  // Opens the item, or with Shift marks it watched, leaving the panel open. A
  // video saved in the playlist opens in the playlist, or on its own with Ctrl.
  function activateItem(entry, markWatched, alone) {
    if (!service) return
    if (markWatched) {
      if (canMarkWatched(entry)) service.markWatched(entry.item)
      return
    }
    service.openUrl(alone && entry.item.source === "youtube"
      ? "https://www.youtube.com/watch?v=" + entry.item.id.substring("youtube:".length)
      : entry.item.url)
  }

  // What the item menu offers. Channels you don't track yet can be added. A
  // Twitch stream's URL is its channel's, so it gets no separate channel action.
  function itemActions(entry) {
    if (!entry) return []
    var actions = [{ id: "open", label: entry.item.kind === "live" ? "Watch" : "Open" }]
    if (entry.item.channel && entry.item.channel.url && entry.item.channel.url !== entry.item.url)
      actions.push({ id: "channel", label: "Open " + entry.item.channel.name })
    if (entry.tracked !== true && (entry.item.source === "twitch" || entry.item.source === "youtube"))
      actions.push({ id: "add", label: "Add " + entry.item.channel.name + " to your channels" })
    if (entry.item.source === "youtube" && entry.item.kind !== "saved")
      actions.push({ id: "save", label: "Watch later" })
    if (canMarkWatched(entry)) actions.push({ id: "watched", label: "Mark as watched" })
    return actions
  }

  signal itemMenuRequested(string key)

  function openItemMenu(entry, anchor, x, y) {
    if (!service) return
    menuEntry = entry
    var point = anchor.mapToItem(filterController, x, y)
    itemMenu.x = Math.max(0, Math.min(point.x, filterController.width - itemMenu.width))
    itemMenu.y = Math.max(0, Math.min(point.y, filterController.height - itemMenu.implicitHeight))
    itemMenu.open()
  }

  function runItemAction(action) {
    var entry = menuEntry
    itemMenu.close()
    if (!entry || !service) return
    if (action === "open") activateItem(entry, false)
    else if (action === "channel") service.openUrl(entry.item.channel.url)
    else if (action === "add") service.addChannel(entry.item)
    else if (action === "save") service.watchLater(entry.item)
    else if (action === "watched") service.markWatched(entry.item)
  }

  function activateAttention(status) {
    if (!service || status.state !== "auth-required") return
    service.signIn(status.source)
    close()
  }

  function openPlaylist(playlist) {
    if (!service) return
    service.openUrl(playlist.url)
  }

  function activateEntry(entry, modifiers) {
    if (entry.kind === "action" || entry.kind === "header-action") activateAction(entry.actionIndex)
    else if (entry.kind === "attention") activateAttention(entry.value)
    else if (entry.kind === "item")
      activateItem(entry.value, (modifiers & Qt.ShiftModifier) !== 0, (modifiers & Qt.ControlModifier) !== 0)
    else if (entry.kind === "playlist") openPlaylist(entry.value)
    else if (entry.kind === "toggle") toggleSection(entry.section)
    else if (entry.kind === "mark-all") markAllWatched(entry.section)
  }

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.barIdentity
    bar: root.bar
    open: root.opened
    focusTarget: filterController
    contentWidth: panel.fittedContentWidth(Style.space(560))
    contentHeight: panel.fittedContentHeight(contentColumn.implicitHeight, panel.screenH * 0.8)

    FilterablePanel {
      id: filterController
      anchors.fill: parent
      model: root.panelRows
      navigationModel: root.navigationRows
      // A feed update keeps the view where it is instead of jumping to the
      // selected row.
      onRevealRequested: if (!root.scrollAnchor) revealTimer.restart()
      onActivateRequested: function(entry, modifiers) { root.activateEntry(entry, modifiers) }
      onMenuRequested: function(entry) { if (entry.kind === "item") root.itemMenuRequested(entry.key) }
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onRefreshRequested: root.activateAction(0)

      PanelFlickable {
        id: panelFlick
        anchors.fill: parent
        contentWidth: width
        contentHeight: contentColumn.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        flickableDirection: Flickable.VerticalFlick
        interactive: contentHeight > height
        ScrollBar.vertical: ScrollBar { policy: ScrollBar.AsNeeded }

        Column {
          id: contentColumn
          width: panelFlick.width
          spacing: Style.space(12)
          onPositioningComplete: root.restoreScrollAnchor()

          PanelHeader {
            title: "Up Next"
            meta: root.service && root.service.restarting ? "Restarting Up Next"
              : (!root.service || root.service.statusState === "inactive"
                ? "Up Next is unavailable"
                : root.service.issues.concat([root.service.summary]).join(" · "))
            detail: root.service && root.service.restarting ? "RESTARTING"
              : (!root.service || !root.service.connected ? "OFFLINE"
                : (root.service.issues.length > 0 ? "ATTENTION" : "CONNECTED"))
            foreground: root.contentForeground
            fontFamily: root.contentFontFamily
            iconOpacity: root.service && root.service.connected ? 1 : 0.5
            iconComponent: Component {
              Image {
                source: Qt.resolvedUrl("logo.svg")
                width: Style.font.display * 1.4
                height: width
                sourceSize.width: width * 2
                sourceSize.height: height * 2
                fillMode: Image.PreserveAspectFit
                smooth: true
              }
            }
          }

          SectionHeading {
            id: actionsHeader
            visible: root.filteredHeaderActions.length > 0 || root.filteredActions.length > 0
            title: filterController.filterText || "Actions"
            foreground: root.contentForeground
            fontFamily: root.contentFontFamily
            trailingControl: Component {
              Row {
                spacing: Style.space(4)

                Repeater {
                  model: root.filteredHeaderActions

                  PanelActionButton {
                    required property var modelData
                    enabled: root.service && (modelData.actionIndex === 4
                      ? !root.service.actionBusy : root.service.canRecheck)
                    iconText: root.actionIcons[modelData.actionIndex]
                    tooltipText: modelData.actionIndex === 4 && root.service && root.service.restarting
                      ? "Restarting Up Next" : modelData.primaryText
                    foreground: modelData.actionIndex === 4
                      ? (root.bar ? root.bar.urgent : Commons.Color.urgent)
                      : Qt.darker(root.contentForeground, 1.15)
                    fontFamily: root.contentFontFamily
                    hasCursor: filterController.cursorIndex === filterController.indexForKey(modelData.key)
                    onHovered: function(hovered) {
                      if (hovered) filterController.cursorIndex = filterController.indexForKey(modelData.key)
                    }
                    onClicked: root.activateAction(modelData.actionIndex)
                  }
                }
              }
            }
          }

          Column {
            width: parent.width
            spacing: Style.space(2)

            Repeater {
              id: actionRepeater
              model: root.filteredActions

              CursorSurface {
                required property var modelData
                width: contentColumn.width
                implicitHeight: actionRow.implicitHeight + Style.space(12)
                hasCursor: filterController.cursorIndex === filterController.indexForKey(modelData.key)
                foreground: root.contentForeground
                accent: root.contentForeground

                Row {
                  id: actionRow
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.leftMargin: Style.space(8)
                  anchors.rightMargin: Style.space(8)
                  spacing: Style.space(10)

                  Text {
                    width: Style.space(22)
                    text: root.actionIcons[modelData.actionIndex]
                    color: root.contentForeground
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.icon
                    horizontalAlignment: Text.AlignHCenter
                  }

                  Text {
                    width: Math.max(0, actionRow.width - Style.space(32))
                    text: root.actionLabels[modelData.actionIndex]
                    color: root.contentForeground
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.body
                    elide: Text.ElideRight
                  }
                }

                MouseArea {
                  id: actionHover
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: Qt.PointingHandCursor
                  onPositionChanged: function(mouse) { root.hoverSelect(actionHover, mouse, modelData.key) }
                  onClicked: root.activateAction(modelData.actionIndex)
                }
              }
            }
          }

          SectionHeading {
            visible: root.filteredAttention.length > 0
            title: "NEEDS ATTENTION"
            foreground: root.bar ? root.bar.urgent : Commons.Color.urgent
            fontFamily: root.contentFontFamily
          }

          Column {
            width: parent.width
            spacing: Style.space(2)

            Repeater {
              id: attentionRepeater
              model: root.filteredAttention

              CursorSurface {
                required property var modelData
                width: contentColumn.width
                implicitHeight: attentionColumn.implicitHeight + Style.space(12)
                hasCursor: filterController.cursorIndex === filterController.indexForKey(modelData.key)
                foreground: root.contentForeground
                accent: root.bar ? root.bar.urgent : Commons.Color.urgent

                Row {
                  anchors.left: parent.left
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  anchors.leftMargin: Style.space(8)
                  anchors.rightMargin: Style.space(8)
                  spacing: Style.space(10)

                  Text {
                    width: Style.space(22)
                    text: root.sourceIcon(modelData.value.source)
                    color: root.sourceColor(modelData.value.source)
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.icon
                    horizontalAlignment: Text.AlignHCenter
                  }

                  Column {
                    id: attentionColumn
                    width: Math.max(0, parent.width - Style.space(32))
                    spacing: Style.space(2)

                    Text {
                      width: parent.width
                      text: modelData.value.state === "auth-required"
                        ? "Sign in to " + modelData.value.source
                        : modelData.value.source + " failed"
                      color: root.contentForeground
                      font.family: root.contentFontFamily
                      font.pixelSize: Style.font.body
                      elide: Text.ElideRight
                    }

                    Text {
                      width: parent.width
                      visible: text !== ""
                      text: String(modelData.value.message || "")
                      color: Qt.darker(root.contentForeground, 1.4)
                      font.family: root.contentFontFamily
                      font.pixelSize: Style.font.caption
                      elide: Text.ElideRight
                    }
                  }
                }

                MouseArea {
                  id: sourceHover
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: modelData.value.state === "auth-required" ? Qt.PointingHandCursor : Qt.ArrowCursor
                  onPositionChanged: function(mouse) { root.hoverSelect(sourceHover, mouse, modelData.key) }
                  onClicked: root.activateAttention(modelData.value)
                }
              }
            }
          }

          Repeater {
            id: sectionRepeater
            model: root.filteredSections

            Column {
              id: sectionColumn
              required property var modelData
              readonly property alias heading: sectionHeading
              readonly property alias linkRow: sectionLink
              width: contentColumn.width
              spacing: Style.space(4)

              function rowAt(index) {
                return rowRepeater.itemAt(index)
              }

              SectionHeading {
                id: sectionHeading
                width: parent.width
                title: sectionColumn.modelData.title
                  + (sectionColumn.modelData.loading ? "" : " · " + sectionColumn.modelData.count)
                  + (filterController.filterText ? " MATCHING" : "")
                foreground: root.contentForeground
                fontFamily: root.contentFontFamily
                collapsible: !filterController.filterText
                expanded: root.expanded[sectionColumn.modelData.kind] === true
                toggleHasCursor: filterController.cursorIndex === filterController.indexForKey(sectionColumn.modelData.toggleKey)
                onToggleHovered: function(point) { root.hoverSelectAt(point, sectionColumn.modelData.toggleKey) }
                onToggleRequested: root.toggleSection(sectionColumn.modelData.kind)
                trailingControl: sectionColumn.modelData.markable.length > 0 ? markAllButton : null

                Component {
                  id: markAllButton
                  PanelActionButton {
                    iconText: "󰄬"
                    tooltipText: "Mark all " + sectionColumn.modelData.markable.length
                      + (filterController.filterText ? " matching" : "") + " as watched"
                    foreground: root.contentForeground
                    fontFamily: root.contentFontFamily
                    hasCursor: filterController.cursorIndex === filterController.indexForKey(sectionColumn.modelData.markAllKey)
                    onHovered: function(hovered) {
                      if (hovered) filterController.cursorIndex = filterController.indexForKey(sectionColumn.modelData.markAllKey)
                    }
                    onClicked: root.markAllWatched(sectionColumn.modelData.kind)
                  }
                }
              }

              Column {
                width: parent.width
                spacing: Style.space(2)

                Repeater {
                  id: rowRepeater
                  model: sectionColumn.modelData.rows
                  delegate: itemDelegate
                }

                Text {
                  visible: sectionColumn.modelData.count === 0 && root.expanded[sectionColumn.modelData.kind] === true
                  width: parent.width
                  topPadding: Style.space(6)
                  bottomPadding: Style.space(6)
                  leftPadding: Style.space(12)
                  text: sectionColumn.modelData.emptyText
                  color: Qt.darker(root.contentForeground, 1.4)
                  font.family: root.contentFontFamily
                  font.pixelSize: Style.font.caption
                  font.italic: sectionColumn.modelData.loading
                }

                CursorSurface {
                  id: sectionLink
                  readonly property var link: sectionColumn.modelData.link
                  visible: link !== null
                  width: parent.width
                  implicitHeight: linkRow.implicitHeight + Style.space(12)
                  hasCursor: link !== null && filterController.cursorIndex === filterController.indexForKey(link.key)
                  foreground: root.contentForeground
                  accent: root.contentForeground

                  Row {
                    id: linkRow
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    anchors.leftMargin: Style.space(8)
                    anchors.rightMargin: Style.space(8)
                    spacing: Style.space(10)

                    Text {
                      width: Style.space(22)
                      text: root.sourceIcon("youtube")
                      color: root.sourceColor("youtube")
                      font.family: root.contentFontFamily
                      font.pixelSize: Style.font.icon
                      horizontalAlignment: Text.AlignHCenter
                    }

                    Text {
                      width: Math.max(0, linkRow.width - Style.space(32))
                      text: sectionLink.link ? String(sectionLink.link.primaryText) : ""
                      color: root.contentForeground
                      font.family: root.contentFontFamily
                      font.pixelSize: Style.font.body
                      elide: Text.ElideRight
                    }
                  }

                  MouseArea {
                    id: linkHover
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onPositionChanged: function(mouse) {
                      if (sectionLink.link) root.hoverSelect(linkHover, mouse, sectionLink.link.key)
                    }
                    onClicked: if (sectionLink.link) root.openPlaylist(sectionLink.link.value)
                  }
                }
              }
            }
          }

          Component {
            id: itemDelegate

            CursorSurface {
              id: itemSurface
              required property var modelData
              // The latest feed's copy, so viewer counts and auto-open
              // changes show without the row being rebuilt.
              readonly property var entry: (root.service && root.service.entriesById[modelData.value.item.id]) || modelData.value
              readonly property var item: entry.item
              readonly property bool live: item.kind === "live"
              readonly property var thumbnail: root.service ? root.service.thumbnailFor(item) : null
              readonly property bool markable: root.canMarkWatched(entry)
              width: contentColumn.width
              implicitHeight: itemColumn.implicitHeight + Style.space(12)
              hasCursor: filterController.cursorIndex === filterController.indexForKey(modelData.key)
              foreground: root.contentForeground
              accent: live ? root.sourceColor(item.source) : root.contentForeground

              Row {
                id: itemRow
                // Above the row's MouseArea, so the watched button gets its
                // clicks. Everything else in it lets clicks through.
                z: 1
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                anchors.leftMargin: Style.space(8)
                anchors.rightMargin: Style.space(8)
                spacing: Style.space(10)

                Item {
                  width: Style.space(64)
                  height: Math.round(width * 9 / 16)
                  clip: true

                  Rectangle {
                    anchors.fill: parent
                    color: Qt.darker(root.contentForeground, itemSurface.live ? 2.2 : 2.5)
                    radius: Style.space(4)
                  }

                  Image {
                    id: itemThumbnail
                    anchors.fill: parent
                    source: itemSurface.thumbnail ? itemSurface.thumbnail.source : ""
                    // The same width the service decodes at, so this shares
                    // its image instead of decoding the full size.
                    sourceSize.width: root.service ? root.service.thumbnailWidth : 160
                    asynchronous: true
                    cache: true
                    fillMode: Image.PreserveAspectCrop
                  }

                  Text {
                    anchors.fill: parent
                    visible: itemThumbnail.status !== Image.Ready
                    text: root.sourceIcon(itemSurface.item.source)
                    color: root.sourceColor(itemSurface.item.source)
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.icon
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                  }
                }

                Column {
                  id: itemColumn
                  width: Math.max(0, parent.width - Style.space(124) - (itemSurface.markable ? watchedButton.width + itemRow.spacing : 0))
                  spacing: Style.space(2)

                  Text {
                    width: parent.width
                    text: String(itemSurface.modelData.primaryText || "")
                    color: root.contentForeground
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.body
                    font.bold: itemSurface.live && itemSurface.entry.tracked
                    elide: Text.ElideRight
                  }

                  Text {
                    width: parent.width
                    visible: text !== ""
                    text: String(itemSurface.modelData.secondaryText || "")
                    color: Qt.darker(root.contentForeground, 1.4)
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.caption
                    elide: Text.ElideRight
                  }

                  Text {
                    width: parent.width
                    visible: text !== ""
                    text: root.detailFor(itemSurface.entry, root.now)
                    color: Qt.darker(root.contentForeground, 1.25)
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.caption
                    elide: Text.ElideRight
                  }
                }

                Text {
                  width: Style.space(20)
                  text: itemSurface.entry.autoOpen === true ? "󰉁" : root.sourceIcon(itemSurface.item.source)
                  color: itemSurface.entry.autoOpen === true
                    ? Qt.darker(root.contentForeground, 1.3)
                    : root.sourceColor(itemSurface.item.source)
                  font.family: root.contentFontFamily
                  font.pixelSize: Style.font.caption
                  horizontalAlignment: Text.AlignHCenter
                }

                PanelActionButton {
                  id: watchedButton
                  visible: itemSurface.markable
                  anchors.verticalCenter: parent.verticalCenter
                  iconText: "󰄬"
                  tooltipText: "Mark as watched"
                  foreground: root.contentForeground
                  fontFamily: root.contentFontFamily
                  onClicked: if (root.service) root.service.markWatched(itemSurface.item)
                }
              }

              MouseArea {
                id: itemHover
                anchors.fill: parent
                hoverEnabled: true
                acceptedButtons: Qt.LeftButton | Qt.RightButton
                cursorShape: Qt.PointingHandCursor
                onPositionChanged: function(mouse) { root.hoverSelect(itemHover, mouse, itemSurface.modelData.key) }
                onClicked: function(mouse) {
                  if (mouse.button === Qt.RightButton)
                    root.openItemMenu(itemSurface.entry, itemSurface, mouse.x, mouse.y)
                  else root.activateItem(itemSurface.entry, false, (mouse.modifiers & Qt.ControlModifier) !== 0)
                }
              }

              Connections {
                target: root
                function onItemMenuRequested(key) {
                  if (key === itemSurface.modelData.key)
                    root.openItemMenu(itemSurface.entry, itemSurface, Style.space(80), itemSurface.height)
                }
              }
            }
          }

          Text {
            visible: filterController.count === 0 || (root.filteredSections.length === 0 && !filterController.filterText)
            width: parent.width
            text: filterController.filterText
              ? "No matches for “" + filterController.filterText + "”"
              : (root.service && root.service.errorText !== "" ? root.service.errorText : "Nothing to watch")
            color: Qt.darker(root.contentForeground, 1.4)
            font.family: root.contentFontFamily
            font.pixelSize: Style.font.body
            horizontalAlignment: Text.AlignHCenter
          }
        }
      }

      Popup {
        id: itemMenu
        readonly property var menuBorderSpec: Border.localOrSurfaceSpec("popups", "border", Commons.Color.popups.border, Commons.Color.popups.border, Style.normalBorderWidth)
        width: Style.space(260)
        implicitHeight: itemMenu.contentItem.contentHeight + topPadding + bottomPadding
        padding: Style.spacing.hairline
        leftPadding: Border.left(menuBorderSpec) + Style.spacing.hairline
        rightPadding: Border.right(menuBorderSpec) + Style.spacing.hairline
        topPadding: Border.top(menuBorderSpec) + Style.spacing.hairline
        bottomPadding: Border.bottom(menuBorderSpec) + Style.spacing.hairline
        focus: true

        background: BorderSurface {
          color: Commons.Color.popups.background
          borderSpec: itemMenu.menuBorderSpec
          radius: Style.cornerRadius
        }

        onOpened: {
          itemMenu.contentItem.currentIndex = 0
          itemMenu.contentItem.forceActiveFocus()
        }
        onClosed: {
          root.menuEntry = null
          filterController.forceActiveFocus()
        }

        // No id here: an id on a Popup's contentItem makes qmllint hang, so
        // the list is reached through itemMenu.contentItem.
        contentItem: ListView {
          implicitHeight: contentHeight
          interactive: false
          model: root.itemActions(root.menuEntry)
          currentIndex: 0

          Keys.priority: Keys.BeforeItem
          Keys.onPressed: function(event) {
            if (event.key === Qt.Key_Escape || event.key === Qt.Key_Menu) {
              itemMenu.close()
              event.accepted = true
            } else if (event.key === Qt.Key_Down || event.text === "j") {
              itemMenu.contentItem.currentIndex = Math.min(itemMenu.contentItem.count - 1, itemMenu.contentItem.currentIndex + 1)
              event.accepted = true
            } else if (event.key === Qt.Key_Up || event.text === "k") {
              itemMenu.contentItem.currentIndex = Math.max(0, itemMenu.contentItem.currentIndex - 1)
              event.accepted = true
            } else if (event.key === Qt.Key_Return || event.key === Qt.Key_Enter) {
              if (itemMenu.contentItem.currentIndex >= 0) root.runItemAction(itemMenu.contentItem.model[itemMenu.contentItem.currentIndex].id)
              event.accepted = true
            }
          }

          delegate: Rectangle {
            required property var modelData
            required property int index
            width: itemMenu.contentItem.width
            height: Style.spacing.popupRowHeight
            color: index === itemMenu.contentItem.currentIndex
              ? Style.hoverFillFor(root.contentForeground, root.contentForeground)
              : "transparent"

            Text {
              textFormat: Text.PlainText
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              anchors.leftMargin: Style.spacing.controlPaddingX
              anchors.rightMargin: Style.spacing.controlPaddingX
              text: parent.modelData.label
              color: root.contentForeground
              font.family: root.contentFontFamily
              font.pixelSize: Style.font.body
              elide: Text.ElideRight
            }

            MouseArea {
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onPositionChanged: itemMenu.contentItem.currentIndex = parent.index
              onClicked: root.runItemAction(parent.modelData.id)
            }
          }
        }
      }
    }
  }
}
