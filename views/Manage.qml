import QtQuick
import qs.Commons
import qs.Ui

// Manage: the taxonomy behind every other screen. Payees, tags and rates get
// their own panes here in time, which is why the pane list exists.
Item {
  id: view
  property var app: null
  readonly property bool formFocused:
    searchField.activeFocus || confirm.opened || (editor.item ? editor.item.formFocused === true : false)
    || currencyField.activeFocus || rateField.activeFocus || rateDateField.activeFocus
    || (arming.item ? arming.item.formFocused === true : false)
    || (payeeEditor.item ? payeeEditor.item.formFocused === true : false)

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property var panes: ["Categories", "Payees", "Rates", "Alerts"]
  property string pane: "Categories"

  // Every category, archived ones included: this is where they are restored.
  property var all: []
  property var rows: []
  property int cursor: 0
  readonly property var current: rows.length > 0 ? rows[Math.min(cursor, rows.length - 1)].cat : null

  function reload() {
    if (!app) return
    app.query(["categories"], function (data, err) {
      if (err) { view.app.lastError = err; return }
      view.all = Array.isArray(data) ? data : []
    })
  }
  onAppChanged: { reload(); reloadRates(); reloadAlerts(); reloadPayees() }
  Connections {
    target: view.app
    function onChanged() { view.reload(); view.reloadRates(); view.reloadAlerts(); view.reloadPayees() }
  }

  // The daemon orders by the seed's sort keys, which interleaves the system
  // markers with the groups; the tree is rebuilt here instead.
  function buildRows() {
    var groups = [], children = ({}), system = []
    for (var i = 0; i < view.all.length; i++) {
      var c = view.all[i]
      if (c.system) { system.push(c); continue }
      if (c.parentId) {
        if (!children[c.parentId]) children[c.parentId] = []
        children[c.parentId].push(c)
      } else {
        groups.push(c)
      }
    }
    var order = function (a, b) {
      if (a.sortOrder !== b.sortOrder) return a.sortOrder - b.sortOrder
      return a.name.localeCompare(b.name)
    }
    groups.sort(order)
    var needle = searchField.text.trim().toLowerCase()
    var hit = function (c) {
      return needle === "" || String(c.name).toLowerCase().indexOf(needle) !== -1
    }
    var out = []
    for (var g = 0; g < groups.length; g++) {
      var kids = (children[groups[g].id] || []).slice().sort(order)
      var keep = []
      for (var k = 0; k < kids.length; k++)
        if (hit(groups[g]) || hit(kids[k])) keep.push(kids[k])
      if (!hit(groups[g]) && keep.length === 0) continue
      out.push({ cat: groups[g], child: false })
      for (var m = 0; m < keep.length; m++) out.push({ cat: keep[m], child: true })
    }
    for (var s = 0; s < system.length; s++)
      if (hit(system[s])) out.push({ cat: system[s], child: false })
    return out
  }

  function rebuild() {
    view.rows = view.buildRows()
    if (view.cursor >= view.rows.length) view.cursor = Math.max(0, view.rows.length - 1)
  }
  onAllChanged: rebuild()

  readonly property int archivedCount: {
    var n = 0
    for (var i = 0; i < view.all.length; i++) if (view.all[i].archived) n++
    return n
  }

  // ---- actions
  function openEditor(cat) {
    if (cat && cat.system) { view.app.lastError = cat.name + " is a system category"; return }
    editor.editing = cat
    editor.parentHint = cat ? String(cat.parentId || cat.id) : ""
    editor.active = true
  }
  function closeEditor() {
    editor.active = false
    editor.editing = null
    view.forceActiveFocus()
  }
  function toggleArchived(cat) {
    if (!cat) return
    if (cat.system) { view.app.lastError = cat.name + " is a system category"; return }
    var back = cat.archived === true
    app.run(["category", back ? "restore" : "archive", String(cat.id)],
            (back ? "restored " : "archived ") + cat.name)
  }
  // ---- rates, spec 8: entered here, never fetched
  property var rates: []
  property int rateCursor: 0
  readonly property var currentRate: rates.length > 0 ? rates[Math.min(rateCursor, rates.length - 1)] : null
  // Rates are quoted against the reference, whatever figures are shown in.
  readonly property string base: app ? app.rateReference : ""

  function reloadRates() {
    if (!app) return
    app.query(["rate", "list"], function (data, err) {
      if (err) { view.app.lastError = err; return }
      view.rates = Array.isArray(data) ? data : []
      if (view.rateCursor >= view.rates.length) view.rateCursor = Math.max(0, view.rates.length - 1)
    })
  }

  function addRate() {
    var cur = currencyField.text.trim().toUpperCase()
    var rate = rateField.text.trim()
    if (cur === "") { currencyField.forceActiveFocus(); return }
    if (rate === "") { rateField.forceActiveFocus(); return }
    var argv = ["rate", "set", cur, rate]
    if (rateDateField.text.trim() !== "") argv.push("-date", rateDateField.text.trim())
    app.run(argv, "1 " + cur + " = " + rate + " " + view.base)
    currencyField.text = ""
    rateField.text = ""
    rateDateField.text = ""
  }

  function removeRate(r) {
    if (!r) return
    app.run(["rate", "remove", String(r.currency), String(r.date)], "removed the " + r.currency + " rate")
  }

  // ---- payees, spec 1.5
  property var payees: []
  property int payeeCursor: 0
  readonly property var currentPayee: payees.length > 0 ? payees[Math.min(payeeCursor, payees.length - 1)] : null

  function reloadPayees() {
    if (!app) return
    app.query(["payees"], function (data, err) {
      view.payees = Array.isArray(data) ? data : []
      if (view.payeeCursor >= view.payees.length) view.payeeCursor = Math.max(0, view.payees.length - 1)
    })
  }
  function openPayee(p) {
    if (!p) return
    payeeEditor.editing = p
    payeeEditor.active = true
  }
  function closePayee() {
    payeeEditor.active = false
    payeeEditor.editing = null
    view.forceActiveFocus()
  }

  // ---- alerts, spec 9: a watch on one of the catalogue's paths
  property var armed: []
  property var catalogue: []
  property int alertCursor: 0
  readonly property var currentAlert: armed.length > 0 ? armed[Math.min(alertCursor, armed.length - 1)] : null

  function reloadAlerts() {
    if (!app) return
    app.query(["alerts"], function (data, err) {
      view.armed = Array.isArray(data) ? data : []
      if (view.alertCursor >= view.armed.length) view.alertCursor = Math.max(0, view.armed.length - 1)
    })
    app.query(["catalogue"], function (data, err) {
      view.catalogue = Array.isArray(data) ? data : []
    })
  }

  // What the watch is doing, from the state the engine keeps on it.
  function alertState(t) {
    if (!t) return ""
    if (t.expiresAt && new Date(t.expiresAt) < new Date()) return "expired"
    var st = t.state || ({})
    if (st.firedAt && !t.standing) return "fired"
    if (st.firedAt && st.ready === false) return "rearming"
    if (st.primed === false) return "learning"
    return "armed"
  }

  function openArming(t) {
    arming.editing = t
    arming.active = true
  }
  function closeArming() {
    arming.active = false
    arming.editing = null
    view.forceActiveFocus()
  }
  function disarm(t) {
    if (!t) return
    app.run(["disarm", String(t.id)], "disarmed " + t.path)
  }

  function askRemove(cat) {
    if (!cat) return
    confirm.cat = cat
    confirm.message = "Remove " + cat.name + "? Only a category nothing points at can go."
    confirm.opened = true
  }

  PointerMoveGate {
    id: pointerGate
    referenceItem: view
  }

  function pointFrom(pane, index, item, mouse) {
    if (view.formFocused) return
    if (!pointerGate.moved(item, mouse)) return
    view.pane = pane
    if (pane === "Categories") view.cursor = index
    else if (pane === "Rates") view.rateCursor = index
    else if (pane === "Payees") view.payeeCursor = index
    else view.alertCursor = index
  }

  function handleKey(e) {
    if (confirm.opened) { confirm.handleKey(e); return true }
    if (editor.active) {
      if (e.key === Qt.Key_Escape) { view.closeEditor(); return true }
      return false
    }
    if (arming.active) {
      if (e.key === Qt.Key_Escape) { view.closeArming(); return true }
      return false
    }
    if (payeeEditor.active) {
      if (e.key === Qt.Key_Escape) { view.closePayee(); return true }
      return false
    }
    if (e.key === Qt.Key_Tab && e.modifiers === Qt.NoModifier) {
      view.pane = view.panes[(view.panes.indexOf(view.pane) + 1) % view.panes.length]
      return true
    }
    if (e.modifiers !== Qt.NoModifier) return false
    if (view.pane === "Payees") {
      switch (e.key) {
      case Qt.Key_J: case Qt.Key_Down:
        pointerGate.reset()
        if (view.payees.length) view.payeeCursor = Math.min(view.payees.length - 1, view.payeeCursor + 1)
        return true
      case Qt.Key_K: case Qt.Key_Up:
        pointerGate.reset()
        view.payeeCursor = Math.max(0, view.payeeCursor - 1)
        return true
      case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_E:
        view.openPayee(view.currentPayee)
        return true
      }
      return false
    }
    if (view.pane === "Alerts") {
      switch (e.key) {
      case Qt.Key_J: case Qt.Key_Down:
        pointerGate.reset()
        if (view.armed.length) view.alertCursor = Math.min(view.armed.length - 1, view.alertCursor + 1)
        return true
      case Qt.Key_K: case Qt.Key_Up:
        pointerGate.reset()
        view.alertCursor = Math.max(0, view.alertCursor - 1)
        return true
      case Qt.Key_A: view.openArming(null); return true
      case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_E:
        view.openArming(view.currentAlert)
        return true
      case Qt.Key_X: view.disarm(view.currentAlert); return true
      }
      return false
    }
    if (view.pane === "Rates") {
      switch (e.key) {
      case Qt.Key_J: case Qt.Key_Down:
        pointerGate.reset()
        if (view.rates.length) view.rateCursor = Math.min(view.rates.length - 1, view.rateCursor + 1)
        return true
      case Qt.Key_K: case Qt.Key_Up:
        pointerGate.reset()
        view.rateCursor = Math.max(0, view.rateCursor - 1)
        return true
      case Qt.Key_A: currencyField.forceActiveFocus(); return true
      case Qt.Key_X: view.removeRate(view.currentRate); return true
      }
      return false
    }
    switch (e.key) {
    case Qt.Key_J: case Qt.Key_Down:
      pointerGate.reset()
      if (view.rows.length) view.cursor = Math.min(view.rows.length - 1, view.cursor + 1)
      return true
    case Qt.Key_K: case Qt.Key_Up:
      pointerGate.reset()
      view.cursor = Math.max(0, view.cursor - 1)
      return true
    case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_E:
      view.openEditor(view.current)
      return true
    case Qt.Key_A:
      editor.editing = null
      editor.parentHint = view.current ? String(view.current.parentId || view.current.id) : ""
      editor.active = true
      return true
    case Qt.Key_X: view.toggleArchived(view.current); return true
    case Qt.Key_Slash: searchField.forceActiveFocus(); searchField.selectAll(); return true
    case Qt.Key_Escape:
      if (searchField.text !== "") { searchField.text = ""; view.rebuild(); return true }
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
  component Badge: Text {
    color: view.dimmer
    font.family: view.ff
    font.pixelSize: Style.font.caption
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
        text: "Manage"
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }
      ButtonGroup {
        visible: view.panes.length > 1
        anchors.verticalCenter: parent.verticalCenter
        options: view.panes.map(function (p) { return { value: p, label: p } })
        value: view.pane
        foreground: view.fg
        accent: view.accent
        fontFamily: view.ff
        fontSize: Style.font.caption
        focusable: false
        onChanged: function (v) { view.pane = v }
      }
      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: view.pane === "Payees"
          ? view.payees.length + (view.payees.length === 1 ? " payee" : " payees")
          : view.pane === "Alerts"
          ? view.armed.length + " armed" + (view.app && view.app.snap && view.app.snap.monitoring === false ? ", evaluation is off" : "")
          : view.pane === "Rates"
          ? view.rates.length + " on file, in " + view.base
          : view.all.length + " categories" + (view.archivedCount > 0 ? ", " + view.archivedCount + " archived" : "")
        color: view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      visible: view.pane === "Categories"
      TextField {
        id: searchField
        width: Style.space(240)
        anchors.verticalCenter: parent.verticalCenter
        placeholderText: "Search   /"
        foreground: view.fg
        accent: view.accent
        font.family: view.ff
        font.pixelSize: Style.font.body
        onTextChanged: view.rebuild()
        Keys.onEscapePressed: { searchField.text = ""; view.forceActiveFocus() }
        Keys.onReturnPressed: { searchField.focus = false; view.forceActiveFocus() }
      }
      Button {
        anchors.verticalCenter: parent.verticalCenter
        text: "New   a"
        foreground: view.fg
        accent: view.accent
        fontFamily: view.ff
        fontSize: Style.font.caption
        bordered: true
        onClicked: {
          editor.editing = null
          editor.parentHint = view.current ? String(view.current.parentId || view.current.id) : ""
          editor.active = true
        }
      }
    }

    // ---- the tree
    Rectangle {
      visible: view.pane === "Categories"
      width: parent.width
      height: parent.height - y - footer.height - parent.spacing
      radius: Style.cornerRadius
      color: "transparent"
      border.width: Style.normalBorderWidth
      border.color: view.border
      clip: true

      Item {
        id: head
        width: parent.width
        height: Style.space(30)
        Caption { x: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "CATEGORY" }
        Caption {
          anchors.right: parent.right
          anchors.rightMargin: Style.space(12)
          anchors.verticalCenter: parent.verticalCenter
          text: "POT AND FLAGS"
        }
        Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
      }

      Text {
        anchors.centerIn: parent
        visible: view.rows.length === 0
        text: view.all.length === 0 ? "Loading" : "Nothing matches. Press a to add a category."
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
        delegate: CursorSurface {
          id: row
          required property var modelData
          required property int index
          readonly property var cat: modelData.cat
          readonly property bool selected: view.pane === "Categories" && index === view.cursor
          readonly property bool muted: cat.archived === true || cat.system === true
          width: list.width
          height: Style.space(28)
          radius: 0
          hasCursor: row.selected
          fill: Style.selectedAccentFill
          foreground: view.fg
          accent: view.accent

          Text {
            id: glyph
            x: Style.space(12) + (row.modelData.child ? Style.space(22) : 0)
            anchors.verticalCenter: parent.verticalCenter
            width: Style.space(26)
            text: row.cat.icon ? row.cat.icon : ""
            color: row.selected ? view.accent : view.dim
            font.family: view.ff
            font.pixelSize: Style.font.icon
          }
          Text {
            anchors.left: glyph.right
            anchors.right: badges.left
            anchors.rightMargin: Style.space(8)
            anchors.verticalCenter: parent.verticalCenter
            text: row.cat.name
            color: row.muted ? view.dim : view.fg
            font.family: view.ff
            font.pixelSize: row.modelData.child ? Style.font.bodySmall : Style.font.body
            elide: Text.ElideRight
          }
          Row {
            id: badges
            anchors.right: parent.right
            anchors.rightMargin: Style.space(12)
            anchors.verticalCenter: parent.verticalCenter
            spacing: Style.space(10)
            Badge {
              visible: !row.modelData.child && !row.cat.system
              text: row.cat.kind
            }
            Badge {
              visible: !row.cat.system && row.cat.behaviour !== "monthly"
              text: row.cat.behaviour
              color: view.dim
            }
            Badge {
              visible: row.cat.goalTarget > 0
              text: "goal " + (view.app ? view.app.fmt(row.cat.goalTarget, view.app.snap ? view.app.snap.baseCurrency : "") : "")
                    + (row.cat.goalDue ? " by " + row.cat.goalDue : "")
              color: view.dim
            }
            Badge {
              visible: row.cat.excludedFromStatistics === true
              text: "not in statistics"
            }
            Badge {
              visible: row.cat.archived === true
              text: "archived"
              color: view.app ? view.app.nearLimit : view.dimmer
            }
            Badge {
              visible: row.cat.system === true
              text: "system"
            }
          }
          MouseArea {
            id: rowMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onEntered: view.pointFrom("Categories", row.index, row, { x: rowMouse.mouseX, y: rowMouse.mouseY })
            onPositionChanged: function (mouse) { view.pointFrom("Categories", row.index, row, mouse) }
            onClicked: view.cursor = row.index
            onDoubleClicked: { view.cursor = row.index; view.openEditor(row.cat) }
          }
        }
      }
    }

    // ---- rates
    Column {
      visible: view.pane === "Rates"
      width: parent.width
      spacing: Style.space(10)

      Row {
        width: parent.width
        spacing: Style.space(8)
        TextField {
          id: currencyField
          width: Style.space(90)
          anchors.verticalCenter: parent.verticalCenter
          placeholderText: "USD"
          maximumLength: 3
          foreground: view.fg
          accent: view.accent
          font.family: view.ff
          font.pixelSize: Style.font.body
          Keys.onReturnPressed: rateField.forceActiveFocus()
          Keys.onEscapePressed: view.forceActiveFocus()
        }
        TextField {
          id: rateField
          width: Style.space(120)
          anchors.verticalCenter: parent.verticalCenter
          placeholderText: "0.92"
          foreground: view.fg
          accent: view.accent
          font.family: view.ff
          font.pixelSize: Style.font.body
          Keys.onReturnPressed: view.addRate()
          Keys.onEnterPressed: view.addRate()
          Keys.onEscapePressed: view.forceActiveFocus()
        }
        TextField {
          id: rateDateField
          width: Style.space(160)
          anchors.verticalCenter: parent.verticalCenter
          placeholderText: "YYYY-MM-DD, today if empty"
          foreground: view.fg
          accent: view.accent
          font.family: view.ff
          font.pixelSize: Style.font.body
          Keys.onReturnPressed: view.addRate()
          Keys.onEnterPressed: view.addRate()
          Keys.onEscapePressed: view.forceActiveFocus()
        }
        Button {
          anchors.verticalCenter: parent.verticalCenter
          text: "Add   a"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.caption
          bordered: true
          onClicked: view.addRate()
        }
        Text {
          anchors.verticalCenter: parent.verticalCenter
          text: view.base !== "" ? "1 of that currency, in " + view.base : ""
          color: view.dimmer
          font.family: view.ff
          font.pixelSize: Style.font.caption
        }
      }

      Rectangle {
        width: parent.width
        height: view.height - y - footer.height - Style.space(60)
        radius: Style.cornerRadius
        color: "transparent"
        border.width: Style.normalBorderWidth
        border.color: view.border
        clip: true

        Item {
          id: rateHead
          width: parent.width
          height: Style.space(30)
          Caption { x: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "FROM" }
          Caption { x: Style.space(140); anchors.verticalCenter: parent.verticalCenter; text: "CURRENCY" }
          Caption { x: Style.space(260); anchors.verticalCenter: parent.verticalCenter; text: "RATE" }
          Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
        }
        Text {
          anchors.centerIn: parent
          visible: view.rates.length === 0
          width: parent.width - Style.space(40)
          horizontalAlignment: Text.AlignHCenter
          wrapMode: Text.WordWrap
          text: "No rates yet. An account in another currency needs one to be counted in liquid funds and net worth; without it every entry has to carry its own."
          color: view.dim
          font.family: view.ff
          font.pixelSize: Style.font.body
        }
        ListView {
          anchors.top: rateHead.bottom
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.bottom: parent.bottom
          clip: true
          model: view.rates
          currentIndex: view.rateCursor
          boundsBehavior: Flickable.StopAtBounds
          delegate: CursorSurface {
            id: rateRow
            required property var modelData
            required property int index
            width: ListView.view.width
            height: Style.space(28)
            readonly property bool selected: view.pane === "Rates" && index === view.rateCursor
            radius: 0
            hasCursor: rateRow.selected
            fill: Style.selectedAccentFill
            foreground: view.fg
            accent: view.accent
            Text {
              x: Style.space(12)
              anchors.verticalCenter: parent.verticalCenter
              text: rateRow.modelData.date
              color: view.dim
              font.family: view.ff
              font.pixelSize: Style.font.bodySmall
            }
            Text {
              x: Style.space(140)
              anchors.verticalCenter: parent.verticalCenter
              text: rateRow.modelData.currency
              color: view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            Text {
              x: Style.space(260)
              anchors.verticalCenter: parent.verticalCenter
              text: "1 " + rateRow.modelData.currency + " = " + rateRow.modelData.rate + " " + view.base
              color: view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
            MouseArea {
              id: rateRowMouse
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onEntered: view.pointFrom("Rates", rateRow.index, rateRow, { x: rateRowMouse.mouseX, y: rateRowMouse.mouseY })
              onPositionChanged: function (mouse) { view.pointFrom("Rates", rateRow.index, rateRow, mouse) }
              onClicked: view.rateCursor = rateRow.index
              onDoubleClicked: { view.rateCursor = rateRow.index; view.removeRate(rateRow.modelData) }
            }
          }
        }
      }
    }

    // ---- payees
    Rectangle {
      visible: view.pane === "Payees"
      width: parent.width
      height: parent.height - y - footer.height - parent.spacing
      radius: Style.cornerRadius
      color: "transparent"
      border.width: Style.normalBorderWidth
      border.color: view.border
      clip: true

      Item {
        id: payeeHead
        width: parent.width
        height: Style.space(30)
        Caption { x: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "PAYEE" }
        Caption {
          anchors.right: parent.right
          anchors.rightMargin: Style.space(12)
          anchors.verticalCenter: parent.verticalCenter
          text: "ON"
        }
        Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
      }
      Text {
        anchors.centerIn: parent
        visible: view.payees.length === 0
        width: parent.width - Style.space(40)
        horizontalAlignment: Text.AlignHCenter
        wrapMode: Text.WordWrap
        text: "No payees yet. Name one on a transaction and it appears here, where it can be renamed, given the spellings a statement uses, or folded into another."
        color: view.dim
        font.family: view.ff
        font.pixelSize: Style.font.body
      }
      ListView {
        anchors.top: payeeHead.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        clip: true
        model: view.payees
        currentIndex: view.payeeCursor
        boundsBehavior: Flickable.StopAtBounds
        delegate: CursorSurface {
          id: payeeRow
          required property var modelData
          required property int index
          width: ListView.view.width
          height: Style.space(40)
          readonly property bool selected: view.pane === "Payees" && index === view.payeeCursor
          radius: 0
          hasCursor: payeeRow.selected
          fill: Style.selectedAccentFill
          foreground: view.fg
          accent: view.accent

          Column {
            anchors.left: parent.left
            anchors.leftMargin: Style.space(12)
            anchors.right: uses.left
            anchors.rightMargin: Style.space(8)
            anchors.verticalCenter: parent.verticalCenter
            spacing: Style.space(2)
            Text {
              width: parent.width
              text: payeeRow.modelData.name
              color: view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
              elide: Text.ElideRight
            }
            Text {
              width: parent.width
              visible: text !== ""
              text: (payeeRow.modelData.defaultCategoryId
                      ? "usually " + (view.app ? view.app.categoryName(payeeRow.modelData.defaultCategoryId) : "") : "")
                    + (payeeRow.modelData.aliases && payeeRow.modelData.aliases.length > 0
                      ? (payeeRow.modelData.defaultCategoryId ? "  ·  " : "") + "also " + payeeRow.modelData.aliases.join(", ") : "")
              color: view.dimmer
              font.family: view.ff
              font.pixelSize: Style.font.caption
              elide: Text.ElideRight
            }
          }
          Text {
            id: uses
            anchors.right: parent.right
            anchors.rightMargin: Style.space(12)
            anchors.verticalCenter: parent.verticalCenter
            text: payeeRow.modelData.uses
            color: view.dim
            font.family: view.ff
            font.pixelSize: Style.font.body
          }
          MouseArea {
            id: payeeRowMouse
            anchors.fill: parent
            hoverEnabled: true
            cursorShape: Qt.PointingHandCursor
            onEntered: view.pointFrom("Payees", payeeRow.index, payeeRow, { x: payeeRowMouse.mouseX, y: payeeRowMouse.mouseY })
            onPositionChanged: function (mouse) { view.pointFrom("Payees", payeeRow.index, payeeRow, mouse) }
            onClicked: view.payeeCursor = payeeRow.index
            onDoubleClicked: { view.payeeCursor = payeeRow.index; view.openPayee(payeeRow.modelData) }
          }
        }
      }
    }

    // ---- alerts
    Column {
      visible: view.pane === "Alerts"
      width: parent.width
      spacing: Style.space(10)

      Row {
        width: parent.width
        spacing: Style.space(10)
        Button {
          text: "Arm a watch   a"
          foreground: view.fg
          accent: view.accent
          fontFamily: view.ff
          fontSize: Style.font.caption
          bordered: true
          onClicked: view.openArming(null)
        }
        Text {
          anchors.verticalCenter: parent.verticalCenter
          text: "Alarms go to the desktop. Evaluation is switched on in Settings."
          color: view.dimmer
          font.family: view.ff
          font.pixelSize: Style.font.caption
        }
      }

      Rectangle {
        width: parent.width
        height: view.height - y - footer.height - Style.space(60)
        radius: Style.cornerRadius
        color: "transparent"
        border.width: Style.normalBorderWidth
        border.color: view.border
        clip: true

        Item {
          id: alertHead
          width: parent.width
          height: Style.space(30)
          Caption { x: Style.space(12); anchors.verticalCenter: parent.verticalCenter; text: "WATCHING" }
          Caption {
            anchors.right: parent.right
            anchors.rightMargin: Style.space(12)
            anchors.verticalCenter: parent.verticalCenter
            text: "STATE"
          }
          Rectangle { anchors.bottom: parent.bottom; width: parent.width; height: Style.spacing.hairline; color: view.border }
        }
        Text {
          anchors.centerIn: parent
          visible: view.armed.length === 0
          width: parent.width - Style.space(40)
          horizontalAlignment: Text.AlignHCenter
          wrapMode: Text.WordWrap
          text: "Nothing armed. Press a to watch a budget line going over, a bill slipping past its date, a large posting, or an account running low."
          color: view.dim
          font.family: view.ff
          font.pixelSize: Style.font.body
        }
        ListView {
          anchors.top: alertHead.bottom
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.bottom: parent.bottom
          clip: true
          model: view.armed
          currentIndex: view.alertCursor
          boundsBehavior: Flickable.StopAtBounds
          delegate: CursorSurface {
            id: alertRow
            required property var modelData
            required property int index
            width: ListView.view.width
            height: Style.space(44)
            readonly property bool selected: view.pane === "Alerts" && index === view.alertCursor
            readonly property string state: view.alertState(modelData)
            radius: 0
            hasCursor: alertRow.selected
            fill: Style.selectedAccentFill
            foreground: view.fg
            accent: view.accent

            Column {
              anchors.left: parent.left
              anchors.leftMargin: Style.space(12)
              anchors.right: alertState.left
              anchors.rightMargin: Style.space(8)
              anchors.verticalCenter: parent.verticalCenter
              spacing: Style.space(2)
              Text {
                width: parent.width
                text: alertRow.modelData.path + "  " + alertRow.modelData.operator
                      + (alertRow.modelData.standing ? "  ·  every time" : "")
                color: view.fg
                font.family: view.ff
                font.pixelSize: Style.font.body
                elide: Text.ElideRight
              }
              Text {
                width: parent.width
                text: (alertRow.modelData.reason ? alertRow.modelData.reason + "  ·  " : "")
                      + "to " + alertRow.modelData.deliverTo
                      + "  ·  until " + String(alertRow.modelData.expiresAt || "").slice(0, 10)
                      + "  ·  by " + alertRow.modelData.armedBy
                color: view.dimmer
                font.family: view.ff
                font.pixelSize: Style.font.caption
                elide: Text.ElideRight
              }
            }
            Text {
              id: alertState
              anchors.right: parent.right
              anchors.rightMargin: Style.space(12)
              anchors.verticalCenter: parent.verticalCenter
              text: alertRow.state
              color: alertRow.state === "fired" ? (view.app ? view.app.expense : view.fg)
                   : alertRow.state === "expired" ? view.dimmer
                   : view.dim
              font.family: view.ff
              font.pixelSize: Style.font.bodySmall
            }
            MouseArea {
              id: alertRowMouse
              anchors.fill: parent
              hoverEnabled: true
              cursorShape: Qt.PointingHandCursor
              onEntered: view.pointFrom("Alerts", alertRow.index, alertRow, { x: alertRowMouse.mouseX, y: alertRowMouse.mouseY })
              onPositionChanged: function (mouse) { view.pointFrom("Alerts", alertRow.index, alertRow, mouse) }
              onClicked: view.alertCursor = alertRow.index
              onDoubleClicked: { view.alertCursor = alertRow.index; view.openArming(alertRow.modelData) }
            }
          }
        }
      }
    }

    Text {
      id: footer
      width: parent.width
      text: view.pane === "Payees"
        ? "j k move   Enter rename, alias, fold in or remove   Tab rates"
        : view.pane === "Alerts"
        ? "j k move   a arm   Enter change   x disarm   Tab categories"
        : view.pane === "Rates"
        ? "j k move   a add   x remove   Tab categories   nothing is fetched: a rate is what you last put in"
        : "j k move   Enter edit   a new   x archive or restore   / search   Tab payees"
      color: view.dimmer
      font.family: view.ff
      font.pixelSize: Style.font.caption
      elide: Text.ElideRight
    }
  }

  ConfirmDialog {
    id: confirm
    property var cat: null
    anchors.fill: parent
    z: 20
    confirmText: "Remove"
    // Cancel is what Enter lands on. The dialog defaults to preselecting
    // Confirm, which on a destructive prompt means a stray Enter destroys.
    selectedIndex: 0
    selectedText: view.app ? view.app.urgent : Color.urgent
    fontFamily: view.ff
    onConfirmed: {
      if (confirm.cat) view.app.run(["category", "remove", String(confirm.cat.id)], "removed " + confirm.cat.name)
      confirm.opened = false
      confirm.cat = null
      view.closeEditor()
    }
    onCanceled: { confirm.opened = false; confirm.cat = null; view.forceActiveFocus() }
  }

  Loader {
    id: payeeEditor
    property var editing: null
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(520))
    active: false
    z: 11
    source: "../components/PayeeForm.qml"
    onLoaded: {
      item.app = view.app
      item.payees = view.payees
      item.editing = payeeEditor.editing
      item.submitted.connect(function (argv, doneText) {
        if (argv && argv.length > 0) view.app.run(argv, doneText)
        view.closePayee()
      })
      item.cancelled.connect(view.closePayee)
      Qt.callLater(function () { if (payeeEditor.item) payeeEditor.item.focusFirst() })
    }
  }

  Loader {
    id: arming
    property var editing: null
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(560))
    active: false
    z: 11
    source: "../components/AlertForm.qml"
    onLoaded: {
      item.app = view.app
      item.catalogue = view.catalogue
      item.editing = arming.editing
      item.submitted.connect(function (argv, doneText) {
        view.app.run(argv, doneText)
        view.closeArming()
      })
      item.cancelled.connect(view.closeArming)
      Qt.callLater(function () { if (arming.item) arming.item.focusFirst() })
    }
  }

  Loader {
    id: editor
    property var editing: null
    property string parentHint: ""
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(560))
    active: false
    z: 10
    source: "../components/CategoryForm.qml"
    onLoaded: {
      item.app = view.app
      item.editing = editor.editing
      item.parentHint = editor.parentHint
      item.submitted.connect(function (argv, doneText) {
        view.app.run(argv, doneText)
        view.closeEditor()
      })
      item.removeRequested.connect(view.askRemove)
      item.cancelled.connect(view.closeEditor)
      Qt.callLater(function () { if (editor.item) editor.item.focusFirst() })
    }
  }
}
