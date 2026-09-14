import QtQuick

// Evenly spaced bars with the baseline at zero, no axes. Every colour and
// size is a property so the component runs outside the shell.
Item {
  id: root

  property var values: []
  property color barColor: "#8fa4c8"
  property color negativeColor: barColor
  property color highlightColor: barColor
  property int highlightIndex: -1
  property color baselineColor: "transparent"
  property real gap: 2
  // Drawn when there is nothing to plot.
  property color emptyColor: "transparent"
  // Rounding on the end away from the baseline. Unscaled like gap, since
  // this component takes its sizes from the caller.
  property real barRadius: 2
  property real minBarHeight: 1

  onValuesChanged: canvas.requestPaint()
  onBarColorChanged: canvas.requestPaint()
  onNegativeColorChanged: canvas.requestPaint()
  onHighlightColorChanged: canvas.requestPaint()
  onHighlightIndexChanged: canvas.requestPaint()
  onBaselineColorChanged: canvas.requestPaint()
  onGapChanged: canvas.requestPaint()
  onMinBarHeightChanged: canvas.requestPaint()

  // One number per slot; a value that is not finite counts as zero so the
  // bars keep their index.
  function numbers(src) {
    var out = []
    if (!src || src.length === undefined) return out
    for (var i = 0; i < src.length; i++) {
      var v = Number(src[i])
      out.push(isFinite(v) ? v : 0)
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
        var vals = root.numbers(root.values)
        var n = vals.length
        if (n === 0) {
          // Same as the line chart: an empty strip says so rather than
          // leaving a gap beside a figure.
          ctx.strokeStyle = root.emptyColor
          ctx.lineWidth = 1
          ctx.setLineDash([Math.max(2, w / 40), Math.max(2, w / 40)])
          ctx.beginPath()
          ctx.moveTo(0, h / 2)
          ctx.lineTo(w, h / 2)
          ctx.stroke()
          ctx.setLineDash([])
          return
        }

        var min = 0, max = 0
        for (var i = 0; i < n; i++) {
          if (vals[i] < min) min = vals[i]
          if (vals[i] > max) max = vals[i]
        }
        var span = max - min
        var minH = Math.max(0, root.minBarHeight)

        // Baseline from the sign of the data: bottom, top or in between.
        var base = span > 0 ? h * max / span : h
        var pxPerUnit = span > 0 ? h / span : 0
        var baseY = Math.round(base)

        // Slot layout, shrinking the gap when the bars would vanish.
        var g = Math.max(0, root.gap)
        var barW = (w - g * (n - 1)) / n
        if (barW < 1) { g = 0; barW = w / n }
        var step = barW + g

        if (root.baselineColor.a > 0) {
          var ly = Math.min(h - 0.5, Math.max(0.5, baseY + (baseY >= h ? -0.5 : 0.5)))
          ctx.strokeStyle = root.baselineColor
          ctx.lineWidth = 1
          ctx.beginPath()
          ctx.moveTo(0, ly)
          ctx.lineTo(w, ly)
          ctx.stroke()
        }

        for (var j = 0; j < n; j++) {
          var v = vals[j]
          var x0 = Math.round(j * step)
          var x1 = Math.round(j * step + barW)
          var bw = Math.max(1, x1 - x0)
          var len = Math.max(minH, Math.round(Math.abs(v) * pxPerUnit))
          var up = v > 0 || (v === 0 && baseY > 0)
          var y = up ? baseY - len : baseY
          if (up && y < 0) { y = 0; len = baseY }
          if (!up && y + len > h) len = h - y
          ctx.fillStyle = j === root.highlightIndex ? root.highlightColor
                        : v < 0 ? root.negativeColor : root.barColor
          // Rounded on the end away from the baseline only, so a bar still
          // sits flat on zero. Canvas has no rounded rectangle, and at this
          // size a radius over a third of the width stops reading as a bar.
          var r = Math.min(bw / 3, len, root.barRadius)
          if (r < 0.5) {
            ctx.fillRect(x0, y, bw, len)
          } else {
            ctx.beginPath()
            if (up) {
              ctx.moveTo(x0, y + len)
              ctx.lineTo(x0, y + r)
              ctx.quadraticCurveTo(x0, y, x0 + r, y)
              ctx.lineTo(x0 + bw - r, y)
              ctx.quadraticCurveTo(x0 + bw, y, x0 + bw, y + r)
              ctx.lineTo(x0 + bw, y + len)
            } else {
              ctx.moveTo(x0, y)
              ctx.lineTo(x0, y + len - r)
              ctx.quadraticCurveTo(x0, y + len, x0 + r, y + len)
              ctx.lineTo(x0 + bw - r, y + len)
              ctx.quadraticCurveTo(x0 + bw, y + len, x0 + bw, y + len - r)
              ctx.lineTo(x0 + bw, y)
            }
            ctx.closePath()
            ctx.fill()
          }
        }
      } catch (e) {
      }
    }
  }
}
