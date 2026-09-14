import QtQuick
import qs.Commons
import qs.Ui
import "../components"

// Accounts: the list with balances on the left, the chosen account and its
// recent postings on the right. a adds, Enter edits, x closes or reopens,
// d removes one nothing points at, r holds a statement against it.
Item {
  id: view
  property var app: null
  readonly property bool formFocused:
    (editor.item ? editor.item.formFocused === true : false)
    || (sheet.item ? sheet.item.formFocused === true : false)
    || confirm.opened

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family
  readonly property int colourMs: app ? app.colourMs : 120
  readonly property int moveMs: app ? app.moveMs : 140

  readonly property var accounts: app ? app.accounts : []
  property int cursor: 0
  readonly property var current: accounts.length > 0 ? accounts[Math.min(cursor, accounts.length - 1)] : null
  property var recent: []
  property string recentFor: ""

  function money(minor, currency) { return app ? app.fmt(minor, currency) : String(minor) }

  function glyph(type) {
    switch (String(type)) {
    case "savings": return "󰆘"
    case "cash": return "󰄨"
    case "credit_card": case "loan": case "payable": return "󰆟"
    case "investment": return "󰄨"
    case "prepaid": return "󰆧"
    case "receivable": return "󰈙"
    }
    return "󰆦"
  }

  function typeLabel(type) {
    switch (String(type)) {
    case "checking": return "Checking"
    case "savings": return "Savings"
    case "cash": return "Cash"
    case "credit_card": return "Credit card"
    case "loan": return "Loan"
    case "investment": return "Investment"
    case "prepaid": return "Prepaid"
    case "receivable": return "Receivable"
    case "payable": return "Payable"
    }
    return String(type)
  }

  function loadRecent() {
    if (!app || !view.current) { view.recent = []; return }
    var id = String(view.current.id)
    view.recentFor = id
    app.query(["transactions", "-account", id, "-limit", "40"], function (data, err) {
      if (view.recentFor !== id) return
      view.recent = Array.isArray(data) ? data : []
    })
  }

  onCurrentChanged: loadRecent()
  onAppChanged: loadRecent()
  Connections {
    target: view.app
    function onChanged() { view.loadRecent() }
  }

  function openEditor(account) {
    editor.editing = account
    editor.active = true
  }
  function closeEditor() {
    editor.active = false
    editor.editing = null
  }

  function openSheet() {
    if (!view.current) return
    sheet.account = view.current
    sheet.active = true
  }
  function closeSheet() {
    sheet.active = false
    sheet.account = null
    view.forceActiveFocus()
  }

  function removeCurrent() {
    if (!view.current) return
    confirm.account = view.current
    confirm.message = "Remove " + view.current.name + "? Only an account nothing points at can go; one with history is closed instead."
    confirm.opened = true
  }

  function toggleOpen() {
    if (!view.current) return
    var open = view.current.active !== false
    app.run(["edit-account", String(view.current.id), "-active=" + (open ? "false" : "true")],
            (open ? "closed " : "reopened ") + view.current.name)
  }

  // A list that scrolls under a still pointer fires hover events for whatever
  // slid beneath it, which would drag the cursor back from wherever the
  // keyboard just put it. The gate answers whether the pointer itself moved.
  PointerMoveGate {
    id: pointerGate
    referenceItem: view
  }

  function pointFrom(index, item, mouse) {
    // A form open in this view owns the keys and the caret; the pointer must
    // not move the cursor out from under it.
    if (view.formFocused) return
    if (!pointerGate.moved(item, mouse)) return
    view.cursor = index
  }

  function handleKey(e) {
    if (confirm.opened) { confirm.handleKey(e); return true }
    if (sheet.active) return sheet.item ? sheet.item.handleKey(e) : false
    if (editor.active) {
      if (e.key === Qt.Key_Escape) { view.closeEditor(); return true }
      return false
    }
    if (e.modifiers !== Qt.NoModifier) return false
    switch (e.key) {
    case Qt.Key_J: case Qt.Key_Down:
      pointerGate.reset()
      if (view.accounts.length) view.cursor = Math.min(view.accounts.length - 1, view.cursor + 1)
      return true
    case Qt.Key_K: case Qt.Key_Up:
      pointerGate.reset()
      view.cursor = Math.max(0, view.cursor - 1)
      return true
    case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_E:
      if (view.current) view.openEditor(view.current)
      return true
    case Qt.Key_A: view.openEditor(null); return true
    case Qt.Key_X: view.toggleOpen(); return true
    case Qt.Key_D: view.removeCurrent(); return true
    case Qt.Key_R: view.openSheet(); return true
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

    Row {
      width: parent.width
      spacing: Style.space(12)
      Text {
        anchors.verticalCenter: parent.verticalCenter
        text: "Accounts"
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }
      Text {
        anchors.verticalCenter: parent.verticalCenter
        visible: !!(view.app && view.app.snap)
        text: view.app && view.app.snap
          ? "liquid " + view.money(view.app.snap.liquid, view.app.snap.baseCurrency)
            + "   net worth " + view.money(view.app.snap.netWorth, view.app.snap.baseCurrency)
            + " " + view.app.snap.baseCurrency
          : ""
        color: view.dimmer
        font.family: view.ff
        font.pixelSize: Style.font.caption
      }
    }

    Row {
      width: parent.width
      height: parent.height - y - footer.height - parent.spacing
      spacing: Style.space(12)

      // ---- the list
      Flickable {
        width: (parent.width - parent.spacing) * 0.42
        height: parent.height
        contentWidth: width
        contentHeight: accountsCol.implicitHeight
        interactive: contentHeight > height
        boundsBehavior: Flickable.StopAtBounds
        clip: true

        Column {
          id: accountsCol
          width: parent.width
          spacing: Style.space(8)

          Text {
            visible: view.accounts.length === 0
            text: "No accounts yet. Press a to add one."
            color: view.dim
            font.family: view.ff
            font.pixelSize: Style.font.body
          }

          Repeater {
            model: view.accounts
            delegate: CursorSurface {
              id: cardItem
              required property var modelData
              required property int index
              width: parent.width
              height: Style.space(64)
              readonly property bool selected: index === view.cursor
              readonly property bool closed: modelData.active === false
              readonly property bool low: modelData.lowBalance !== undefined && modelData.lowBalance !== null
                                          && modelData.balance < modelData.lowBalance
              // One highlight, whichever hand moved it: the keyboard and the
              // pointer both write view.cursor and the paint follows from it.
              hasCursor: cardItem.selected
              bordered: true
              fill: Style.selectedAccentFill
              foreground: view.fg
              accent: view.accent

              Text {
                id: glyphText
                anchors.left: parent.left
                anchors.leftMargin: Style.space(14)
                anchors.verticalCenter: parent.verticalCenter
                text: view.glyph(cardItem.modelData.type)
                color: cardItem.selected ? view.accent : view.dim
                font.family: view.ff
                font.pixelSize: Style.font.icon
              }
              Column {
                anchors.left: glyphText.right
                anchors.leftMargin: Style.space(12)
                anchors.right: rowActions.visible ? rowActions.left : balanceText.left
                anchors.rightMargin: Style.space(8)
                anchors.verticalCenter: parent.verticalCenter
                spacing: Style.space(2)
                Text {
                  width: parent.width
                  text: cardItem.modelData.name + (cardItem.closed ? "  (closed)" : "")
                  color: cardItem.closed ? view.dim : view.fg
                  font.family: view.ff
                  font.pixelSize: Style.font.body
                  elide: Text.ElideRight
                }
                Text {
                  width: parent.width
                  text: view.typeLabel(cardItem.modelData.type)
                        + (cardItem.modelData.institution ? "  ·  " + cardItem.modelData.institution : "")
                  color: view.dimmer
                  font.family: view.ff
                  font.pixelSize: Style.font.caption
                  elide: Text.ElideRight
                }
              }
              Text {
                id: balanceText
                anchors.right: parent.right
                anchors.rightMargin: Style.space(14)
                anchors.verticalCenter: parent.verticalCenter
                text: view.money(cardItem.modelData.balance, cardItem.modelData.currency) + " " + cardItem.modelData.currency
                color: cardItem.low ? (view.app ? view.app.nearLimit : view.fg)
                     : cardItem.modelData.balance < 0 ? (view.app ? view.app.expense : view.fg)
                     : cardItem.closed ? view.dim : view.fg
                font.family: view.ff
                font.pixelSize: Style.font.body
              }
              // Hover for the whole card, children included. The card's own
              // MouseArea cannot do this: a child MouseArea takes the hover
              // from it, which made the actions fade out the instant the
              // pointer reached one, and back in as it left. That flickered.
              HoverHandler { id: cardHover }

              // The verbs this row answers to, shown when the pointer is on it.
              // They exist on the keyboard already and say so in the footer,
              // but nothing told a mouse that a double click opens the editor.
              Row {
                id: rowActions
                // Beside the balance, never over it: a row is hovered in
                // order to read it, so covering its number is the one thing
                // these must not do.
                anchors.right: balanceText.left
                anchors.rightMargin: Style.space(8)
                anchors.verticalCenter: parent.verticalCenter
                spacing: Style.space(2)
                z: 2
                visible: opacity > 0
                opacity: cardHover.hovered ? 1 : 0
                Behavior on opacity { NumberAnimation { duration: view.colourMs; easing.type: Easing.OutCubic } }


                RowAction {
                  app: view.app
                  glyph: "󰏫"
                  tint: view.accent
                  hint: "Edit this account"
                  onTriggered: { view.cursor = cardItem.index; view.openEditor(cardItem.modelData) }
                }
                RowAction {
                  app: view.app
                  glyph: "󰑐"
                  tint: view.accent
                  hint: "Hold a statement against it"
                  onTriggered: { view.cursor = cardItem.index; view.openSheet() }
                }
                RowAction {
                  app: view.app
                  glyph: "󰩺"
                  tint: view.app ? view.app.expense : view.dim
                  hint: "Remove, if nothing points at it"
                  onTriggered: { view.cursor = cardItem.index; view.removeCurrent() }
                }
              }

              MouseArea {
                id: cardMouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onEntered: view.pointFrom(cardItem.index, cardItem, { x: cardMouse.mouseX, y: cardMouse.mouseY })
                onPositionChanged: function (mouse) { view.pointFrom(cardItem.index, cardItem, mouse) }
                onClicked: view.cursor = cardItem.index
                onDoubleClicked: { view.cursor = cardItem.index; view.openEditor(cardItem.modelData) }
              }
            }
          }
        }
      }

      // ---- the chosen account
      Rectangle {
        width: (parent.width - parent.spacing) * 0.58
        height: parent.height
        radius: Style.cornerRadius
        color: "transparent"
        border.width: Style.normalBorderWidth
        border.color: view.border
        clip: true

        Text {
          anchors.centerIn: parent
          visible: !view.current
          text: "Pick an account to see it here."
          color: view.dim
          font.family: view.ff
          font.pixelSize: Style.font.body
        }

        Column {
          id: detail
          visible: !!view.current
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.top: parent.top
          anchors.margins: Style.space(16)
          spacing: Style.space(6)

          Text {
            text: view.current ? view.current.name : ""
            color: view.fg
            font.family: view.ff
            font.pixelSize: Style.font.title
          }
          Text {
            text: view.current
              ? view.typeLabel(view.current.type) + "  ·  " + view.current.currency
                + (view.current.institution ? "  ·  " + view.current.institution : "")
                + (view.current.last4 ? "  ·  " + view.current.last4 : "")
                + "  ·  opened " + view.current.openingDate
              : ""
            color: view.dimmer
            font.family: view.ff
            font.pixelSize: Style.font.caption
            width: parent.width
            elide: Text.ElideRight
          }
          Text {
            text: view.current ? view.money(view.current.balance, view.current.currency) + " " + view.current.currency : ""
            color: view.current && view.current.balance < 0 ? (view.app ? view.app.expense : view.fg) : view.fg
            font.family: view.ff
            font.pixelSize: Style.font.display
            font.bold: true
            topPadding: Style.space(6)
          }
          Text {
            visible: !!(view.current && view.current.lowBalance !== undefined && view.current.lowBalance !== null)
            text: view.current && view.current.lowBalance !== undefined && view.current.lowBalance !== null
              ? "warns under " + view.money(view.current.lowBalance, view.current.currency)
                + (view.current.includeInNetWorth === false ? "  ·  outside net worth" : "")
              : (view.current && view.current.includeInNetWorth === false ? "outside net worth" : "")
            color: view.dimmer
            font.family: view.ff
            font.pixelSize: Style.font.caption
          }

          Caption { text: "RECENT"; topPadding: Style.space(12) }
        }

        ListView {
          anchors.top: detail.bottom
          anchors.topMargin: Style.space(6)
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.bottom: parent.bottom
          anchors.leftMargin: Style.space(16)
          anchors.rightMargin: Style.space(16)
          anchors.bottomMargin: Style.space(12)
          clip: true
          model: view.recent
          boundsBehavior: Flickable.StopAtBounds
          delegate: Item {
            id: postingRow
            required property var modelData
            width: ListView.view.width
            height: Style.space(26)
            readonly property bool transfer: modelData.kind === "transfer"
            // A transfer shows the leg that touches this account.
            readonly property bool incoming: transfer && view.current && modelData.counterAccountId === view.current.id
            readonly property real amount: incoming
              ? (modelData.counterAmount !== undefined && modelData.counterAmount !== null ? modelData.counterAmount : -modelData.amount)
              : modelData.amount

            Text {
              id: dateText
              anchors.left: parent.left
              anchors.verticalCenter: parent.verticalCenter
              width: Style.space(52)
              text: String(postingRow.modelData.date || "").slice(5)
              color: view.dimmer
              font.family: view.ff
              font.pixelSize: Style.font.bodySmall
            }
            Text {
              anchors.left: dateText.right
              anchors.right: amountText.left
              anchors.rightMargin: Style.space(8)
              anchors.verticalCenter: parent.verticalCenter
              text: postingRow.modelData.description && postingRow.modelData.description !== ""
                ? postingRow.modelData.description
                : postingRow.transfer
                  ? (postingRow.incoming ? "From " + (view.app ? view.app.accountName(postingRow.modelData.accountId) : "")
                                         : "To " + (view.app ? view.app.accountName(postingRow.modelData.counterAccountId) : ""))
                  : (view.app ? view.app.categoryName(postingRow.modelData.categoryId) : "")
              color: view.fg
              font.family: view.ff
              font.pixelSize: Style.font.body
              elide: Text.ElideRight
            }
            Text {
              id: amountText
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              text: (postingRow.amount > 0 ? "+" : "") + view.money(postingRow.amount, view.current ? view.current.currency : postingRow.modelData.currency)
              color: postingRow.transfer ? view.dim
                   : postingRow.amount < 0 ? (view.app ? view.app.expense : view.fg)
                   : (view.app ? view.app.income : view.fg)
              font.family: view.ff
              font.pixelSize: Style.font.body
            }
          }
        }
      }
    }

    Text {
      id: footer
      width: parent.width
      text: "j k move   Enter edit   a new   x close or reopen   r reconcile   d remove"
      color: view.dimmer
      font.family: view.ff
      font.pixelSize: Style.font.caption
      elide: Text.ElideRight
    }
  }

  Loader {
    id: sheet
    property var account: null
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(620))
    active: false
    z: 10
    source: "../components/ReconcileSheet.qml"
    onLoaded: {
      item.app = view.app
      item.account = sheet.account
      item.closed.connect(view.closeSheet)
      Qt.callLater(function () { if (sheet.item) sheet.item.focusFirst() })
    }
  }

  ConfirmDialog {
    id: confirm
    property var account: null
    anchors.fill: parent
    z: 20
    confirmText: "Remove"
    // Cancel is what Enter lands on. The dialog defaults to preselecting
    // Confirm, which on a destructive prompt means a stray Enter destroys.
    selectedIndex: 0
    selectedText: view.app ? view.app.urgent : Color.urgent
    fontFamily: view.ff
    onConfirmed: {
      if (confirm.account) view.app.run(["remove-account", String(confirm.account.id)], "removed " + confirm.account.name)
      confirm.opened = false
      confirm.account = null
      view.forceActiveFocus()
    }
    onCanceled: { confirm.opened = false; confirm.account = null; view.forceActiveFocus() }
  }

  Loader {
    id: editor
    property var editing: null
    anchors.top: parent.top
    anchors.right: parent.right
    width: Math.min(parent.width, Style.space(560))
    active: false
    z: 10
    source: "../components/AccountForm.qml"
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
