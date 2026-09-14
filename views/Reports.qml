import QtQuick
import qs.Commons
import qs.Ui
import "../charts"

// Reports: the headline metrics, spending by category against the period
// before, and cash flow over the year. Enter opens a category's lines, t
// jumps to its transactions.
Item {
  id: view
  property var app: null
  readonly property bool formFocused: false

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  property var rep: null
  property var metrics: null
  property string key: ""
  property int cursor: 0
  property var open: ({})
  readonly property var rows: rep && rep.rows ? rep.rows : []
  readonly property var current: rows.length > 0 ? rows[Math.min(cursor, rows.length - 1)] : null
  readonly property string cur: app && app.snap && app.snap.baseCurrency ? app.snap.baseCurrency : ""
  readonly property var months: app && app.snap && app.snap.months ? app.snap.months : []

  function money(minor) { return app ? app.fmt(minor, view.cur) : String(minor) }
  // A line against its plan for the period, when one was set.
  function planLabel(row) {
    if (!row || !row.planned) return ""
    return Math.round((row.spent / row.planned) * 100) + "%"
  }
  function planColor(row) {
    if (!row || !row.planned) return view.dimmer
    if (!view.app) return view.fg
    var pct = (row.spent / row.planned) * 100
    if (pct >= 100) return view.app.overLimit
    if (pct >= 80) return view.app.nearLimit
    return view.app.onBudget
  }
  function monthLabel(k) {
    var parts = String(k || "").split("-")
    if (parts.length !== 2) return k
    var names = ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"]
    return (names[Number(parts[1]) - 1] || parts[1]) + " " + parts[0]
  }
  function shiftKey(k, n) {
    var parts = String(k).split("-")
    var y = Number(parts[0]), m = Number(parts[1]) + n
    while (m < 1) { m += 12; y-- }
    while (m > 12) { m -= 12; y++ }
    return y + "-" + (m < 10 ? "0" : "") + m
  }
  function compact(minor) {
    if (app && app.blurAmounts) return "•••"
    var d = app ? app.decimalsFor(view.cur) : 2
    var v = minor / Math.pow(10, d)
    var sign = v < 0 ? "-" : ""
    v = Math.abs(v)
    if (v >= 1000000) return sign + (v / 1000000).toFixed(1) + "M"
    if (v >= 1000) return sign + (v / 1000).toFixed(v >= 10000 ? 0 : 1) + "K"
    return sign + v.toFixed(0)
  }

  function reload() {
    if (!app) return
    if (view.key === "") {
      if (app.snap && app.snap.budget && app.snap.budget.period) view.key = app.snap.budget.period
      else return
    }
    app.query(["report", "spending", "-period", view.key], function (data, err) {
      if (err) { view.app.lastError = err; return }
      view.rep = data
      if (view.cursor >= view.rows.length) view.cursor = Math.max(0, view.rows.length - 1)
    })
    app.query(["report", "metrics"], function (data, err) { if (!err) view.metrics = data })
  }
  onAppChanged: reload()
  onKeyChanged: reload()
  Connections {
    target: view.app
    function onChanged() { view.reload() }
    function onSnapChanged() { if (view.key === "") view.reload() }
  }

  PointerMoveGate {
    id: pointerGate
    referenceItem: view
  }

  function pointFrom(index, item, mouse) {
    if (view.formFocused) return
    if (!pointerGate.moved(item, mouse)) return
    view.cursor = index
  }

  function toggle(row) {
    if (!row) return
    var o = view.open
    o[row.categoryId] = !o[row.categoryId]
    view.open = o
    view.openChanged()
  }
  function drill(row) {
    if (!row || !app || !view.rep) return
    app.openTransactions({ category: row.categoryId, from: view.rep.from, to: view.rep.to, label: row.name })
  }

  function handleKey(e) {
    if (e.modifiers !== Qt.NoModifier) return false
    switch (e.key) {
    case Qt.Key_J: case Qt.Key_Down:
      pointerGate.reset()
      if (view.rows.length) view.cursor = Math.min(view.rows.length - 1, view.cursor + 1)
      return true
    case Qt.Key_K: case Qt.Key_Up: pointerGate.reset(); view.cursor = Math.max(0, view.cursor - 1); return true
    case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_Space: view.toggle(view.current); return true
    case Qt.Key_T: view.drill(view.current); return true
    case Qt.Key_BracketLeft: view.key = view.shiftKey(view.key, -1); return true
    case Qt.Key_BracketRight: view.key = view.shiftKey(view.key, 1); return true
    }
    return false
  }

  component Caption: Text {
    color: view.dimmer
    font.family: view.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }
  component Body: Text {
    color: view.fg
    font.family: view.ff
    font.pixelSize: Style.font.body
  }
  component Card: Rectangle {
    radius: Style.cornerRadius
    color: "transparent"
    border.width: Style.normalBorderWidth
    border.color: view.border
  }

  Flickable {
    anchors.fill: parent
    contentWidth: width
    contentHeight: col.implicitHeight
    interactive: contentHeight > height
    boundsBehavior: Flickable.StopAtBounds
    clip: true

    Column {
      id: col
      width: parent.width
      spacing: Style.space(14)

      Row {
        width: parent.width
        spacing: Style.space(12)
        Text {
          anchors.verticalCenter: parent.verticalCenter
          text: "Reports"
          color: view.fg
          font.family: view.ff
          font.pixelSize: Style.font.heading
        }
        Text {
          anchors.verticalCenter: parent.verticalCenter
          text: view.rep ? view.rep.from + " to " + view.rep.to + "   against " + view.rep.prevFrom + " to " + view.rep.prevTo : ""
          color: view.dimmer
          font.family: view.ff
          font.pixelSize: Style.font.caption
        }
      }

      // ---- metrics, spec 6.1
      Row {
        width: parent.width
        spacing: Style.space(12)
        readonly property real tileW: (width - spacing * 3) / 4
        Repeater {
          model: [
            { label: "AVERAGE PER DAY", value: view.metrics ? view.money(view.metrics.averageDaily) : "",
              sub: view.metrics ? "spent " + view.money(view.metrics.expense) + " so far" : "" },
            { label: "PROJECTED PERIOD END", value: view.metrics ? view.money(view.metrics.projected) : "",
              sub: view.metrics ? "still due " + view.money(view.metrics.recurringDue) : "" },
            { label: "RUNWAY", value: view.metrics ? view.metrics.runwayMonths.toFixed(1) + " months" : "",
              sub: view.metrics ? "liquid funds over " + view.money(view.metrics.trailing) + " a period" : "" },
            { label: "FIXED SHARE", value: view.metrics ? view.metrics.fixedShare + "%" : "",
              sub: "of spending posted by bills" }
          ]
          delegate: Card {
            required property var modelData
            width: parent.tileW
            height: Style.space(84)
            Column {
              anchors.fill: parent
              anchors.margins: Style.space(12)
              spacing: Style.space(3)
              Caption { text: modelData.label }
              Text {
                text: modelData.value
                color: view.fg
                font.family: view.ff
                font.pixelSize: Style.font.title
                font.bold: true
              }
              Caption { text: modelData.sub; font.letterSpacing: 0; color: view.dim }
            }
          }
        }
      }

      // ---- spending by category, spec R1
      Card {
        width: parent.width
        height: spendCol.implicitHeight + Style.space(28)
        Column {
          id: spendCol
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.top: parent.top
          anchors.margins: Style.space(14)
          spacing: Style.space(6)

          Row {
            width: parent.width
            spacing: Style.space(10)
            Caption { anchors.verticalCenter: parent.verticalCenter; text: "SPENDING BY CATEGORY" }
            Row {
              anchors.verticalCenter: parent.verticalCenter
              spacing: Style.space(4)
              Button { text: "["; foreground: view.dim; accent: view.accent; fontFamily: view.ff; fontSize: Style.font.caption; bordered: true; onClicked: view.key = view.shiftKey(view.key, -1) }
              Body { anchors.verticalCenter: parent.verticalCenter; text: view.monthLabel(view.key); leftPadding: Style.space(6); rightPadding: Style.space(6) }
              Button { text: "]"; foreground: view.dim; accent: view.accent; fontFamily: view.ff; fontSize: Style.font.caption; bordered: true; onClicked: view.key = view.shiftKey(view.key, 1) }
            }
            Caption {
              anchors.verticalCenter: parent.verticalCenter
              text: view.rep ? "total " + view.money(view.rep.total) + "   before " + view.money(view.rep.previous) : ""
              font.letterSpacing: 0
            }
          }

          Body {
            visible: view.rows.length === 0
            text: "Nothing spent in this window or the one before."
            color: view.dim
          }

          Row {
            width: parent.width
            visible: view.rows.length > 0
            leftPadding: Style.space(8)
            rightPadding: Style.space(8)
            spacing: Style.space(8)
            Item { width: Style.space(24) + Style.space(190); height: 1 }
            Item { width: parent.width - Style.space(24) - Style.space(190) - Style.space(110) * 4 - Style.space(8) * 7 - Style.space(16); height: 1 }
            Caption { width: Style.space(110); horizontalAlignment: Text.AlignRight; text: "SPENT" }
            Caption { width: Style.space(110); horizontalAlignment: Text.AlignRight; text: "SHARE" }
            Caption { width: Style.space(110); horizontalAlignment: Text.AlignRight; text: "VS BEFORE" }
            Caption { width: Style.space(110); horizontalAlignment: Text.AlignRight; text: "VS PLAN" }
          }

          Repeater {
            model: view.rows
            delegate: Column {
              id: group
              required property var modelData
              required property int index
              width: parent.width
              readonly property bool selected: index === view.cursor
              readonly property bool expanded: view.open[modelData.categoryId] === true
              spacing: 0

              CursorSurface {
                width: parent.width
                height: Style.space(30)
                radius: 0
                hasCursor: group.selected
                fill: Style.selectedAccentFill
                foreground: view.fg
                accent: view.accent
                Row {
                  anchors.fill: parent
                  anchors.leftMargin: Style.space(8)
                  anchors.rightMargin: Style.space(8)
                  spacing: Style.space(8)
                  Text {
                    width: Style.space(24)
                    anchors.verticalCenter: parent.verticalCenter
                    text: group.modelData.icon || ""
                    color: group.selected ? view.accent : view.dim
                    font.family: view.ff
                    font.pixelSize: Style.font.icon
                  }
                  Body {
                    width: Style.space(190)
                    anchors.verticalCenter: parent.verticalCenter
                    text: (group.modelData.children && group.modelData.children.length ? (group.expanded ? "▾ " : "▸ ") : "  ") + group.modelData.name
                    elide: Text.ElideRight
                  }
                  Item {
                    width: parent.width - Style.space(24) - Style.space(190) - Style.space(110) * 4 - Style.space(8) * 7
                    height: parent.height
                    Rectangle {
                      anchors.verticalCenter: parent.verticalCenter
                      width: parent.width * Math.min(1, (group.modelData.share || 0) / 100)
                      height: Style.space(8)
                      radius: height / 2
                      color: group.selected ? view.accent : (view.app ? view.app.expense : view.fg)
                      opacity: 0.85
                    }
                  }
                  Body { width: Style.space(110); horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter; text: view.money(group.modelData.spent) }
                  Body { width: Style.space(110); horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter; text: group.modelData.share + "%"; color: view.dim }
                  Body {
                    width: Style.space(110)
                    horizontalAlignment: Text.AlignRight
                    anchors.verticalCenter: parent.verticalCenter
                    text: group.modelData.deltaPct !== undefined && group.modelData.deltaPct !== null
                      ? (group.modelData.deltaPct > 0 ? "+" : "") + Number(group.modelData.deltaPct).toFixed(1) + "%"
                      : group.modelData.delta > 0 ? "new" : ""
                    color: group.modelData.delta > 0 ? (view.app ? view.app.expense : view.fg) : (view.app ? view.app.income : view.fg)
                    font.pixelSize: Style.font.bodySmall
                  }
                  Body {
                    width: Style.space(110)
                    horizontalAlignment: Text.AlignRight
                    anchors.verticalCenter: parent.verticalCenter
                    text: view.planLabel(group.modelData)
                    color: view.planColor(group.modelData)
                    font.pixelSize: Style.font.bodySmall
                  }
                }
                MouseArea {
                  id: groupMouse
                  anchors.fill: parent
                  hoverEnabled: true
                  cursorShape: Qt.PointingHandCursor
                  onEntered: view.pointFrom(group.index, group, { x: groupMouse.mouseX, y: groupMouse.mouseY })
                  onPositionChanged: function (mouse) { view.pointFrom(group.index, group, mouse) }
                  onClicked: { view.cursor = group.index; view.toggle(group.modelData) }
                  onDoubleClicked: view.drill(group.modelData)
                }
              }

              Repeater {
                model: group.expanded && group.modelData.children ? group.modelData.children : []
                delegate: Item {
                  id: child
                  required property var modelData
                  width: parent.width
                  height: Style.space(24)
                  Row {
                    anchors.fill: parent
                    anchors.leftMargin: Style.space(40)
                    anchors.rightMargin: Style.space(8)
                    spacing: Style.space(8)
                    Body {
                      width: Style.space(182)
                      anchors.verticalCenter: parent.verticalCenter
                      text: child.modelData.name
                      color: view.dim
                      font.pixelSize: Style.font.bodySmall
                      elide: Text.ElideRight
                    }
                    Item { width: parent.width - Style.space(182) - Style.space(110) * 4 - Style.space(8) * 5; height: 1 }
                    Body { width: Style.space(110); horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter; text: view.money(child.modelData.spent); color: view.dim; font.pixelSize: Style.font.bodySmall }
                    Body { width: Style.space(110); horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter; text: child.modelData.share + "%"; color: view.dimmer; font.pixelSize: Style.font.bodySmall }
                    Body {
                      width: Style.space(110)
                      horizontalAlignment: Text.AlignRight
                      anchors.verticalCenter: parent.verticalCenter
                      text: child.modelData.deltaPct !== undefined && child.modelData.deltaPct !== null
                        ? (child.modelData.deltaPct > 0 ? "+" : "") + Number(child.modelData.deltaPct).toFixed(1) + "%"
                        : child.modelData.delta > 0 ? "new" : ""
                      color: view.dimmer
                      font.pixelSize: Style.font.bodySmall
                    }
                    Body {
                      width: Style.space(110)
                      horizontalAlignment: Text.AlignRight
                      anchors.verticalCenter: parent.verticalCenter
                      text: view.planLabel(child.modelData)
                      color: view.planColor(child.modelData)
                      font.pixelSize: Style.font.bodySmall
                    }
                  }
                }
              }
            }
          }

          Caption {
            text: "j k move   Enter open   t transactions   [ ] period"
            font.letterSpacing: 0
            topPadding: Style.space(4)
          }
        }
      }

      // ---- income against expense over the year, spec R2
      Card {
        width: parent.width
        height: flowCol.implicitHeight + Style.space(28)
        Column {
          id: flowCol
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.top: parent.top
          anchors.margins: Style.space(14)
          spacing: Style.space(10)
          Caption { text: "INCOME AGAINST EXPENSE, TWELVE PERIODS" }
          GroupedBarChart {
            id: flowChart
            emptyText: "Nothing to chart in these periods."
            progress: 0
            Behavior on progress { NumberAnimation { duration: (view.app ? view.app.moveMs : 140) * 2; easing.type: Easing.OutCubic } }
            Component.onCompleted: flowChart.progress = 1
            width: parent.width
            height: Style.space(200)
            labels: view.months.map(function (m) { return m.label })
            series: [
              { label: "Income", color: view.app ? view.app.income : view.accent, values: view.months.map(function (m) { return m.income }) },
              { label: "Expenses", color: view.app ? view.app.expense : view.fg, values: view.months.map(function (m) { return m.expense }) },
              { label: "Net", color: view.app ? view.app.net : view.fg, values: view.months.map(function (m) { return m.net }) }
            ]
            axisColor: view.dimmer
            gridColor: view.border
            textColor: view.dimmer
            legendTextColor: view.dim
            fontFamily: view.ff
            fontSize: Style.font.caption
            formatValue: function (v) { return view.compact(v) }
          }
          Row {
            width: parent.width
            spacing: Style.space(8)
            Caption { width: Style.space(60); text: "PERIOD" }
            Caption { width: Style.space(120); text: "INCOME"; horizontalAlignment: Text.AlignRight }
            Caption { width: Style.space(120); text: "EXPENSE"; horizontalAlignment: Text.AlignRight }
            Caption { width: Style.space(120); text: "NET"; horizontalAlignment: Text.AlignRight }
            Caption { width: Style.space(90); text: "SAVED"; horizontalAlignment: Text.AlignRight }
          }
          Repeater {
            model: view.months.slice().reverse()
            delegate: Row {
              required property var modelData
              width: parent.width
              spacing: Style.space(8)
              visible: modelData.income !== 0 || modelData.expense !== 0
              Body { width: Style.space(60); text: modelData.label + " " + String(modelData.key).slice(2, 4); color: view.dim; font.pixelSize: Style.font.bodySmall }
              Body { width: Style.space(120); horizontalAlignment: Text.AlignRight; text: view.money(modelData.income); color: view.app ? view.app.income : view.fg; font.pixelSize: Style.font.bodySmall }
              Body { width: Style.space(120); horizontalAlignment: Text.AlignRight; text: view.money(modelData.expense); color: view.app ? view.app.expense : view.fg; font.pixelSize: Style.font.bodySmall }
              Body { width: Style.space(120); horizontalAlignment: Text.AlignRight; text: view.money(modelData.net); font.pixelSize: Style.font.bodySmall }
              Body { width: Style.space(90); horizontalAlignment: Text.AlignRight; text: modelData.savingsRate + "%"; color: view.dim; font.pixelSize: Style.font.bodySmall }
            }
          }
        }
      }
    }
  }
}
