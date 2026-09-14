import QtQuick
import qs.Commons
import qs.Ui

// The plan for one period, category by category, against what was spent.
// Enter sets a line, [ and ] move between periods, and the helpers fill a
// period from the last one, from history, or by scaling.
Item {
  id: view
  property var app: null
  readonly property bool formFocused: planField.activeFocus || goalTarget.activeFocus || goalDue.activeFocus
    || pickAmount.activeFocus || pickCategory.popupOpen

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  property var doc: null
  property var env: null
  property string key: ""
  property int cursor: 0
  // The envelope model shows pots instead of plans.
  readonly property bool envelope: !!(app && app.snap && app.snap.model === "envelope")
  // Rows: the planned lines (or pots) first, then the spending without one.
  readonly property var cards: envelope ? (env && env.items ? env.items : []) : (doc && doc.cards ? doc.cards : [])
  readonly property var unbudgeted: doc && doc.unbudgeted ? doc.unbudgeted : []
  readonly property var rows: cards.concat(unbudgeted)
  readonly property var current: rows.length > 0 ? rows[Math.min(cursor, rows.length - 1)] : null
  readonly property string cur: app && app.snap && app.snap.baseCurrency ? app.snap.baseCurrency : ""
  readonly property bool isCurrent: app && app.snap && app.snap.budget ? app.snap.budget.period === view.key : false
  readonly property real elapsedPct: isCurrent && app.snap.period && app.snap.period.days > 0
    ? app.snap.period.elapsed * 100 / app.snap.period.days : -1

  function money(minor) { return app ? app.fmt(minor, view.cur) : String(minor) }

  function monthLabel(k) {
    var parts = String(k || "").split("-")
    if (parts.length !== 2) return k
    var names = ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"]
    var m = Number(parts[1])
    return (names[m - 1] || parts[1]) + " " + parts[0]
  }
  function shiftKey(k, n) {
    var parts = String(k).split("-")
    var y = Number(parts[0]), m = Number(parts[1]) + n
    while (m < 1) { m += 12; y-- }
    while (m > 12) { m -= 12; y++ }
    return y + "-" + (m < 10 ? "0" : "") + m
  }

  function reload() {
    if (!app) return
    if (view.key === "") {
      if (app.snap && app.snap.budget && app.snap.budget.period) view.key = app.snap.budget.period
      else return
    }
    app.query(["budget", "-period", view.key], function (data, err) {
      if (err) { view.app.lastError = err; return }
      view.doc = data
      if (view.cursor >= view.rows.length) view.cursor = Math.max(0, view.rows.length - 1)
    })
    if (view.envelope) {
      app.query(["envelopes", "-period", view.key], function (data, err) {
        if (err) { view.app.lastError = err; return }
        view.env = data
      })
    }
  }
  onEnvelopeChanged: reload()
  onAppChanged: reload()
  onKeyChanged: reload()
  Connections {
    target: view.app
    function onChanged() { view.reload() }
    function onSnapChanged() { if (view.key === "") view.reload() }
  }

  // ---- editing one line
  function isPot(row) { return !!row && row.assigned !== undefined }
  function plannedOf(row) { return view.isPot(row) ? row.assigned : (row ? row.planned : 0) }
  function unplanned(row) { return !view.isPot(row) && (!row || !row.planned || row.planned <= 0) }

  function openPlan(row) {
    if (!row) return
    planner.row = row
    planField.text = view.plannedOf(row) > 0 ? view.plain(view.plannedOf(row)) : ""
    planner.visible = true
    Qt.callLater(function () { planField.forceActiveFocus(); planField.selectAll() })
  }
  function closePlan() {
    planner.visible = false
    planner.row = null
    view.forceActiveFocus()
  }
  function submitPlan() {
    if (!planner.row) return
    var amount = planField.text.trim()
    if (amount === "") amount = "0"
    app.run(["budget", "set", String(planner.row.categoryId), amount, "-period", view.key],
            amount === "0" ? "removed the plan for " + planner.row.name : "planned " + amount + " for " + planner.row.name)
    view.closePlan()
  }
  function plain(minor) {
    var d = app ? app.decimalsFor(view.cur) : 2
    var s = String(Math.abs(Number(minor) || 0))
    while (s.length <= d) s = "0" + s
    return d > 0 ? s.slice(0, s.length - d) + "." + s.slice(s.length - d) : s
  }
  function helper(argv, done) { app.run(["budget"].concat(argv).concat(["-period", view.key]), done) }

  // ---- planning a category that has no line and no spending yet, which is
  // every category the table cannot show
  readonly property var plannable: (app ? app.categories : [])
    .filter(function (c) { return c.kind === "expense" && !c.system && !c.archived })
    .map(function (c) { return { value: c.id, label: (c.parentName ? c.parentName + " / " : "") + c.name } })

  function openPicker() {
    pickCategory.value = ""
    pickAmount.text = ""
    picker.visible = true
    Qt.callLater(function () { pickAmount.forceActiveFocus() })
  }
  function closePicker() {
    picker.visible = false
    view.forceActiveFocus()
  }
  function submitPicker() {
    if (pickCategory.value === "") { view.app.lastError = "pick a category first"; return }
    var amount = pickAmount.text.trim()
    if (amount === "") { pickAmount.forceActiveFocus(); return }
    app.run(["budget", "set", String(pickCategory.value), amount, "-period", view.key],
            (view.envelope ? "assigned " : "planned ") + amount + " for " + pickCategory.currentLabel())
    view.closePicker()
  }

  // ---- goals and pot behaviour
  function openGoal(row) {
    if (!row) return
    goalCard.row = row
    goalTarget.text = row.goalTarget > 0 ? view.plain(row.goalTarget) : ""
    goalDue.text = row.goalDue || ""
    goalCard.visible = true
    Qt.callLater(function () { goalTarget.forceActiveFocus(); goalTarget.selectAll() })
  }
  function closeGoal() {
    goalCard.visible = false
    goalCard.row = null
    view.forceActiveFocus()
  }
  function submitGoal() {
    if (!goalCard.row) return
    var target = goalTarget.text.trim() === "" ? "0" : goalTarget.text.trim()
    var argv = ["category", "goal", String(goalCard.row.categoryId), target]
    if (target !== "0") argv.push(goalDue.text.trim())
    app.run(argv, target === "0" ? "cleared the goal for " + goalCard.row.name : "goal set for " + goalCard.row.name)
    view.closeGoal()
  }
  function cycleBehaviour(row) {
    if (!row) return
    var order = ["monthly", "rollover", "goal", "untracked"]
    var cur = row.behaviour || (view.app ? (view.app.category(row.categoryId) || {}).behaviour : "") || "monthly"
    var next = order[(order.indexOf(cur) + 1) % order.length]
    if (next === "goal" && !(row.goalTarget > 0)) next = "untracked"
    app.run(["category", "behaviour", String(row.categoryId), next], row.name + " is now " + next)
  }

  // This view edits in place rather than behind a sheet, so a plan field can
  // hold the caret while rows sit hoverable underneath. pointFrom refuses to
  // move the cursor while that is true, or the pointer would scroll the list
  // out from under whatever is being typed.
  PointerMoveGate {
    id: pointerGate
    referenceItem: view
  }

  function pointFrom(index, item, mouse) {
    if (view.formFocused) return
    if (!pointerGate.moved(item, mouse)) return
    view.cursor = index
  }

  function handleKey(e) {
    if (picker.visible) {
      if (e.key === Qt.Key_Escape) { view.closePicker(); return true }
      return false
    }
    if (planner.visible) {
      if (e.key === Qt.Key_Escape) { view.closePlan(); return true }
      return false
    }
    if (goalCard.visible) {
      if (e.key === Qt.Key_Escape) { view.closeGoal(); return true }
      return false
    }
    if (e.modifiers & ~Qt.ShiftModifier) return false
    switch (e.key) {
    case Qt.Key_J: case Qt.Key_Down:
      pointerGate.reset()
      if (view.rows.length) view.cursor = Math.min(view.rows.length - 1, view.cursor + 1)
      return true
    case Qt.Key_K: case Qt.Key_Up: pointerGate.reset(); view.cursor = Math.max(0, view.cursor - 1); return true
    case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_E: view.openPlan(view.current); return true
    case Qt.Key_X:
      if (view.current && view.plannedOf(view.current) > 0)
        app.run(["budget", "set", String(view.current.categoryId), "0", "-period", view.key], "removed the plan for " + view.current.name)
      return true
    case Qt.Key_G: view.openGoal(view.current); return true
    case Qt.Key_P: view.openPicker(); return true
    case Qt.Key_B: view.cycleBehaviour(view.current); return true
    case Qt.Key_R:
      if (!view.envelope) return false
      view.helper(["rollover"], "rolled the previous period over")
      return true
    case Qt.Key_BracketLeft: view.key = view.shiftKey(view.key, -1); return true
    case Qt.Key_BracketRight: view.key = view.shiftKey(view.key, 1); return true
    case Qt.Key_C: view.helper(["copy"], "copied the previous period"); return true
    case Qt.Key_V: view.helper(["average", "3"], "planned from the last three periods"); return true
    case Qt.Key_M: view.helper(["median", "12"], "planned from the median of a year"); return true
    case Qt.Key_Plus: case Qt.Key_Equal: view.helper(["scale", "5"], "scaled by five percent"); return true
    case Qt.Key_Minus: view.helper(["scale", "-5"], "scaled by minus five percent"); return true
    }
    return false
  }

  function potInfo(row) {
    if (!row) return ""
    var out = row.behaviour || ""
    if (row.goalTarget > 0) {
      out = "goal " + view.money(row.goalTarget) + " by " + row.goalDue
      if (row.accrual > 0) out += ", " + view.money(row.accrual) + " a period"
    }
    return out
  }

  function pace(row) {
    if (view.elapsedPct < 0 || !row || row.planned <= 0) return ""
    if (row.pct > view.elapsedPct + 10) return "ahead of pace"
    if (row.pct < view.elapsedPct - 10) return "under pace"
    return "on pace"
  }
  function stateColor(row) {
    if (!row || row.planned <= 0) return view.dim
    if (row.state === "over") return app ? app.overLimit : view.fg
    if (row.state === "near") return app ? app.nearLimit : view.fg
    return app ? app.onBudget : view.fg
  }

  component Caption: Text {
    color: view.dimmer
    font.family: view.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }
  component Helper: Button {
    foreground: view.dim
    accent: view.accent
    fontFamily: view.ff
    fontSize: Style.font.caption
    bordered: true
  }

  Column {
    anchors.fill: parent
    spacing: Style.space(12)

    // ---- header: period and its totals
    Row {
      width: parent.width
      spacing: Style.space(12)
      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: "Budget"
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }
      Row {
        anchors.verticalCenter: parent.verticalCenter
        spacing: Style.space(6)
        Helper { text: "["; onClicked: view.key = view.shiftKey(view.key, -1) }
        Text {
          anchors.verticalCenter: parent.verticalCenter
          text: view.monthLabel(view.key) + (view.isCurrent ? "  ·  this period" : "")
          color: view.fg
          font.family: view.ff
          font.pixelSize: Style.font.body
          leftPadding: Style.space(6)
          rightPadding: Style.space(6)
        }
        Helper { text: "]"; onClicked: view.key = view.shiftKey(view.key, 1) }
      }
      Text {
        anchors.verticalCenter: parent.verticalCenter
        visible: view.envelope ? !!view.env : !!view.doc
        text: view.envelope
          ? (view.env ? "to be budgeted " + view.money(view.env.toBeBudgeted) + "   in pots " + view.money(view.env.held)
              + (view.env.deficit > 0 ? "   overspent " + view.money(view.env.deficit) : "")
              + "   spent " + view.money(view.env.spent) : "")
          : (view.doc ? "planned " + view.money(view.doc.planned) + "   spent " + view.money(view.doc.spent)
              + "   " + view.doc.pct + "% used" + (view.elapsedPct >= 0 ? "   day " + view.app.snap.period.elapsed + " of " + view.app.snap.period.days : "") : "")
        color: view.envelope && view.env && view.env.toBeBudgeted < 0 ? (view.app ? view.app.expense : view.fg) : view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
      }
    }

    // ---- helpers
    Row {
      width: parent.width
      spacing: Style.space(8)
      Helper {
        text: view.envelope ? "Assign a category   p" : "Plan a category   p"
        tooltipText: "Any category, including the ones with nothing on them yet"
        foreground: view.fg
        onClicked: view.openPicker()
      }
      Helper { text: "Copy last   c"; tooltipText: "Copy the previous period's lines here"; onClicked: view.helper(["copy"], "copied the previous period") }
      Helper { text: "Average 3   v"; tooltipText: "Plan every line from the average of the last three periods"; onClicked: view.helper(["average", "3"], "planned from the last three periods") }
      Helper { text: "Average 6"; onClicked: view.helper(["average", "6"], "planned from the last six periods") }
      Helper { text: "Median 12   m"; tooltipText: "Plan every line from the median of the last twelve periods"; onClicked: view.helper(["median", "12"], "planned from the median of a year") }
      Helper { text: "+5%   +"; onClicked: view.helper(["scale", "5"], "scaled by five percent") }
      Helper { text: "-5%   -"; onClicked: view.helper(["scale", "-5"], "scaled by minus five percent") }
      Helper { visible: view.envelope; text: "Roll over   r"; tooltipText: "Carry the previous period's pots into this one"; onClicked: view.helper(["rollover"], "rolled the previous period over") }
    }

    // ---- the table
    Rectangle {
      width: parent.width
      height: parent.height - y - footer.height - parent.spacing
      radius: Style.cornerRadius
      color: "transparent"
      border.width: Style.normalBorderWidth
      border.color: view.border
      clip: true

      readonly property real pad: Style.space(12)
      readonly property real iconW: Style.space(28)
      readonly property real numW: Style.space(110)
      readonly property real barW: Style.space(170)
      readonly property real paceW: Style.space(110)
      readonly property real nameW: Math.max(Style.space(80), width - pad * 2 - iconW - numW * 3 - barW - paceW - Style.space(8) * 6)

      Item {
        id: head
        width: parent.width
        height: Style.space(30)
        Row {
          anchors.fill: parent
          anchors.leftMargin: parent.parent.pad
          anchors.rightMargin: parent.parent.pad
          spacing: Style.space(8)
          Item { width: parent.parent.parent.iconW; height: 1 }
          Caption { width: parent.parent.parent.nameW; text: "CATEGORY"; anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.numW; text: view.envelope ? "ROLLED IN" : "PLANNED"; horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.numW; text: view.envelope ? "ASSIGNED" : "SPENT"; horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.numW; text: view.envelope ? "SPENT" : "LEFT"; horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.barW; text: view.envelope ? "AVAILABLE" : "USED"; leftPadding: Style.space(16); anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.paceW; text: view.envelope ? "POT" : "PACE"; anchors.verticalCenter: parent.verticalCenter }
        }
        Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
      }

      Text {
        anchors.centerIn: parent
        visible: view.rows.length === 0
        text: view.doc ? "Nothing planned and nothing spent in " + view.monthLabel(view.key) + ". Press c to copy the previous period."
                       : "Loading"
        color: view.dim
        font.family: view.ff
        font.pixelSize: Style.font.body
      }

      ListView {
        id: list
        anchors.top: head.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        clip: true
        model: view.rows
        currentIndex: view.cursor
        boundsBehavior: Flickable.StopAtBounds
        readonly property var table: parent
        delegate: CursorSurface {
          id: line
          required property var modelData
          required property int index
          width: list.width
          height: Style.space(34)
          readonly property bool selected: index === view.cursor
          readonly property bool pot: view.isPot(modelData)
          readonly property bool unplanned: view.unplanned(modelData)
          readonly property bool firstUnplanned: index === view.cards.length && index > 0
          radius: 0
          hasCursor: line.selected
          fill: Style.selectedAccentFill
          foreground: view.fg
          accent: view.accent

          // A rule between the planned lines and the rest.
          Rectangle {
            visible: line.firstUnplanned
            anchors.top: parent.top
            width: parent.width
            height: Style.spacing.hairline
            color: view.border
          }

          Row {
            anchors.fill: parent
            anchors.leftMargin: list.table.pad
            anchors.rightMargin: list.table.pad
            spacing: Style.space(8)
            Text {
              width: list.table.iconW
              anchors.verticalCenter: parent.verticalCenter
              text: line.modelData.icon || ""
              color: line.selected ? view.accent : view.dim
              font.family: view.ff
              font.pixelSize: Style.font.icon
            }
            Text {
              width: list.table.nameW
              anchors.verticalCenter: parent.verticalCenter
              text: line.modelData.name
              color: line.unplanned ? view.dim : view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
              elide: Text.ElideRight
            }
            Text {
              width: list.table.numW
              anchors.verticalCenter: parent.verticalCenter
              horizontalAlignment: Text.AlignRight
              text: line.pot ? (line.modelData.rolloverIn !== 0 ? view.money(line.modelData.rolloverIn) : "")
                  : line.unplanned ? (view.envelope ? "no pot" : "no plan") : view.money(line.modelData.planned)
              color: line.pot && line.modelData.rolloverIn < 0 ? (view.app ? view.app.expense : view.fg)
                   : line.unplanned ? view.dimmer : view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            Text {
              width: list.table.numW
              anchors.verticalCenter: parent.verticalCenter
              horizontalAlignment: Text.AlignRight
              text: line.pot ? view.money(line.modelData.assigned) : view.money(line.modelData.spent)
              color: view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            Text {
              width: list.table.numW
              anchors.verticalCenter: parent.verticalCenter
              horizontalAlignment: Text.AlignRight
              text: line.pot ? view.money(line.modelData.spent)
                  : line.unplanned ? (view.envelope ? view.money(line.modelData.spent) : "") : view.money(line.modelData.remaining)
              color: !line.pot && !line.unplanned && line.modelData.remaining < 0 ? (view.app ? view.app.expense : view.fg) : view.dim
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            Item {
              width: list.table.barW
              height: parent.height
              Rectangle {
                id: track
                anchors.left: parent.left
                anchors.leftMargin: Style.space(16)
                anchors.verticalCenter: parent.verticalCenter
                width: parent.width - pctText.width - Style.space(24)
                height: Style.space(6)
                radius: height / 2
                color: view.border
                visible: !line.unplanned && !line.pot
                Rectangle {
                  width: parent.width * Math.min(1, (line.modelData.pct || 0) / 100)
                  height: parent.height
                  radius: parent.radius
                  color: view.stateColor(line.modelData)
                }
              }
              Text {
                id: pctText
                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                text: line.pot ? view.money(line.modelData.available) : line.unplanned ? "" : line.modelData.pct + "%"
                color: line.pot ? (line.modelData.available < 0 ? (view.app ? view.app.overLimit : view.fg) : (view.app ? view.app.onBudget : view.fg))
                     : view.stateColor(line.modelData)
                font.family: view.ff
                font.pixelSize: line.pot ? Style.font.body : Style.font.bodySmall
              }
            }
            Text {
              width: list.table.paceW
              anchors.verticalCenter: parent.verticalCenter
              text: line.pot ? view.potInfo(line.modelData)
                  : line.unplanned ? (view.envelope ? "Enter to assign" : "Enter to plan") : view.pace(line.modelData)
              color: line.unplanned ? view.dimmer
                   : !line.pot && view.pace(line.modelData) === "ahead of pace" ? (view.app ? view.app.nearLimit : view.dim) : view.dimmer
              font.family: view.ff
              font.pixelSize: Style.font.caption
              elide: Text.ElideRight
            }
          }
          MouseArea {
            id: lineMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onEntered: view.pointFrom(line.index, line, { x: lineMouse.mouseX, y: lineMouse.mouseY })
            onPositionChanged: function (mouse) { view.pointFrom(line.index, line, mouse) }
            onClicked: view.cursor = line.index
            onDoubleClicked: { view.cursor = line.index; view.openPlan(line.modelData) }
          }
        }
      }
    }

    Text {
      id: footer
      width: parent.width
      text: view.envelope
        ? "j k move   Enter assign   p any category   g goal   b pot behaviour   r roll over   x remove   [ ] period"
        : "j k move   Enter plan   p any category   g goal   x remove   [ ] period   c copy last   v average   m median   + - scale"
      color: view.dimmer
      font.family: view.ff
      font.pixelSize: Style.font.caption
      elide: Text.ElideRight
    }
  }

  // ---- plan any category, not only the ones already on the table
  Rectangle {
    id: picker
    visible: false
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(460))
    height: pickCol.implicitHeight + Style.space(32)
    radius: Style.cornerRadius
    color: Color.popups.background
    border.width: Style.normalBorderWidth
    border.color: Color.popups.border
    z: 12

    Column {
      id: pickCol
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      anchors.margins: Style.space(16)
      spacing: Style.space(10)
      Text {
        text: (view.envelope ? "Assign to a category, " : "Plan a category, ") + view.monthLabel(view.key)
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.title
      }
      Row {
        width: parent.width
        spacing: Style.space(10)
        Column {
          width: (parent.width - parent.spacing) * 0.6
          spacing: Style.space(4)
          Caption { text: "CATEGORY" }
          SearchableDropdown {
            id: pickCategory
            width: parent.width
            showLabel: false
            options: view.plannable
            placeholderText: "Type to find a category"
            triggerLabel: value === "" ? "Pick one" : currentLabel()
            foreground: view.fg
            accent: view.accent
            fontFamily: view.ff
            onChanged: function (v) { value = v }
          }
        }
        Column {
          width: (parent.width - parent.spacing) * 0.4
          spacing: Style.space(4)
          Caption { text: view.envelope ? "ASSIGN" : "PLANNED" }
          TextField {
            id: pickAmount
            width: parent.width
            foreground: view.fg
            accent: view.accent
            font.family: view.ff
            font.pixelSize: Style.font.body
            placeholderText: "350"
            Keys.onReturnPressed: view.submitPicker()
            Keys.onEnterPressed: view.submitPicker()
            Keys.onEscapePressed: view.closePicker()
          }
        }
      }
      Text {
        text: "A category with nothing on it yet never appears in the table, so this is where it joins the plan."
        color: view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
        width: parent.width
      }
      Row {
        spacing: Style.space(8)
        Button {
          text: "Save   Enter"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.submitPicker()
        }
        Button {
          text: "Cancel   Esc"
          foreground: view.dim
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.closePicker()
        }
      }
    }
  }

  // ---- a category's goal: a target and the month it is due
  Rectangle {
    id: goalCard
    property var row: null
    visible: false
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(420))
    height: goalCol.implicitHeight + Style.space(32)
    radius: Style.cornerRadius
    color: Color.popups.background
    border.width: Style.normalBorderWidth
    border.color: Color.popups.border
    z: 11

    Column {
      id: goalCol
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      anchors.margins: Style.space(16)
      spacing: Style.space(10)
      Text {
        text: goalCard.row ? "Goal for " + goalCard.row.name : ""
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.title
      }
      Row {
        width: parent.width
        spacing: Style.space(10)
        Column {
          width: (parent.width - parent.spacing) * 0.5
          spacing: Style.space(4)
          Caption { text: "TARGET, 0 CLEARS" }
          TextField {
            id: goalTarget
            width: parent.width
            foreground: view.fg
            accent: view.accent
            font.family: view.ff
            font.pixelSize: Style.font.body
            placeholderText: "1200"
            KeyNavigation.tab: goalDue
            Keys.onReturnPressed: view.submitGoal()
            Keys.onEnterPressed: view.submitGoal()
            Keys.onEscapePressed: view.closeGoal()
          }
        }
        Column {
          width: (parent.width - parent.spacing) * 0.5
          spacing: Style.space(4)
          Caption { text: "DUE, YYYY-MM" }
          TextField {
            id: goalDue
            width: parent.width
            foreground: view.fg
            accent: view.accent
            font.family: view.ff
            font.pixelSize: Style.font.body
            placeholderText: "2027-06"
            KeyNavigation.tab: goalTarget
            Keys.onReturnPressed: view.submitGoal()
            Keys.onEnterPressed: view.submitGoal()
            Keys.onEscapePressed: view.closeGoal()
          }
        }
      }
      Text {
        text: "The pot keeps what is left each period and the app suggests what to put in to reach the target in time."
        color: view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
        wrapMode: Text.WordWrap
        width: parent.width
      }
      Row {
        spacing: Style.space(8)
        Button {
          text: "Save   Enter"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.submitGoal()
        }
        Button {
          text: "Cancel   Esc"
          foreground: view.dim
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.closeGoal()
        }
      }
    }
  }

  // ---- one line's plan
  Rectangle {
    id: planner
    property var row: null
    visible: false
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(380))
    height: planCol.implicitHeight + Style.space(32)
    radius: Style.cornerRadius
    color: Color.popups.background
    border.width: Style.normalBorderWidth
    border.color: Color.popups.border
    z: 10

    Column {
      id: planCol
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      anchors.margins: Style.space(16)
      spacing: Style.space(10)
      Text {
        text: planner.row ? planner.row.name + ", " + view.monthLabel(view.key) : ""
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.title
      }
      Caption { text: view.envelope ? "ASSIGN TO THE POT, 0 TAKES IT ALL BACK" : "PLANNED AMOUNT, 0 REMOVES THE LINE" }
      TextField {
        id: planField
        width: parent.width
        foreground: view.fg
        accent: view.accent
        font.family: view.ff
        font.pixelSize: Style.font.body
        placeholderText: "350"
        Keys.onReturnPressed: view.submitPlan()
        Keys.onEnterPressed: view.submitPlan()
        Keys.onEscapePressed: view.closePlan()
      }
      Text {
        visible: !!planner.row
        text: planner.row ? "spent " + view.money(planner.row.spent) + " so far" : ""
        color: view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
      }
      Row {
        spacing: Style.space(8)
        Button {
          text: "Save   Enter"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.submitPlan()
        }
        Button {
          text: "Cancel   Esc"
          foreground: view.dim
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.closePlan()
        }
      }
    }
  }
}
