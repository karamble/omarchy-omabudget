import QtQuick
import qs.Commons
import qs.Ui

// Bills: what is due on the left, the rules behind them on the right. Tab
// moves between the two; Enter posts or edits, s skips, a adds, x removes.
Item {
  id: view
  property var app: null
  readonly property bool formFocused:
    (editor.item ? editor.item.formFocused === true : false)
    || postDate.activeFocus || postAmount.activeFocus || confirm.opened

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  property var due: []
  property var rules: []
  property int dueCursor: 0
  property int ruleCursor: 0
  // Which pane the keys go to.
  property string pane: "due"
  readonly property var currentDue: due.length > 0 ? due[Math.min(dueCursor, due.length - 1)] : null
  readonly property var currentRule: rules.length > 0 ? rules[Math.min(ruleCursor, rules.length - 1)] : null

  function money(minor, currency) { return app ? app.fmt(minor, currency) : String(minor) }

  function reload() {
    if (!app) return
    app.query(["bills", "-days", "60"], function (data, err) {
      view.due = Array.isArray(data) ? data : []
      if (view.dueCursor >= view.due.length) view.dueCursor = Math.max(0, view.due.length - 1)
    })
    app.query(["bill", "list", "-all"], function (data, err) {
      view.rules = Array.isArray(data) ? data : []
      if (view.ruleCursor >= view.rules.length) view.ruleCursor = Math.max(0, view.rules.length - 1)
    })
  }
  onAppChanged: reload()
  Connections {
    target: view.app
    function onChanged() { view.reload() }
  }

  function ruleById(id) {
    for (var i = 0; i < view.rules.length; i++) if (view.rules[i].id === id) return view.rules[i]
    return null
  }

  function state(b) {
    if (b.overdue) return "overdue " + (-b.daysUntil) + (b.daysUntil === -1 ? " day" : " days")
    if (b.daysUntil === 0) return "today"
    if (b.daysUntil === 1) return "tomorrow"
    return "in " + b.daysUntil + " days"
  }

  // ---- actions
  function openEditor(rule) {
    editor.editing = rule
    editor.active = true
  }
  function closeEditor() {
    editor.active = false
    editor.editing = null
    view.forceActiveFocus()
  }
  function openPost(bill) {
    if (!bill) return
    poster.bill = bill
    postDate.text = bill.date
    postAmount.text = view.plain(bill.amount, bill.currency)
    poster.visible = true
    Qt.callLater(function () { (bill.variableAmount ? postAmount : postDate).forceActiveFocus() })
  }
  function closePost() {
    poster.visible = false
    poster.bill = null
    view.forceActiveFocus()
  }
  function submitPost() {
    if (!poster.bill) return
    var argv = ["bill", "post", String(poster.bill.ruleId)]
    if (postDate.text.trim() !== "") argv.push("-date", postDate.text.trim())
    if (postAmount.text.trim() !== "") argv.push("-amount", postAmount.text.trim())
    app.run(argv, "posted " + poster.bill.name)
    view.closePost()
  }
  function plain(minor, currency) {
    var d = app ? app.decimalsFor(currency) : 2
    var s = String(Math.abs(Number(minor) || 0))
    while (s.length <= d) s = "0" + s
    return d > 0 ? s.slice(0, s.length - d) + "." + s.slice(s.length - d) : s
  }
  function skipCurrent() {
    if (!view.currentDue) return
    app.run(["bill", "skip", String(view.currentDue.ruleId)], "skipped " + view.currentDue.name)
  }
  function removeRule(rule) {
    if (!rule) return
    confirm.rule = rule
    confirm.message = "Remove " + rule.name + "? What it already posted stays."
    confirm.opened = true
  }
  function togglePause(rule) {
    if (!rule) return
    var paused = rule.active === false
    app.run(["bill", "edit", String(rule.id), paused ? "-resume" : "-pause"], (paused ? "resumed " : "paused ") + rule.name)
  }
  function editCurrent() {
    var rule = view.pane === "due"
      ? (view.currentDue ? view.ruleById(view.currentDue.ruleId) : null)
      : view.currentRule
    if (rule) view.openEditor(rule)
  }

  // Rows slide under a still pointer when either list refreshes; the gate
  // keeps that from dragging the cursor off what the keyboard chose.
  PointerMoveGate {
    id: pointerGate
    referenceItem: view
  }

  function pointFrom(pane, index, item, mouse) {
    if (view.formFocused) return
    if (!pointerGate.moved(item, mouse)) return
    view.pane = pane
    if (pane === "due") view.dueCursor = index
    else view.ruleCursor = index
  }

  function handleKey(e) {
    if (confirm.opened) { confirm.handleKey(e); return true }
    if (editor.active) {
      if (e.key === Qt.Key_Escape) { view.closeEditor(); return true }
      return false
    }
    if (poster.visible) {
      if (e.key === Qt.Key_Escape) { view.closePost(); return true }
      return false
    }
    if (e.modifiers !== Qt.NoModifier) return false
    switch (e.key) {
    case Qt.Key_Tab:
      view.pane = view.pane === "due" ? "rules" : "due"
      return true
    case Qt.Key_J: case Qt.Key_Down:
      pointerGate.reset()
      if (view.pane === "due") view.dueCursor = Math.min(Math.max(0, view.due.length - 1), view.dueCursor + 1)
      else view.ruleCursor = Math.min(Math.max(0, view.rules.length - 1), view.ruleCursor + 1)
      return true
    case Qt.Key_K: case Qt.Key_Up:
      pointerGate.reset()
      if (view.pane === "due") view.dueCursor = Math.max(0, view.dueCursor - 1)
      else view.ruleCursor = Math.max(0, view.ruleCursor - 1)
      return true
    case Qt.Key_Return: case Qt.Key_Enter:
      if (view.pane === "due") view.openPost(view.currentDue)
      else view.editCurrent()
      return true
    case Qt.Key_P:
      if (view.pane === "due") view.openPost(view.currentDue)
      else view.togglePause(view.currentRule)
      return true
    case Qt.Key_S:
      if (view.pane === "due") view.skipCurrent()
      return true
    case Qt.Key_E: view.editCurrent(); return true
    case Qt.Key_A: view.openEditor(null); return true
    case Qt.Key_X:
      view.removeRule(view.pane === "due" ? (view.currentDue ? view.ruleById(view.currentDue.ruleId) : null) : view.currentRule)
      return true
    }
    return false
  }

  component Caption: Text {
    color: view.dimmer
    font.family: view.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }
  component Pane: Rectangle {
    property bool focused: false
    radius: Style.cornerRadius
    color: "transparent"
    border.width: Style.normalBorderWidth
    border.color: focused ? Style.focusBorderColor : view.border
    clip: true
  }

  Column {
    anchors.fill: parent
    spacing: Style.space(12)

    Row {
      width: parent.width
      spacing: Style.space(12)
      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: "Bills"
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }
      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: view.due.length + " due in sixty days   " + view.rules.length + " rules"
        color: view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
      }
    }

    Row {
      width: parent.width
      height: parent.height - y - footer.height - parent.spacing
      spacing: Style.space(12)

      // ---- due
      Pane {
        id: duePane
        width: (parent.width - parent.spacing) * 0.56
        height: parent.height
        focused: view.pane === "due"

        Item {
          id: dueHead
          width: parent.width
          height: Style.space(30)
          Caption { x: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "DUE" }
          Caption { x: Style.space(12) + Style.space(70); anchors.verticalCenter: parent.verticalCenter; text: "BILL" }
          Caption { anchors.right: parent.right; anchors.rightMargin: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "AMOUNT" }
          Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
        }
        Text {
          anchors.centerIn: parent
          visible: view.due.length === 0
          text: "Nothing due in the next sixty days. Press a to add a bill."
          color: view.dim
          font.family: view.ff
          font.pixelSize: Style.font.body
        }
        ListView {
          id: dueList
          anchors.top: dueHead.bottom
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.bottom: parent.bottom
          clip: true
          model: view.due
          currentIndex: view.dueCursor
          boundsBehavior: Flickable.StopAtBounds
          delegate: CursorSurface {
            id: dueRow
            required property var modelData
            required property int index
            width: dueList.width
            height: Style.space(44)
            readonly property bool selected: view.pane === "due" && index === view.dueCursor
            readonly property bool soon: !modelData.overdue && modelData.daysUntil <= 3
            radius: 0
            hasCursor: dueRow.selected
            fill: Style.selectedAccentFill
            foreground: view.fg
            accent: view.accent

            Text {
              id: dueDate
              x: Style.space(12)
              anchors.verticalCenter: parent.verticalCenter
              width: Style.space(62)
              text: String(dueRow.modelData.date || "").slice(5)
              color: dueRow.modelData.overdue ? (view.app ? view.app.expense : view.fg)
                   : dueRow.soon ? (view.app ? view.app.nearLimit : view.fg) : view.dim
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            Text {
              id: dueIcon
              anchors.left: dueDate.right
              anchors.leftMargin: Style.space(8)
              anchors.verticalCenter: parent.verticalCenter
              width: Style.space(24)
              text: dueRow.modelData.kind === "transfer" ? "" : (dueRow.modelData.categoryIcon || "")
              color: dueRow.selected ? view.accent : view.dim
              font.family: view.ff
              font.pixelSize: Style.font.icon
            }
            Column {
              anchors.left: dueIcon.right
              anchors.leftMargin: Style.space(4)
              anchors.right: dueAmount.left
              anchors.rightMargin: Style.space(8)
              anchors.verticalCenter: parent.verticalCenter
              spacing: Style.space(2)
              Text {
                width: parent.width
                text: dueRow.modelData.name
                color: view.fg
                font.family: view.ff
                font.pixelSize: Style.font.body
                elide: Text.ElideRight
              }
              Text {
                width: parent.width
                text: view.state(dueRow.modelData)
                      + (dueRow.modelData.autoPost ? "  ·  posts itself" : "")
                      + (dueRow.modelData.variableAmount ? "  ·  amount varies" : "")
                      + "  ·  " + (dueRow.modelData.kind === "transfer" ? "transfer" : dueRow.modelData.categoryName)
                      + "  ·  " + dueRow.modelData.accountName
                color: dueRow.modelData.overdue ? (view.app ? view.app.expense : view.dimmer) : view.dimmer
                font.family: view.ff
                font.pixelSize: Style.font.caption
                elide: Text.ElideRight
              }
            }
            Text {
              id: dueAmount
              anchors.right: parent.right
              anchors.rightMargin: Style.space(12)
              anchors.verticalCenter: parent.verticalCenter
              text: view.money(dueRow.modelData.amount, dueRow.modelData.currency)
              color: dueRow.modelData.kind === "income" ? (view.app ? view.app.income : view.fg) : view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            MouseArea {
              id: dueMouse
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onEntered: view.pointFrom("due", dueRow.index, dueRow, { x: dueMouse.mouseX, y: dueMouse.mouseY })
              onPositionChanged: function (mouse) { view.pointFrom("due", dueRow.index, dueRow, mouse) }
              onClicked: { view.pane = "due"; view.dueCursor = dueRow.index }
              onDoubleClicked: { view.pane = "due"; view.dueCursor = dueRow.index; view.openPost(dueRow.modelData) }
            }
          }
        }
      }

      // ---- rules
      Pane {
        id: rulesPane
        width: (parent.width - parent.spacing) * 0.44
        height: parent.height
        focused: view.pane === "rules"

        Item {
          id: rulesHead
          width: parent.width
          height: Style.space(30)
          Caption { x: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "RULES" }
          Caption { anchors.right: parent.right; anchors.rightMargin: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "NEXT" }
          Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
        }
        Text {
          anchors.centerIn: parent
          visible: view.rules.length === 0
          text: "No rules yet. Press a."
          color: view.dim
          font.family: view.ff
          font.pixelSize: Style.font.body
        }
        ListView {
          id: rulesList
          anchors.top: rulesHead.bottom
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.bottom: parent.bottom
          clip: true
          model: view.rules
          currentIndex: view.ruleCursor
          boundsBehavior: Flickable.StopAtBounds
          delegate: CursorSurface {
            id: ruleRow
            required property var modelData
            required property int index
            width: rulesList.width
            height: Style.space(44)
            readonly property bool selected: view.pane === "rules" && index === view.ruleCursor
            readonly property bool paused: modelData.active === false
            readonly property var tpl: modelData.template || ({})
            radius: 0
            hasCursor: ruleRow.selected
            fill: Style.selectedAccentFill
            foreground: view.fg
            accent: view.accent

            Column {
              anchors.left: parent.left
              anchors.leftMargin: Style.space(12)
              anchors.right: ruleNext.left
              anchors.rightMargin: Style.space(8)
              anchors.verticalCenter: parent.verticalCenter
              spacing: Style.space(2)
              Text {
                width: parent.width
                text: ruleRow.modelData.name + (ruleRow.paused ? "  (paused)" : "")
                color: ruleRow.paused ? view.dim : view.fg
                font.family: view.ff
                font.pixelSize: Style.font.body
                elide: Text.ElideRight
              }
              Text {
                width: parent.width
                text: view.money(ruleRow.tpl.amount || 0, ruleRow.tpl.currency)
                      + "  ·  " + ruleRow.modelData.frequency
                      + (ruleRow.modelData.dayRule === "last" ? ", last day" : "")
                      + (ruleRow.modelData.autoPost ? "  ·  auto" : "")
                      + "  ·  posted " + (ruleRow.modelData.posted || 0)
                color: view.dimmer
                font.family: view.ff
                font.pixelSize: Style.font.caption
                elide: Text.ElideRight
              }
            }
            Text {
              id: ruleNext
              anchors.right: parent.right
              anchors.rightMargin: Style.space(12)
              anchors.verticalCenter: parent.verticalCenter
              text: ruleRow.paused ? "" : String(ruleRow.modelData.nextDue || "").slice(5)
              color: view.dim
              font.family: view.ff
              font.pixelSize: Style.font.bodySmall
            }
            MouseArea {
              id: ruleMouse
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onEntered: view.pointFrom("rules", ruleRow.index, ruleRow, { x: ruleMouse.mouseX, y: ruleMouse.mouseY })
              onPositionChanged: function (mouse) { view.pointFrom("rules", ruleRow.index, ruleRow, mouse) }
              onClicked: { view.pane = "rules"; view.ruleCursor = ruleRow.index }
              onDoubleClicked: { view.pane = "rules"; view.ruleCursor = ruleRow.index; view.openEditor(ruleRow.modelData) }
            }
          }
        }
      }
    }

    Text {
      id: footer
      width: parent.width
      text: view.pane === "due"
        ? "j k move   Enter post   s skip   e edit   a new   x remove   Tab rules"
        : "j k move   Enter edit   p pause or resume   a new   x remove   Tab due"
      color: view.dimmer
      font.family: view.ff
      font.pixelSize: Style.font.caption
      elide: Text.ElideRight
    }
  }

  // ---- post one occurrence: the date it was paid and the amount
  Rectangle {
    id: poster
    property var bill: null
    visible: false
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(420))
    height: postCol.implicitHeight + Style.space(32)
    radius: Style.cornerRadius
    color: Color.popups.background
    border.width: Style.normalBorderWidth
    border.color: Color.popups.border
    z: 10

    Column {
      id: postCol
      anchors.left: parent.left
      anchors.right: parent.right
      anchors.top: parent.top
      anchors.margins: Style.space(16)
      spacing: Style.space(10)
      Text {
        text: poster.bill ? "Post " + poster.bill.name : ""
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
          Caption { text: "PAID ON" }
          TextField {
            id: postDate
            width: parent.width
            foreground: view.fg
            accent: view.accent
            font.family: view.ff
            font.pixelSize: Style.font.body
            placeholderText: "YYYY-MM-DD"
            KeyNavigation.tab: postAmount
            Keys.onReturnPressed: view.submitPost()
            Keys.onEnterPressed: view.submitPost()
            Keys.onEscapePressed: view.closePost()
          }
        }
        Column {
          width: (parent.width - parent.spacing) * 0.5
          spacing: Style.space(4)
          Caption { text: "AMOUNT" }
          TextField {
            id: postAmount
            width: parent.width
            foreground: view.fg
            accent: view.accent
            font.family: view.ff
            font.pixelSize: Style.font.body
            KeyNavigation.tab: postDate
            Keys.onReturnPressed: view.submitPost()
            Keys.onEnterPressed: view.submitPost()
            Keys.onEscapePressed: view.closePost()
          }
        }
      }
      Row {
        spacing: Style.space(8)
        Button {
          text: "Post   Enter"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.submitPost()
        }
        Button {
          text: "Cancel   Esc"
          foreground: view.dim
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.closePost()
        }
      }
    }
  }

  ConfirmDialog {
    id: confirm
    property var rule: null
    anchors.fill: parent
    z: 20
    confirmText: "Remove"
    // Cancel is what Enter lands on. The dialog defaults to preselecting
    // Confirm, which on a destructive prompt means a stray Enter destroys.
    selectedIndex: 0
    selectedText: view.app ? view.app.urgent : Color.urgent
    fontFamily: view.ff
    onConfirmed: {
      if (confirm.rule) view.app.run(["bill", "remove", String(confirm.rule.id)], "removed " + confirm.rule.name)
      confirm.opened = false
      confirm.rule = null
      view.forceActiveFocus()
    }
    onCanceled: { confirm.opened = false; confirm.rule = null; view.forceActiveFocus() }
  }

  Loader {
    id: editor
    property var editing: null
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(620))
    active: false
    z: 10
    source: "../components/BillForm.qml"
    onLoaded: {
      item.app = view.app
      item.editing = editor.editing
      item.submitted.connect(function (argv, doneText) {
        view.app.run(argv, doneText)
        view.closeEditor()
      })
      item.cancelled.connect(view.closeEditor)
      Qt.callLater(function () { if (editor.item) editor.item.focusFirst() })
    }
  }
}
