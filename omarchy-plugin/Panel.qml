import QtQuick
import QtQuick.Controls
import Quickshell
import qs.Commons
import qs.Ui

Panel {
  id: root
  moduleName: "timmo.twitch"
  ipcTarget: "timmo.twitch"
  manageIpc: false

  property var anchorItem: null
  property var hostWidget: null
  property var service: null

  readonly property var barIdentity: hostWidget || root
  readonly property color contentForeground: bar ? bar.foreground : Color.foreground
  readonly property string contentFontFamily: bar ? bar.fontFamily : Style.font.family
  readonly property int actionCount: 5
  readonly property var actionLabels: [
    "Recheck notifications",
    "Open all live autolaunch",
    "Open following",
    "Open following live",
    "Restart notifications"
  ]
  readonly property var actionIcons: ["", "󰕃", "", "", "󰜉"]
  readonly property var panelRows: buildPanelRows()
  readonly property var filteredActions: filterRows("action")
  readonly property var filteredChannels: filterRows("channel")
  readonly property var filteredFollowedChannels: filterRows("followed")
  readonly property var filteredLiveChannels: filteredChannels.filter(function(entry) { return entry.value.live === true })
  readonly property var filteredOfflineChannels: filteredChannels.filter(function(entry) { return entry.value.live !== true })
  readonly property var visibleOfflineChannels: filterController.filterText || offlineExpanded ? filteredOfflineChannels : []
  readonly property var visibleFollowedChannels: filterController.filterText || followedExpanded ? filteredFollowedChannels : []
  readonly property var navigationRows: buildNavigationRows()
  property bool offlineExpanded: false
  property bool followedExpanded: false

  function buildPanelRows() {
    var rows = []
    for (var i = 0; i < actionCount; i++) {
      rows.push({
        key: "action:" + i,
        kind: "action",
        section: "action",
        actionIndex: i,
        primaryText: actionLabels[i],
        secondaryText: ""
      })
    }
    var channels = service ? service.channels : []
    for (var j = 0; j < channels.length; j++) {
      var channel = channels[j]
      rows.push({
        key: "channel:" + String(channel.login || j),
        kind: "channel",
        section: "channel",
        value: channel,
        primaryText: channel.login,
        secondaryText: channel.title
      })
    }
    var followed = service ? service.followedLive : []
    for (var k = 0; k < followed.length; k++) {
      var followedChannel = followed[k]
      rows.push({
        key: "followed:" + String(followedChannel.login || k),
        kind: "followed",
        section: "followed",
        value: followedChannel,
        primaryText: followedChannel.login,
        secondaryText: followedChannel.title
      })
    }
    return rows
  }

  function filterRows(kind) {
    return filterController.filteredModel.filter(function(entry) { return entry.kind === kind })
  }

  function buildNavigationRows() {
    var rows = filteredActions.concat(filteredLiveChannels)
    if (!filterController.filterText && filteredFollowedChannels.length > 0)
      rows.push({ key: "toggle:followed", kind: "toggle-followed" })
    rows = rows.concat(visibleFollowedChannels)
    if (!filterController.filterText && filteredOfflineChannels.length > 0)
      rows.push({ key: "toggle:offline", kind: "toggle-offline" })
    return rows.concat(visibleOfflineChannels)
  }

  function open() {
    offlineExpanded = false
    followedExpanded = false
    filterController.reset()
    if (service) {
      service.refresh()
      service.refreshFollowedLive()
    }
    controller.show()
    Qt.callLater(function() {
      panelFlick.contentY = 0
      filterController.forceActiveFocus()
    })
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
    var entry = filterController.selectedEntry()
    if (!entry) return null
    var rows = filteredActions
    var repeater = actionRepeater
    if (entry.kind === "channel" && entry.value.live === true) {
      rows = filteredLiveChannels
      repeater = liveChannelRepeater
    } else if (entry.kind === "channel") {
      rows = visibleOfflineChannels
      repeater = offlineChannelRepeater
    } else if (entry.kind === "followed") {
      rows = visibleFollowedChannels
      repeater = followedChannelRepeater
    } else if (entry.kind === "toggle-offline") return offlineHeader
    else if (entry.kind === "toggle-followed") return followedHeader
    return repeater.itemAt(rows.indexOf(entry))
  }

  function scrollCursorIntoView() {
    var item = cursorItem()
    if (!item) return
    var point = item.mapToItem(contentColumn, 0, 0)
    if (point.y < panelFlick.contentY) panelFlick.contentY = point.y
    else if (point.y + item.height > panelFlick.contentY + panelFlick.height)
      panelFlick.contentY = point.y + item.height - panelFlick.height
  }

  function activateAction(index) {
    if (!service) return
    if (index === 0) service.recheck(false)
    else if (index === 1) {
      service.recheck(true)
      close()
    } else if (index === 2) {
      service.openFollowing()
      close()
    } else if (index === 3) {
      service.openFollowingLive()
      close()
    }
    else if (index === 4) service.restart()
  }

  function activateChannel(channel) {
    if (!service) return
    service.openChannel(channel)
    close()
  }

  function activateEntry(entry) {
    if (entry.kind === "action") activateAction(entry.actionIndex)
    else if (entry.kind === "channel" || entry.kind === "followed") activateChannel(entry.value)
    else if (entry.kind === "toggle-offline") offlineExpanded = !offlineExpanded
    else if (entry.kind === "toggle-followed") followedExpanded = !followedExpanded
  }

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.barIdentity
    bar: root.bar
    open: root.opened
    focusTarget: filterController
    contentWidth: panel.fittedContentWidth(Style.space(430))
    contentHeight: panel.fittedContentHeight(contentColumn.implicitHeight, Style.space(670))

    FilterablePanel {
      id: filterController
      anchors.fill: parent
      model: root.panelRows
      navigationModel: root.navigationRows
      onRevealRequested: Qt.callLater(root.scrollCursorIntoView)
      onActivateRequested: function(entry) { root.activateEntry(entry) }
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onRefreshRequested: root.activateAction(0)

      Flickable {
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

          PanelHero {
            width: parent.width
            title: "Twitch"
            meta: !root.service || root.service.statusState === "inactive"
              ? "Notifications unavailable"
              : (root.service.statusState === "live"
                ? root.service.liveCount + " live now"
                : "No channels live")
            detail: root.service && root.service.active ? "ACTIVE" : "OFFLINE"
            foreground: root.contentForeground
            fontFamily: root.contentFontFamily
            iconOpacity: root.service && root.service.active ? 1 : 0.5
            iconComponent: Component {
              Text {
                text: ""
                color: root.service && root.service.statusState === "live" ? "#ac77e5" : root.contentForeground
                font.family: root.contentFontFamily
                font.pixelSize: Style.font.display
              }
            }
          }

          Text {
            text: filterController.filterText || "ACTIONS"
            color: Qt.darker(root.contentForeground, 1.4)
            font.family: root.contentFontFamily
            font.pixelSize: Style.font.caption
            font.bold: true
            font.letterSpacing: 1.2
          }

          Column {
            width: parent.width
            spacing: Style.space(2)

            Repeater {
              id: actionRepeater
              model: root.filteredActions

              CursorSurface {
                required property int index
                required property var modelData
                width: contentColumn.width
                implicitHeight: actionRow.implicitHeight + Style.space(12)
                hasCursor: filterController.cursorIndex === filterController.indexForKey(modelData.key)
                foreground: root.contentForeground
                accent: modelData.actionIndex === 4 && root.bar ? root.bar.urgent : root.contentForeground

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
                    color: modelData.actionIndex === 4 && root.bar ? root.bar.urgent : root.contentForeground
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
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: Qt.PointingHandCursor
                  onEntered: filterController.cursorIndex = filterController.indexForKey(modelData.key)
                  onClicked: root.activateAction(modelData.actionIndex)
                }
              }
            }
          }

          Text {
            visible: root.filteredLiveChannels.length > 0
            text: filterController.filterText
              ? "LIVE · " + root.filteredLiveChannels.length + " MATCHING"
              : "LIVE · " + root.filteredLiveChannels.length
            color: Qt.darker(root.contentForeground, 1.4)
            font.family: root.contentFontFamily
            font.pixelSize: Style.font.caption
            font.bold: true
            font.letterSpacing: 1.2
          }

          Column {
            width: parent.width
            spacing: Style.space(2)

            Repeater {
              id: liveChannelRepeater
              model: root.filteredLiveChannels
              delegate: channelDelegate
            }
          }

          CursorSurface {
            id: followedHeader
            width: parent.width
            visible: root.filteredFollowedChannels.length > 0
            implicitHeight: followedHeaderRow.implicitHeight + Style.space(8)
            hasCursor: filterController.cursorIndex === filterController.indexForKey("toggle:followed")
            foreground: root.contentForeground
            accent: root.contentForeground

            Row {
              id: followedHeaderRow
              anchors.left: parent.left
              anchors.verticalCenter: parent.verticalCenter
              anchors.leftMargin: Style.space(8)
              spacing: Style.space(6)

              Text {
                text: filterController.filterText || root.followedExpanded ? "󰅀" : "󰅂"
                color: Qt.darker(root.contentForeground, 1.4)
                font.family: root.contentFontFamily
                font.pixelSize: Style.font.caption
              }

              Text {
                text: filterController.filterText
                  ? "OTHER LIVE · " + root.filteredFollowedChannels.length + " MATCHING"
                  : "OTHER LIVE · " + root.filteredFollowedChannels.length
                color: Qt.darker(root.contentForeground, 1.4)
                font.family: root.contentFontFamily
                font.pixelSize: Style.font.caption
                font.bold: true
                font.letterSpacing: 1.2
              }
            }

            MouseArea {
              anchors.fill: parent
              enabled: !filterController.filterText
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onEntered: filterController.cursorIndex = filterController.indexForKey("toggle:followed")
              onClicked: root.activateEntry({ kind: "toggle-followed" })
            }
          }

          Column {
            width: parent.width
            spacing: Style.space(2)

            Repeater {
              id: followedChannelRepeater
              model: root.visibleFollowedChannels
              delegate: channelDelegate
            }
          }

          CursorSurface {
            id: offlineHeader
            width: parent.width
            visible: root.filteredOfflineChannels.length > 0
            implicitHeight: offlineHeaderRow.implicitHeight + Style.space(8)
            hasCursor: filterController.cursorIndex === filterController.indexForKey("toggle:offline")
            foreground: root.contentForeground
            accent: root.contentForeground

            Row {
              id: offlineHeaderRow
              anchors.left: parent.left
              anchors.verticalCenter: parent.verticalCenter
              anchors.leftMargin: Style.space(8)
              spacing: Style.space(6)

              Text {
                text: filterController.filterText || root.offlineExpanded ? "󰅀" : "󰅂"
                color: Qt.darker(root.contentForeground, 1.4)
                font.family: root.contentFontFamily
                font.pixelSize: Style.font.caption
              }

              Text {
                text: filterController.filterText
                  ? "OFFLINE · " + root.filteredOfflineChannels.length + " MATCHING"
                  : "OFFLINE · " + root.filteredOfflineChannels.length
                color: Qt.darker(root.contentForeground, 1.4)
                font.family: root.contentFontFamily
                font.pixelSize: Style.font.caption
                font.bold: true
                font.letterSpacing: 1.2
              }
            }

            MouseArea {
              anchors.fill: parent
              enabled: !filterController.filterText
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onEntered: filterController.cursorIndex = filterController.indexForKey("toggle:offline")
              onClicked: root.activateEntry({ kind: "toggle-offline" })
            }
          }

          Column {
            width: parent.width
            spacing: Style.space(2)

            Repeater {
              id: offlineChannelRepeater
              model: root.visibleOfflineChannels
              delegate: channelDelegate
            }
          }

          Component {
            id: channelDelegate

            CursorSurface {
              id: channelSurface
              required property int index
              required property var modelData
              readonly property bool hasThumbnail: modelData.value.live === true
                && String(modelData.value.thumbnailUrl || "") !== ""
              width: contentColumn.width
              implicitHeight: channelColumn.implicitHeight + Style.space(12)
              hasCursor: filterController.cursorIndex === filterController.indexForKey(modelData.key)
              foreground: root.contentForeground
              accent: modelData.value.live === true ? "#ac77e5" : root.contentForeground

              Row {
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
                    color: modelData.value.live === true
                      ? Qt.darker(root.contentForeground, 2.2)
                      : Qt.darker(root.contentForeground, 2.5)
                    radius: Style.space(4)
                  }

                  Image {
                    id: channelThumbnail
                    anchors.fill: parent
                    visible: channelSurface.hasThumbnail
                    source: channelSurface.hasThumbnail ? String(modelData.value.thumbnailUrl) : ""
                    asynchronous: true
                    cache: false
                    fillMode: Image.PreserveAspectCrop
                  }

                  Text {
                    id: channelIcon
                    anchors.fill: parent
                    visible: !channelSurface.hasThumbnail || channelThumbnail.status !== Image.Ready
                    text: modelData.value.live === true ? "" : "󰖪"
                    color: modelData.value.live === true ? "#ac77e5" : Qt.darker(root.contentForeground, 1.5)
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.icon
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                  }
                }

                Column {
                  id: channelColumn
                  width: Math.max(0, parent.width - Style.space(104))
                  spacing: Style.space(2)

                  Text {
                    width: parent.width
                    text: String(modelData.value.login || "")
                    color: root.contentForeground
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.body
                    font.bold: modelData.value.live === true
                    elide: Text.ElideRight
                  }

                  Text {
                    width: parent.width
                    visible: text !== ""
                    text: modelData.value.live === true ? String(modelData.value.title || "") : "Offline · open recent broadcasts"
                    color: Qt.darker(root.contentForeground, 1.4)
                    font.family: root.contentFontFamily
                    font.pixelSize: Style.font.caption
                    elide: Text.ElideRight
                  }
                }

                Text {
                  width: Style.space(20)
                  visible: modelData.value.autoOpen === true
                  text: "󰋺"
                  color: Qt.darker(root.contentForeground, 1.3)
                  font.family: root.contentFontFamily
                  font.pixelSize: Style.font.caption
                  horizontalAlignment: Text.AlignHCenter
                }
              }

              MouseArea {
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onEntered: filterController.cursorIndex = filterController.indexForKey(modelData.key)
                onClicked: root.activateChannel(modelData.value)
              }
            }
          }

          Text {
            visible: filterController.count === 0
            width: parent.width
            text: filterController.filterText
              ? "No matches for “" + filterController.filterText + "”"
              : (root.service && root.service.errorText !== "" ? root.service.errorText : "No configured channels")
            color: Qt.darker(root.contentForeground, 1.4)
            font.family: root.contentFontFamily
            font.pixelSize: Style.font.body
            horizontalAlignment: Text.AlignHCenter
          }
        }
      }
    }
  }
}
