import QtQuick

// A line of values scaled to the item, oldest first, no axes. Every colour
// and size is a property so the component runs outside the shell.
Item {
  id: root

  property var points: []
  property color lineColor: "#8fa4c8"
  property color fillColor: "transparent"
  property real lineWidth: 1.5
  property bool endDot: true
  property real padding: 2
  // Drawn when there is nothing to plot.
  property color emptyColor: "transparent"

  onPointsChanged: canvas.requestPaint()
  onLineColorChanged: canvas.requestPaint()
  onFillColorChanged: canvas.requestPaint()
  onLineWidthChanged: canvas.requestPaint()
  onEndDotChanged: canvas.requestPaint()
  onPaddingChanged: canvas.requestPaint()
  onEmptyColorChanged: canvas.requestPaint()

  // Finite numbers only, in the order given.
  function numbers(src) {
    var out = []
    if (!src || src.length === undefined) return out
    for (var i = 0; i < src.length; i++) {
      var v = Number(src[i])
      if (isFinite(v)) out.push(v)
    }
    return out
  }

  Canvas {
    id: canvas
    anchors.fill: parent
    onWidthChanged: requestPaint()
    onHeightChanged: requestPaint()

    onPaint: {
      var ctx = getContext("2d")
      if (!ctx) return
      var w = width, h = height
      ctx.clearRect(0, 0, w, h)
      if (!(w > 0) || !(h > 0)) return
      try {
        var vals = root.numbers(root.points)
        var n = vals.length
        if (n === 0) {
          // A dashed rule through the middle, so an empty tile reads as
          // nothing-yet rather than as something that failed to draw.
          ctx.strokeStyle = root.emptyColor
          ctx.lineWidth = 1
          ctx.setLineDash([Math.max(2, w / 40), Math.max(2, w / 40)])
          ctx.beginPath()
          ctx.moveTo(Math.max(0, root.padding), h / 2)
          ctx.lineTo(Math.max(0, w - root.padding), h / 2)
          ctx.stroke()
          ctx.setLineDash([])
          return
        }

        var pad = Math.max(0, root.padding)
        var lw = Math.max(0.5, root.lineWidth)
        var dotR = Math.max(1.5, lw * 1.25)
        var left = pad, right = Math.max(pad, w - pad)
        var top = pad, bottom = Math.max(pad, h - pad)
        var mid = (top + bottom) / 2

        if (n === 1) {
          if (root.endDot) {
            ctx.fillStyle = root.lineColor
            ctx.beginPath()
            ctx.arc(right, mid, dotR, 0, Math.PI * 2)
            ctx.fill()
          }
          return
        }

        var min = vals[0], max = vals[0]
        for (var i = 1; i < n; i++) {
          if (vals[i] < min) min = vals[i]
          if (vals[i] > max) max = vals[i]
        }
        var span = max - min
        var xs = [], ys = []
        for (var j = 0; j < n; j++) {
          xs.push(left + (right - left) * j / (n - 1))
          ys.push(span > 0 ? bottom - (vals[j] - min) / span * (bottom - top) : mid)
        }

        if (root.fillColor.a > 0) {
          // Fading down rather than a flat block, so the line reads as the
          // subject and the fill as its shadow. Both stops come from the fill
          // the caller passed, so nothing here invents a colour.
          var grad = ctx.createLinearGradient(0, top, 0, bottom)
          grad.addColorStop(0, Qt.rgba(root.fillColor.r, root.fillColor.g, root.fillColor.b, root.fillColor.a))
          grad.addColorStop(1, Qt.rgba(root.fillColor.r, root.fillColor.g, root.fillColor.b, 0))
          ctx.fillStyle = grad
          ctx.beginPath()
          ctx.moveTo(xs[0], ys[0])
          for (var k = 1; k < n; k++) ctx.lineTo(xs[k], ys[k])
          // Closed to the padded baseline, not the item's edge: the stroke is
          // inset by padding and the fill used to overshoot it by that much.
          ctx.lineTo(xs[n - 1], bottom)
          ctx.lineTo(xs[0], bottom)
          ctx.closePath()
          ctx.fill()
        }

        ctx.strokeStyle = root.lineColor
        ctx.lineWidth = lw
        ctx.lineJoin = "round"
        ctx.lineCap = "round"
        ctx.beginPath()
        ctx.moveTo(xs[0], ys[0])
        for (var m = 1; m < n; m++) ctx.lineTo(xs[m], ys[m])
        ctx.stroke()

        if (root.endDot) {
          ctx.fillStyle = root.lineColor
          ctx.beginPath()
          ctx.arc(xs[n - 1], ys[n - 1], dotR, 0, Math.PI * 2)
          ctx.fill()
        }
      } catch (e) {
      }
    }
  }
}
