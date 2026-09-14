import QtQuick

// Groups of bars along the x axis, one bar per series, with a value axis at
// the left, a zero line, faint grid lines and a legend at the right. Every
// colour, font and size is a property so the component runs outside the
// shell.
Item {
  id: root

  property var labels: []
  property var series: []
  property int visibleGroups: 0
  property color axisColor: "#5a6472"
  property color gridColor: "#2a3038"
  property color textColor: "#9aa4b2"
  property color legendTextColor: textColor
  property string fontFamily: "monospace"
  property int fontSize: 11
  property var formatValue: function (v) { return String(v) }
  property bool showLegend: true
  property real groupGap: 0.35
  // Said in the middle of the plot when there is nothing to chart.
  property string emptyText: ""
  // How far the bars have grown, 0 to 1. Animated by the caller, or left at
  // 1 for a chart that should simply be there.
  property real progress: 1
  // Group under the pointer, or -1. The caller reads it for a readout.
  property int hoverGroup: -1

  onLabelsChanged: canvas.requestPaint()
  onSeriesChanged: canvas.requestPaint()
  onVisibleGroupsChanged: canvas.requestPaint()
  onAxisColorChanged: canvas.requestPaint()
  onGridColorChanged: canvas.requestPaint()
  onTextColorChanged: canvas.requestPaint()
  onLegendTextColorChanged: canvas.requestPaint()
  onFontFamilyChanged: canvas.requestPaint()
  onFontSizeChanged: canvas.requestPaint()
  onFormatValueChanged: canvas.requestPaint()
  onShowLegendChanged: canvas.requestPaint()
  onGroupGapChanged: canvas.requestPaint()
  onEmptyTextChanged: canvas.requestPaint()
  onProgressChanged: canvas.requestPaint()
  onHoverGroupChanged: canvas.requestPaint()

  // Which group a point in the chart's own coordinates falls in, or -1. The
  // caller uses it to drive a readout; the geometry is recomputed here rather
  // than cached because a hover already costs a repaint.
  function groupAt(px) {
    var all = root.labels && root.labels.length !== undefined ? root.labels : []
    var total = all.length
    var offset = root.visibleGroups > 0 && root.visibleGroups < total ? total - root.visibleGroups : 0
    var groups = total - offset
    if (groups <= 0) return -1
    var g = Math.floor((px - root.plotLeft) / ((root.width - root.plotRight - root.plotLeft) / groups))
    return (g >= 0 && g < groups) ? g : -1
  }

  // Where the plot starts and ends, published after each paint so hit testing
  // uses the same numbers the drawing did.
  property real plotLeft: 0
  property real plotRight: 0

  function tickText(v) {
    var f = root.formatValue
    try {
      if (typeof f === "function") return String(f(v))
    } catch (e) {
    }
    return String(v)
  }

  // The series a bar can be drawn for: an object with a values array.
  function usableSeries(src) {
    var out = []
    if (!src || src.length === undefined) return out
    for (var i = 0; i < src.length; i++) {
      var s = src[i]
      if (!s || typeof s !== "object") continue
      if (!s.values || s.values.length === undefined) continue
      out.push(s)
    }
    return out
  }

  // Nice ticks that include zero: a step of 1, 2, 2.5 or 5 times a power of
  // ten, the smallest that needs at most five ticks to cover lo..hi.
  function niceTicks(lo, hi) {
    var range = hi - lo
    if (!(range > 0)) return [0]
    var mult = [1, 2, 2.5, 5]
    var power = Math.pow(10, Math.floor(Math.log(range) / Math.LN10) - 2)
    for (var round = 0; round < 12; round++) {
      for (var m = 0; m < mult.length; m++) {
        var step = mult[m] * power
        var first = Math.floor(lo / step), last = Math.ceil(hi / step)
        if (last - first + 1 <= 5) {
          var ticks = []
          for (var k = first; k <= last; k++) {
            var v = k * step
            ticks.push(step % 1 === 0 ? Math.round(v) : v)
          }
          return ticks
        }
      }
      power *= 10
    }
    return [0]
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
        var fs = Math.max(1, root.fontSize)
        ctx.font = fs + 'px "' + String(root.fontFamily).replace(/"/g, "") + '"'
        ctx.lineWidth = 1

        var allLabels = root.labels && root.labels.length !== undefined ? root.labels : []
        var ser = root.usableSeries(root.series)
        var total = allLabels.length
        var offset = root.visibleGroups > 0 && root.visibleGroups < total ? total - root.visibleGroups : 0
        var groups = total - offset
        var hasData = groups > 0 && ser.length > 0

        // Data range over the visible groups, always including zero.
        var lo = 0, hi = 0
        if (hasData) {
          for (var si = 0; si < ser.length; si++) {
            var sv = ser[si].values
            for (var gi = 0; gi < groups; gi++) {
              var v = Number(sv[offset + gi])
              if (!isFinite(v)) continue
              if (v < lo) lo = v
              if (v > hi) hi = v
            }
          }
        }
        var ticks = hasData ? root.niceTicks(lo, hi) : [0]
        var yMin = ticks[0], yMax = ticks[ticks.length - 1]
        if (!(yMax > yMin)) { yMin = 0; yMax = 1 }

        // Margins: tick labels at the left, group labels below with room
        // for their descenders, legend at the right.
        var tickPad = Math.round(fs * 0.5)
        var axisW = 0
        if (hasData) {
          for (var ti = 0; ti < ticks.length; ti++)
            axisW = Math.max(axisW, ctx.measureText(root.tickText(ticks[ti])).width)
          axisW = Math.ceil(axisW) + tickPad
        }
        var legendW = 0
        var square = Math.round(fs * 0.8)
        if (hasData && root.showLegend) {
          for (var li = 0; li < ser.length; li++)
            legendW = Math.max(legendW, ctx.measureText(String(ser[li].label || "")).width)
          legendW = Math.ceil(legendW) + square + tickPad * 3
          if (legendW > w * 0.4) legendW = 0
        }
        var topPad = Math.round(fs * 0.6)
        var bottomH = tickPad + Math.ceil(fs * 1.4)
        var plotL = axisW, plotR = w - legendW
        var plotT = topPad, plotB = h - bottomH
        root.plotLeft = plotL
        root.plotRight = w - plotR
        var plotW = plotR - plotL, plotH = plotB - plotT
        if (!(plotW > 0) || !(plotH > 0)) return

        function yOf(val) { return plotB - (val - yMin) / (yMax - yMin) * plotH }

        // Grid, tick labels and the zero line.
        ctx.textAlign = "right"
        ctx.textBaseline = "middle"
        for (var t = 0; t < ticks.length; t++) {
          var tv = ticks[t]
          var ty = Math.min(h - 0.5, Math.max(0.5, Math.round(yOf(tv)) + 0.5))
          // Zero is the rule everything is read against, so it carries its
          // own weight: the axis colour alone is often fainter than the grid
          // colour beside it, which left zero as the palest line on the chart.
          ctx.strokeStyle = tv === 0 ? root.axisColor : root.gridColor
          ctx.lineWidth = tv === 0 ? 1.5 : 1
          ctx.beginPath()
          ctx.moveTo(plotL, ty)
          ctx.lineTo(plotR, ty)
          ctx.stroke()
          ctx.lineWidth = 1
          if (hasData) {
            ctx.fillStyle = root.textColor
            ctx.fillText(root.tickText(tv), plotL - tickPad, ty)
          }
        }
        if (!hasData) {
          if (root.emptyText !== "") {
            ctx.fillStyle = root.textColor
            ctx.textAlign = "center"
            ctx.textBaseline = "middle"
            ctx.fillText(root.emptyText, (plotL + plotR) / 2, (plotT + plotB) / 2)
          }
          return
        }

        // Bars.
        var slotW = plotW / groups
        var gapFrac = Math.min(0.9, Math.max(0, root.groupGap))
        var innerW = slotW * (1 - gapFrac)
        var barW = innerW / ser.length
        var inner = barW >= 4 ? 1 : 0
        var zeroY = Math.round(yOf(0))
        var grown = Math.max(0, Math.min(1, root.progress))

        // The group under the pointer, behind its bars.
        if (root.hoverGroup >= 0 && root.hoverGroup < groups) {
          ctx.fillStyle = root.gridColor
          ctx.fillRect(plotL + root.hoverGroup * slotW, plotT, slotW, plotH)
        }

        for (var g = 0; g < groups; g++) {
          var start = plotL + g * slotW + (slotW - innerW) / 2
          for (var s = 0; s < ser.length; s++) {
            var val = Number(ser[s].values[offset + g])
            if (!isFinite(val) || val === 0) continue
            var x0 = Math.round(start + s * barW)
            var x1 = Math.round(start + (s + 1) * barW) - inner
            var bw = Math.max(1, x1 - x0)
            var vy = Math.round(yOf(val))
            var y = val > 0 ? Math.min(vy, zeroY - 1) : zeroY
            var len = val > 0 ? zeroY - y : Math.max(1, vy - zeroY)
            // Grown from the zero line rather than faded in, so the axis
            // stays put and only the data moves.
            len = Math.max(1, Math.round(len * grown))
            if (val > 0) y = zeroY - len
            ctx.fillStyle = ser[s].color ? ser[s].color : root.axisColor
            ctx.fillRect(x0, y, bw, len)
          }
        }

        // Group labels, thinned when they would overlap.
        ctx.fillStyle = root.textColor
        ctx.textAlign = "center"
        ctx.textBaseline = "top"
        var labelW = 0
        for (var lw = 0; lw < groups; lw++)
          labelW = Math.max(labelW, ctx.measureText(String(allLabels[offset + lw])).width)
        var every = labelW > 0 && slotW > 0 ? Math.max(1, Math.ceil((labelW + tickPad) / slotW)) : 1
        for (var gl = 0; gl < groups; gl++) {
          if ((groups - 1 - gl) % every !== 0) continue
          var cx = plotL + (gl + 0.5) * slotW
          ctx.fillText(String(allLabels[offset + gl]), cx, plotB + tickPad)
        }

        // Legend.
        if (legendW > 0) {
          var rowH = Math.round(fs * 1.6)
          var lx = plotR + tickPad * 2
          ctx.textAlign = "left"
          ctx.textBaseline = "middle"
          for (var e = 0; e < ser.length; e++) {
            var cy = plotT + e * rowH + rowH / 2
            if (cy + rowH / 2 > h) break
            ctx.fillStyle = ser[e].color ? ser[e].color : root.axisColor
            ctx.fillRect(lx, Math.round(cy - square / 2), square, square)
            ctx.fillStyle = root.legendTextColor
            ctx.fillText(String(ser[e].label || ""), lx + square + tickPad, cy)
          }
        }
      } catch (e) {
      }
    }
  }
}
