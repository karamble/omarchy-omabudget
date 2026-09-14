import QtQuick
import qs.Commons
import qs.Ui
import "../components"

// The ledger: every transaction, filtered and searched, with a row cursor.
// Enter edits, d deletes with u to undo, b shows the recently deleted.
Item {
  id: view
  property var app: null
  readonly property bool formFocused:
    searchField.activeFocus || kindGroup.activeFocus || accountBox.popupOpen || periodBox.popupOpen
    || (editor.item ? editor.item.formFocused === true : false)
    || bulkTag.activeFocus || bulkCategory.popupOpen

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family
  readonly property int colourMs: app ? app.colourMs : 120

  property var rows: []
  property int cursor: 0
  property bool loading: false
  property string error: ""
  property bool bin: false
  property string kind: ""
  property string account: ""
  property string category: ""
  property string categoryLabel: ""
  property string period: "period"
  property string customFrom: ""
  property string customTo: ""
  property string lastDeleted: ""
  // Marked rows, keyed by id, for the actions that take a handful at once.
  property var marked: ({})
  readonly property var markedIds: Object.keys(view.marked)
  property bool primed: false
  readonly property int pageSize: 200
  readonly property var current: rows.length > 0 ? rows[Math.min(cursor, rows.length - 1)] : null

  function money(minor, currency) { return app ? app.fmt(minor, currency) : String(minor) }

  readonly property var pickableCategories: (app ? app.byRecentUse(app.categories) : [])
    .filter(function (c) { return !c.system && !c.archived })
    .map(function (c) { return { value: c.id, label: (c.parentName ? c.parentName + " / " : "") + c.name } })

  function markup(s) {
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
  }

  // The description, or what stands in for it, with the payee and the tags
  // after it in the dim colour.
  function rowText(row, transfer) {
    var main = row.description && row.description !== ""
      ? row.description
      : (transfer ? "Transfer to " + (app ? app.accountName(row.counterAccountId) : "")
                  : (app ? app.categoryName(row.categoryId) : ""))
    var extra = []
    if (row.payee && row.payee !== "" && row.payee !== main) extra.push(view.markup(row.payee))
    var tags = row.tags || []
    for (var i = 0; i < tags.length; i++) extra.push("#" + view.markup(tags[i]))
    if (extra.length === 0) return view.markup(main)
    return view.markup(main) + " <font color=\"" + view.dimmer + "\">" + extra.join("  ") + "</font>"
  }

  // The date window for the period switch, from the dashboard's months.
  function range() {
    var snap = app ? app.snap : null
    if (!snap || !snap.period) return {}
    var months = snap.months || []
    switch (view.period) {
    case "custom": return { from: view.customFrom, to: view.customTo }
    case "period": return { from: snap.period.from, to: snap.period.to }
    case "last": return months.length > 1 ? { from: months[months.length - 2].from, to: months[months.length - 2].to } : {}
    case "3m": return months.length > 2 ? { from: months[months.length - 3].from } : {}
    case "year": return months.length > 0 ? { from: months[0].from } : {}
    }
    return {}
  }

  // The search box is the whole filter language: tag:weekly, payee:market,
  // >50 and <200 narrow it, everything else is text.
  function parseSearch(raw) {
    var out = { tags: [], payee: "", min: "", max: "", text: "" }
    var words = String(raw || "").trim().split(/\s+/)
    var text = []
    for (var i = 0; i < words.length; i++) {
      var w = words[i]
      if (w === "") continue
      if (w.toLowerCase().indexOf("tag:") === 0) { out.tags.push(w.slice(4)); continue }
      if (w.toLowerCase().indexOf("payee:") === 0) { out.payee = w.slice(6); continue }
      if (w.charAt(0) === ">" && isFinite(Number(w.slice(1)))) { out.min = w.slice(1); continue }
      if (w.charAt(0) === "<" && isFinite(Number(w.slice(1)))) { out.max = w.slice(1); continue }
      text.push(w)
    }
    out.text = text.join(" ")
    return out
  }

  function reload() {
    if (!app) return
    var argv = ["transactions", "-limit", String(view.pageSize)]
    if (view.bin) argv.push("-deleted")
    if (view.kind !== "") argv.push("-kind", view.kind)
    if (view.account !== "") argv.push("-account", view.account)
    if (view.category !== "") argv.push("-category", view.category)
    var terms = view.parseSearch(searchField.text)
    for (var t = 0; t < terms.tags.length; t++) argv.push("-tag", terms.tags[t])
    if (terms.payee !== "") argv.push("-payee", terms.payee)
    if (terms.min !== "") argv.push("-min", terms.min)
    if (terms.max !== "") argv.push("-max", terms.max)
    if (terms.text !== "") argv.push("-q", terms.text)
    var r = view.range()
    if (r.from) argv.push("-from", r.from)
    if (r.to) argv.push("-to", r.to)
    view.loading = true
    app.query(argv, function (data, err) {
      view.loading = false
      view.error = err || ""
      view.rows = Array.isArray(data) ? data : []
      if (view.cursor >= view.rows.length) view.cursor = Math.max(0, view.rows.length - 1)
    })
  }

  // The app arrives after creation, so the first read waits for it.
  onAppChanged: {
    if (app && app.snap) view.primed = true
    view.applyPending()
    view.reload()
  }
  function applyPending() {
    if (!app || !app.pendingFilter) return
    var f = app.pendingFilter
    app.pendingFilter = null
    view.category = f.category || ""
    view.categoryLabel = f.label || view.category
    if (f.from || f.to) {
      view.customFrom = f.from || ""
      view.customTo = f.to || ""
      view.period = "custom"
    }
  }
  function clearCategory() {
    view.category = ""
    view.categoryLabel = ""
    if (view.period === "custom") view.period = "period"
    view.reload()
  }
  Connections {
    target: view.app
    function onChanged() { view.reload() }
    function onSnapChanged() { if (!view.primed && view.app.snap) { view.primed = true; view.reload() } }
    function onPendingFilterChanged() { if (view.app.pendingFilter) { view.applyPending(); view.reload() } }
  }
  onKindChanged: reload()
  onCategoryChanged: reload()
  onAccountChanged: reload()
  onPeriodChanged: reload()
  onBinChanged: reload()

  Timer {
    id: searchDebounce
    interval: 250
    repeat: false
    onTriggered: view.reload()
  }

  // Rows slide under a still pointer whenever this list scrolls or refilters,
  // and the hover events that follow would drag the cursor back from wherever
  // the keyboard just put it. The gate answers whether the pointer moved.
  PointerMoveGate {
    id: pointerGate
    referenceItem: view
  }

  function pointFrom(index, item, mouse) {
    if (view.formFocused) return
    if (!pointerGate.moved(item, mouse)) return
    view.cursor = index
  }

  function move(step) {
    if (view.rows.length === 0) return
    pointerGate.reset()
    view.cursor = Math.max(0, Math.min(view.rows.length - 1, view.cursor + step))
    list.positionViewAtIndex(view.cursor, ListView.Contain)
  }

  function openEditor(row) {
    editor.editing = row
    editor.active = true
  }
  function closeEditor() {
    editor.active = false
    editor.editing = null
  }

  function isMarked(id) { return view.marked[String(id)] === true }

  function toggleMark() {
    if (!view.current) return
    var next = {}
    for (var k in view.marked) next[k] = view.marked[k]
    var id = String(view.current.id)
    if (next[id]) delete next[id]
    else next[id] = true
    view.marked = next
    view.move(1)
  }

  function clearMarks() { view.marked = ({}) }

  // Every action on a handful is one command: the verbs take a list of ids.
  function bulkRun(argv, doneText) {
    if (view.markedIds.length === 0) return
    app.run(argv.concat(view.markedIds), doneText)
    view.clearMarks()
  }

  function bulkDelete() {
    var n = view.markedIds.length
    if (n === 0) return
    if (view.bin) { view.bulkRun(["undo"], "restored " + n); return }
    view.bulkRun(["delete"], "deleted " + n + ", u to undo")
  }

  function bulkCategorise(id) {
    if (id === "") return
    view.bulkRun(["edit", "-category", String(id)],
                 "moved " + view.markedIds.length + " to " + (app ? app.categoryName(id) : id))
  }

  function bulkAddTag() {
    var tag = bulkTag.text.trim()
    if (tag === "") { bulkTag.forceActiveFocus(); return }
    view.bulkRun(["edit", "-add-tag", tag], "tagged " + view.markedIds.length + " " + tag)
    bulkTag.text = ""
  }

  function deleteCurrent() {
    if (!view.current) return
    var id = String(view.current.id)
    if (view.bin) { app.run(["undo", id], "restored"); return }
    view.lastDeleted = id
    app.run(["delete", id], "deleted, u to undo")
  }
  function undo() {
    if (view.lastDeleted === "") return
    app.run(["undo", view.lastDeleted], "restored")
    view.lastDeleted = ""
  }

  function handleKey(e) {
    if (editor.active) {
      if (e.key === Qt.Key_Escape) { view.closeEditor(); return true }
      return false
    }
    if (e.modifiers & ~Qt.ShiftModifier) return false
    switch (e.key) {
    case Qt.Key_J: case Qt.Key_Down: view.move(1); return true
    case Qt.Key_K: case Qt.Key_Up: view.move(-1); return true
    case Qt.Key_PageDown: view.move(15); return true
    case Qt.Key_PageUp: view.move(-15); return true
    case Qt.Key_G:
      view.cursor = (e.modifiers & Qt.ShiftModifier) ? Math.max(0, view.rows.length - 1) : 0
      list.positionViewAtIndex(view.cursor, ListView.Contain)
      return true
    case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_E:
      if (view.current && !view.bin) view.openEditor(view.current)
      return true
    case Qt.Key_A: view.openEditor(null); return true
    case Qt.Key_Space: view.toggleMark(); return true
    case Qt.Key_D:
      if (view.markedIds.length > 0) view.bulkDelete()
      else view.deleteCurrent()
      return true
    case Qt.Key_U: view.undo(); return true
    case Qt.Key_B: view.bin = !view.bin; return true
    case Qt.Key_Slash: searchField.forceActiveFocus(); searchField.selectAll(); return true
    case Qt.Key_Escape:
      if (view.markedIds.length > 0) { view.clearMarks(); return true }
      if (searchField.text !== "") { searchField.text = ""; view.reload(); return true }
      if (view.category !== "") { view.clearCategory(); return true }
      return false
    }
    return false
  }

  component Caption: Text {
    color: view.dimmer
    font.family: view.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }

  Column {
    anchors.fill: parent
    spacing: Style.space(12)

    // ---- header
    Row {
      width: parent.width
      spacing: Style.space(12)
      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: view.bin ? "Recently deleted" : "Transactions"
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }
      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: view.loading ? "loading" : view.rows.length + (view.rows.length >= view.pageSize ? "+" : "") + " rows"
        color: view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
      }
      Button {
        visible: view.category !== ""
        anchors.verticalCenter: parent.verticalCenter
        text: view.categoryLabel + "   Esc"
        tooltipText: "Only this category; Esc shows everything again"
        foreground: view.accent
        accent: view.accent
        fontFamily: view.ff
        fontSize: Style.font.caption
        bordered: true
        onClicked: view.clearCategory()
      }
      Item { width: Math.max(0, parent.width - headerButtons.width - Style.space(24) - parent.spacing * 3
                            - Style.space(200)); height: 1 }
      Row {
        id: headerButtons
        anchors.verticalCenter: parent.verticalCenter
        spacing: Style.space(8)
        Button {
          text: "New   a"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.caption
          bordered: true
          onClicked: view.openEditor(null)
        }
        Button {
          text: view.bin ? "Back to the ledger   b" : "Recycle bin   b"
          foreground: view.bin ? view.accent : view.dim
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.caption
          bordered: true
          selected: view.bin
          onClicked: view.bin = !view.bin
        }
      }
    }

    // ---- filters
    Row {
      width: parent.width
      spacing: Style.space(10)
      TextField {
        id: searchField
        width: Style.space(240)
        anchors.verticalCenter: parent.verticalCenter
        placeholderText: "Search, or tag:weekly payee:market >50   /"
        foreground: view.fg
        accent: view.accent
        font.family: view.ff
        font.pixelSize: Style.font.body
        onTextChanged: searchDebounce.restart()
        Keys.onEscapePressed: { searchField.focus = false; view.forceActiveFocus() }
        Keys.onReturnPressed: { searchField.focus = false; view.reload() }
      }
      ButtonGroup {
        id: kindGroup
        anchors.verticalCenter: parent.verticalCenter
        options: [
          { value: "", label: "All" },
          { value: "expense", label: "Expenses" },
          { value: "income", label: "Income" },
          { value: "transfer", label: "Transfers" }
        ]
        value: view.kind
        foreground: view.fg
        accent: view.accent
        fontFamily: view.ff
        fontSize: Style.font.caption
        focusable: false
        onChanged: function (v) { view.kind = v }
      }
      Dropdown {
        id: accountBox
        anchors.verticalCenter: parent.verticalCenter
        width: Style.space(160)
        showLabel: false
        options: [{ value: "", label: "All accounts" }].concat((view.app ? view.app.accounts : [])
          .map(function (a) { return { value: a.id, label: a.name } }))
        value: view.account
        foreground: view.fg
        accent: view.accent
        fontFamily: view.ff
        onChanged: function (v) { view.account = v }
      }
      Dropdown {
        id: periodBox
        anchors.verticalCenter: parent.verticalCenter
        width: Style.space(150)
        showLabel: false
        options: [
          { value: "period", label: "This period" },
          { value: "last", label: "Last period" },
          { value: "3m", label: "Three periods" },
          { value: "year", label: "Twelve periods" },
          { value: "all", label: "All time" },
          { value: "custom", label: view.customFrom !== "" ? view.customFrom + " to " + view.customTo : "Window" }
        ]
        value: view.period
        foreground: view.fg
        accent: view.accent
        fontFamily: view.ff
        onChanged: function (v) { view.period = v }
      }
    }

    // ---- what to do with the marked rows
    Rectangle {
      width: parent.width
      visible: view.markedIds.length > 0
      height: visible ? bulkRow.implicitHeight + Style.space(16) : 0
      radius: Style.cornerRadius
      color: "transparent"
      border.width: Style.normalBorderWidth
      border.color: view.accent
      Row {
        id: bulkRow
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        anchors.margins: Style.space(10)
        spacing: Style.space(8)
        Text {
          anchors.verticalCenter: parent.verticalCenter
          text: view.markedIds.length + " marked"
          color: view.accent
          font.family: view.ff
          font.pixelSize: Style.font.bodySmall
        }
        SearchableDropdown {
          id: bulkCategory
          width: Style.space(240)
          anchors.verticalCenter: parent.verticalCenter
          showLabel: false
          options: view.pickableCategories
          placeholderText: "Type to find a category"
          triggerLabel: "Move them to"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          onChanged: function (v) { view.bulkCategorise(v); value = "" }
        }
        TextField {
          id: bulkTag
          width: Style.space(160)
          anchors.verticalCenter: parent.verticalCenter
          foreground: view.fg
          accent: view.accent
          font.family: view.ff
          font.pixelSize: Style.font.bodySmall
          placeholderText: "Tag them"
          Keys.onReturnPressed: view.bulkAddTag()
          Keys.onEnterPressed: view.bulkAddTag()
          Keys.onEscapePressed: { bulkTag.text = ""; view.forceActiveFocus() }
        }
        Button {
          anchors.verticalCenter: parent.verticalCenter
          text: view.bin ? "Restore" : "Delete"
          foreground: view.bin ? view.fg : (view.app ? view.app.expense : view.fg)
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.bulkDelete()
        }
        Button {
          anchors.verticalCenter: parent.verticalCenter
          text: "Clear   Esc"
          foreground: view.dim
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.bodySmall
          bordered: true
          onClicked: view.clearMarks()
        }
      }
    }

    // ---- table
    Rectangle {
      width: parent.width
      height: parent.height - y - footer.height - parent.spacing
      radius: Style.cornerRadius
      color: "transparent"
      border.width: Style.normalBorderWidth
      border.color: view.border
      clip: true

      readonly property real dateW: Style.space(84)
      readonly property real iconW: Style.space(28)
      readonly property real amountW: Style.space(120)
      // Always reserved, painted only on hover, so the columns do not shift
      // under the pointer the way an overlay would.
      readonly property real actionsW: Style.space(56)
      readonly property real accountW: Style.space(110)
      readonly property real categoryW: Style.space(170)
      readonly property real pad: Style.space(12)
      readonly property real descW: Math.max(Style.space(80), width - pad * 2 - dateW - iconW - amountW - accountW - categoryW - actionsW - Style.space(8) * 6)

      Item {
        id: head
        width: parent.width
        height: Style.space(30)
        Row {
          anchors.fill: parent
          anchors.leftMargin: parent.parent.pad
          anchors.rightMargin: parent.parent.pad
          spacing: Style.space(8)
          Caption { width: parent.parent.parent.dateW; text: "DATE"; anchors.verticalCenter: parent.verticalCenter }
          Item { width: parent.parent.parent.iconW; height: 1 }
          Caption { width: parent.parent.parent.descW; text: "DESCRIPTION"; anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.categoryW; text: "CATEGORY"; anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.accountW; text: "ACCOUNT"; anchors.verticalCenter: parent.verticalCenter }
          Caption { width: parent.parent.parent.amountW; text: "AMOUNT"; horizontalAlignment: Text.AlignRight; anchors.verticalCenter: parent.verticalCenter }
          Item { width: parent.parent.parent.actionsW; height: Style.spacing.hairline }
        }
        Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
      }

      Text {
        anchors.centerIn: parent
        visible: view.rows.length === 0 && !view.loading
        text: view.error !== "" ? view.error
            : view.bin ? "Nothing deleted in the last 30 days."
            : "Nothing here. Press n to add one, or widen the period."
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
          id: rowItem
          required property var modelData
          required property int index
          width: list.width
          height: Style.space(30)
          readonly property bool selected: index === view.cursor
          readonly property bool transfer: modelData.kind === "transfer"
          readonly property bool marked: view.isMarked(modelData.id)
          // Square: the cursor chrome rounds its corners by default, which
          // suits a card and not a row in a dense table.
          radius: 0
          hasCursor: rowItem.selected
          fill: Style.selectedAccentFill
          foreground: view.fg
          accent: view.accent
          // A mark is a fact about the row, not a cursor, so it keeps its own
          // tint underneath rather than competing for the same paint.
          color: rowItem.selected ? fill
               : rowItem.marked ? Util.alpha(view.accent, 0.22)
               : "transparent"

          // A marked row also carries an edge, so the mark reads at a glance
          // and does not depend on a tint the theme may barely show.
          Rectangle {
            anchors.left: parent.left
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            width: Style.space(3)
            visible: rowItem.marked
            color: view.accent
          }

          Row {
            anchors.fill: parent
            anchors.leftMargin: list.table.pad
            anchors.rightMargin: list.table.pad
            spacing: Style.space(8)
            Text {
              width: list.table.dateW
              anchors.verticalCenter: parent.verticalCenter
              text: String(rowItem.modelData.date || "").slice(5)
              color: view.dimmer
              font.family: view.ff
              font.pixelSize: Style.font.bodySmall
            }
            Text {
              width: list.table.iconW
              anchors.verticalCenter: parent.verticalCenter
              text: rowItem.transfer ? "" : (view.app ? view.app.categoryIcon(rowItem.modelData.categoryId) : "")
              color: rowItem.selected ? view.accent : view.dim
              font.family: view.ff
              font.pixelSize: Style.font.icon
            }
            Text {
              width: list.table.descW
              anchors.verticalCenter: parent.verticalCenter
              // The payee and the tags ride along in the same cell, dimmed,
              // because a tag you cannot see is a tag you will not use.
              text: view.rowText(rowItem.modelData, rowItem.transfer)
              textFormat: Text.StyledText
              color: view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
              elide: Text.ElideRight
            }
            Text {
              width: list.table.categoryW
              anchors.verticalCenter: parent.verticalCenter
              text: rowItem.transfer ? "Transfer"
                  : (rowItem.modelData.splits && rowItem.modelData.splits.length > 0) ? "Split, " + rowItem.modelData.splits.length + " lines"
                  : (view.app ? view.app.categoryName(rowItem.modelData.categoryId) : "")
              color: view.dim
              font.family: view.ff
              font.pixelSize: Style.font.bodySmall
              elide: Text.ElideRight
            }
            Text {
              width: list.table.accountW
              anchors.verticalCenter: parent.verticalCenter
              text: view.app ? view.app.accountName(rowItem.modelData.accountId) : ""
              color: view.dim
              font.family: view.ff
              font.pixelSize: Style.font.bodySmall
              elide: Text.ElideRight
            }
            Text {
              width: list.table.amountW
              anchors.verticalCenter: parent.verticalCenter
              horizontalAlignment: Text.AlignRight
              text: (rowItem.modelData.amount > 0 ? "+" : "") + view.money(rowItem.modelData.amount, rowItem.modelData.currency)
              color: rowItem.transfer ? view.dim
                   : rowItem.modelData.amount < 0 ? (view.app ? view.app.expense : view.fg)
                   : (view.app ? view.app.income : view.fg)
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            Row {
              id: rowActions
              width: list.table.actionsW
              height: parent.height
              spacing: Style.space(2)
              visible: opacity > 0
              opacity: rowHover.hovered ? 1 : 0
              Behavior on opacity { NumberAnimation { duration: view.colourMs; easing.type: Easing.OutCubic } }
              RowAction {
                app: view.app
                anchors.verticalCenter: parent.verticalCenter
                size: Style.space(26)
                glyph: view.bin ? "󰑐" : "󰏫"
                tint: view.accent
                hint: view.bin ? "Bring it back" : "Edit this transaction"
                onTriggered: {
                  view.cursor = rowItem.index
                  // In the bin the same verb restores, which is what the d
                  // key does there too.
                  if (view.bin) view.deleteCurrent()
                  else view.openEditor(rowItem.modelData)
                }
              }
              RowAction {
                app: view.app
                anchors.verticalCenter: parent.verticalCenter
                size: Style.space(26)
                visible: !view.bin
                glyph: "󰩺"
                tint: view.app ? view.app.expense : view.dim
                hint: "Delete, with u to undo"
                onTriggered: { view.cursor = rowItem.index; view.deleteCurrent() }
              }
            }
          }
          // Hover for the whole row, children included: a child MouseArea
          // takes it from the row's own, which would make these flicker.
          HoverHandler { id: rowHover }
          MouseArea {
            id: rowMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onEntered: view.pointFrom(rowItem.index, rowItem, { x: rowMouse.mouseX, y: rowMouse.mouseY })
            onPositionChanged: function (mouse) { view.pointFrom(rowItem.index, rowItem, mouse) }
            onClicked: view.cursor = rowItem.index
            onDoubleClicked: { view.cursor = rowItem.index; if (!view.bin) view.openEditor(rowItem.modelData) }
          }
        }
      }
    }

    // ---- footer
    Text {
      id: footer
      width: parent.width
      text: view.bin
        ? "j k move   Enter or d restore   b back to the ledger   / search"
        : view.markedIds.length > 0
          ? view.markedIds.length + " marked   Space mark   d delete them   Esc clear the marks"
          : "j k move   Enter edit   a new   Space mark   d delete   u undo   b recycle bin   / search, with tag: payee: >50 <200"
      color: view.dimmer
      font.family: view.ff
      font.pixelSize: Style.font.caption
      elide: Text.ElideRight
    }
  }

  // The form floats over the table.
  Loader {
    id: editor
    property var editing: null
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(540))
    active: false
    z: 10
    source: "../components/TransactionForm.qml"
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
