import QtQuick
import qs.Commons

Rectangle {
  id: root

  required property string title
  property color foreground: Color.foreground
  property string fontFamily: Style.font.family
  property bool hasCursor: false
  property Component trailingControl: null

  width: parent.width
  implicitHeight: Math.max(titleText.implicitHeight, trailingLoader.implicitHeight,
    Style.space(22), Style.font.icon + Style.spacing.sm * 2) + Style.space(12)
  radius: 0
  color: hasCursor ? Style.hoverFillFor(foreground, foreground) : Qt.rgba(foreground.r, foreground.g, foreground.b, 0.06)
  border.width: 1
  border.color: Qt.rgba(foreground.r, foreground.g, foreground.b, hasCursor ? 0.4 : 0.16)

  Text {
    id: titleText
    anchors.left: parent.left
    anchors.leftMargin: Style.space(12)
    anchors.right: trailingLoader.item && trailingLoader.item.visible ? trailingLoader.left : parent.right
    anchors.rightMargin: Style.space(12)
    anchors.verticalCenter: parent.verticalCenter
    text: root.title.toUpperCase()
    textFormat: Text.PlainText
    elide: Text.ElideRight
    color: Qt.darker(root.foreground, 1.15)
    font.family: root.fontFamily
    font.pixelSize: Style.font.caption
    font.bold: true
    font.letterSpacing: 1.2
  }

  Loader {
    id: trailingLoader
    sourceComponent: root.trailingControl
    anchors.right: parent.right
    anchors.rightMargin: Style.space(8)
    anchors.verticalCenter: parent.verticalCenter
  }
}
