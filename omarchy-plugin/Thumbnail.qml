import QtQuick

Item {
  id: root

  property string thumbnailUrl: ""
  property url source: ""
  property double lastRefresh: 0
  property bool firstCurrent: false

  visible: false
  onThumbnailUrlChanged: refresh()

  function refresh() {
    if (!thumbnailUrl) return
    var next = firstCurrent ? second : first
    if (next.status === Image.Loading) return
    lastRefresh = Math.max(Date.now(), lastRefresh + 1)
    next.source = thumbnailUrl + (thumbnailUrl.indexOf("?") >= 0 ? "&" : "?")
      + "t=" + lastRefresh
  }

  function loaded(image) {
    if (image.status !== Image.Ready) return
    // Keep the successful image alive so rebuilt rows share its decoded cache.
    source = image.source
    firstCurrent = image === first
    var previous = firstCurrent ? second : first
    previous.source = ""
  }

  Image {
    id: first
    asynchronous: true
    cache: true
    onStatusChanged: root.loaded(first)
  }

  Image {
    id: second
    asynchronous: true
    cache: true
    onStatusChanged: root.loaded(second)
  }
}
