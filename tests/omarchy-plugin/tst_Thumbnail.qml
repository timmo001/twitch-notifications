import QtQuick
import QtTest
import "../../omarchy-plugin" as Twitch

TestCase {
  name: "Thumbnail"

  Component {
    id: thumbnailComponent
    Twitch.Thumbnail {}
  }

  Component {
    id: rowImageComponent
    Image { cache: true; asynchronous: true }
  }

  function test_refreshKeepsLastGoodImage() {
    var thumbnail = createTemporaryObject(thumbnailComponent, this)
    thumbnail.thumbnailUrl = Qt.resolvedUrl("preview.svg").toString()
    tryVerify(function() { return thumbnail.source.toString() !== "" })
    var original = thumbnail.source.toString()

    thumbnail.refresh()
    // The cached image stays available while its replacement loads.
    verify(thumbnail.source.toString() !== "")
    tryVerify(function() { return thumbnail.source.toString() !== original })
    var refreshed = thumbnail.source.toString()

    thumbnail.thumbnailUrl = Qt.resolvedUrl("missing.svg").toString()
    tryVerify(function() {
      return Array.from(thumbnail.children).some(function(image) { return image.status === Image.Error })
    })
    compare(thumbnail.source.toString(), refreshed)

    // Rebuilding a row must reuse the decoded image without a loading placeholder.
    var row = createTemporaryObject(rowImageComponent, this, { source: thumbnail.source })
    compare(row.status, Image.Ready)
    row.destroy()
    var rebuiltRow = createTemporaryObject(rowImageComponent, this, { source: thumbnail.source })
    compare(rebuiltRow.status, Image.Ready)

    thumbnail.thumbnailUrl = Qt.resolvedUrl("preview.svg?retry=1").toString()
    tryVerify(function() { return thumbnail.source.toString() !== refreshed })
    verify(thumbnail.source.toString().indexOf("?retry=1&t=") >= 0)
  }

  function test_firstFailureHasNoImage() {
    var thumbnail = createTemporaryObject(thumbnailComponent, this)
    thumbnail.thumbnailUrl = Qt.resolvedUrl("missing.svg").toString()
    tryVerify(function() {
      return Array.from(thumbnail.children).some(function(image) { return image.status === Image.Error })
    })
    compare(thumbnail.source.toString(), "")
  }
}
