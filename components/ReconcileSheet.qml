import QtQuick
import qs.Commons
import qs.Ui

// Hold a statement against the ledger for one account: what the bank says the
// account held on a date, against what this ledger says, and the lines still
// waiting. A line's place on a statement is its status, so ticking one is an
// ordinary edit.
Overlay {
  id: card
  property var app: null
  // The account this statement is for.
  property var account: null

  signal closed()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property color edge: app ? app.cardBorder : Style.normalBorderColor
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property bool formFocused:
    throughField.activeFocus || balanceField.activeFocus
    || settleButton.activeFocus || closeButton.activeFocus

  property var sheet: null
  property int cursor: 0
  readonly property var rows: sheet && sheet.rows ? sheet.rows : []
  readonly property string currency: account ? String(account.currency) : ""
  readonly property bool balanced: !!sheet && Number(sheet.difference) === 0
  readonly property bool anythingWaiting: rows.length > 0 || (!!sheet && Number(sheet.ticked) !== 0)

  implicitHeight: col.implicitHeight + Style.space(32)

  function focusFirst() { balanceField.forceActiveFocus() }
  function money(minor) { return app ? app.fmt(minor, card.currency) : String(minor) }

  function today() {
    var d = new Date()
    var m = d.getMonth() + 1, day = d.getDate()
    return d.getFullYear() + "-" + (m < 10 ? "0" : "") + m + "-" + (day < 10 ? "0" : "") + day
  }

  function load() {
    if (!card.app || !card.account) return
    var argv = ["reconcile", String(card.account.id), balanceField.text.trim() === "" ? "0" : balanceField.text.trim()]
    if (throughField.text.trim() !== "") argv.push("-through", throughField.text.trim())
    card.app.query(argv, function (data, err) {
      if (err) { card.app.lastError = err; return }
      card.sheet = data
      if (card.cursor >= card.rows.length) card.cursor = Math.max(0, card.rows.length - 1)
    })
  }

  Component.onCompleted: {
    throughField.text = card.today()
    card.load()
  }
  onAccountChanged: card.load()
  Connections {
    target: card.app
    function onChanged() { card.load() }
  }

  // Ticking a line on or off the statement.
  function toggleRow() {
    var row = card.rows[card.cursor]
    if (!row) return
    var on = String(row.status) === "cleared"
    card.app.run(["edit", String(row.id), "-status", on ? "pending" : "cleared"],
                 (on ? "took " : "put ") + (row.description || "the line") + (on ? " off" : " on") + " the statement")
  }

  function settle() {
    if (!card.balanced) { card.app.lastError = "the sheet is out by " + card.money(card.sheet ? card.sheet.difference : 0); return }
    var argv = ["reconcile", String(card.account.id), balanceField.text.trim() === "" ? "0" : balanceField.text.trim(), "-finish"]
    if (throughField.text.trim() !== "") argv.push("-through", throughField.text.trim())
    card.app.run(argv, "settled " + card.account.name + " to " + card.money(card.sheet ? card.sheet.statement : 0))
    card.closed()
  }

  function handleKey(e) {
    if (e.key === Qt.Key_Escape) { card.closed(); return true }
    if (throughField.activeFocus || balanceField.activeFocus) return false
    switch (e.key) {
    case Qt.Key_J: case Qt.Key_Down:
      if (card.rows.length) card.cursor = Math.min(card.rows.length - 1, card.cursor + 1)
      return true
    case Qt.Key_K: case Qt.Key_Up:
      card.cursor = Math.max(0, card.cursor - 1)
      return true
    case Qt.Key_Space: case Qt.Key_X:
      card.toggleRow()
      return true
    case Qt.Key_Return: case Qt.Key_Enter:
      card.settle()
      return true
    }
    return false
  }

  component Label: Text {
    color: card.dimmer
    font.family: card.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }
  component Note: Text {
    color: card.dim
    font.family: card.ff
    font.pixelSize: Style.font.caption
    wrapMode: Text.WordWrap
  }

  Column {
    id: col
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    anchors.margins: Style.space(16)
    spacing: Style.space(10)

    Text {
      text: card.account ? "Reconcile " + card.account.name : "Reconcile"
      color: card.fg
      font.family: card.ff
      font.pixelSize: Style.font.title
    }
    Note {
      width: parent.width
      text: "Put in what the statement closed at. Tick the lines it shows, leave the rest pending, and settle it when the difference is nothing."
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * 0.45
        spacing: Style.space(4)
        Label { text: "CLOSING DATE" }
        TextField {
          id: throughField
          width: parent.width
          foreground: card.fg
          accent: card.accent
          font.family: card.ff
          font.pixelSize: Style.font.body
          placeholderText: "2026-08-31"
          onTextChanged: card.load()
          Keys.onEscapePressed: card.closed()
        }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.55
        spacing: Style.space(4)
        Label { text: "CLOSING BALANCE" }
        TextField {
          id: balanceField
          width: parent.width
          foreground: card.fg
          accent: card.accent
          font.family: card.ff
          font.pixelSize: Style.font.body
          placeholderText: "0.00"
          onTextChanged: card.load()
          Keys.onEscapePressed: card.closed()
        }
      }
    }

    Rectangle {
      width: parent.width
      height: summary.implicitHeight + Style.space(16)
      radius: Style.cornerRadius
      color: "transparent"
      border.width: Style.normalBorderWidth
      border.color: card.edge
      Row {
        id: summary
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        anchors.margins: Style.space(10)
        spacing: Style.space(10)
        Column {
          width: (parent.width - parent.spacing * 2) / 3
          spacing: Style.space(2)
          Label { text: "SETTLED" }
          Text {
            text: card.sheet ? card.money(card.sheet.settled) : ""
            color: card.dim
            font.family: card.ff
            font.pixelSize: Style.font.body
          }
        }
        Column {
          width: (parent.width - parent.spacing * 2) / 3
          spacing: Style.space(2)
          Label { text: "ON THIS STATEMENT" }
          Text {
            text: card.sheet ? card.money(card.sheet.ticked) : ""
            color: card.dim
            font.family: card.ff
            font.pixelSize: Style.font.body
          }
        }
        Column {
          width: (parent.width - parent.spacing * 2) / 3
          spacing: Style.space(2)
          Label { text: "DIFFERENCE" }
          Text {
            text: card.sheet ? card.money(card.sheet.difference) : ""
            color: !card.sheet ? card.dim
                 : card.balanced ? (card.app ? card.app.onBudget : card.fg)
                 : (card.app ? card.app.expense : card.fg)
            font.family: card.ff
            font.pixelSize: Style.font.body
          }
        }
      }
    }

    Note {
      width: parent.width
      visible: card.rows.length === 0
      text: card.anythingWaiting
        ? "Everything on or before this date is on the statement."
        : "Nothing is waiting on or before this date."
    }

    ListView {
      width: parent.width
      height: Math.min(Style.space(260), contentHeight)
      visible: card.rows.length > 0
      clip: true
      interactive: contentHeight > height
      model: card.rows
      currentIndex: card.cursor
      delegate: Rectangle {
        id: line
        required property var modelData
        required property int index
        width: ListView.view.width
        height: Style.space(26)
        radius: Style.cornerRadius
        readonly property bool on: String(modelData.status) === "cleared"
        color: index === card.cursor ? Style.selectedAccentFill : "transparent"
        Row {
          anchors.fill: parent
          anchors.leftMargin: Style.space(8)
          anchors.rightMargin: Style.space(8)
          spacing: Style.space(8)
          Text {
            width: Style.space(18)
            anchors.verticalCenter: parent.verticalCenter
            text: line.on ? "󰄲" : "󰄱"
            color: line.on ? card.accent : card.dimmer
            font.family: card.ff
            font.pixelSize: Style.font.body
          }
          Text {
            width: Style.space(80)
            anchors.verticalCenter: parent.verticalCenter
            text: String(line.modelData.date || "")
            color: card.dimmer
            font.family: card.ff
            font.pixelSize: Style.font.bodySmall
          }
          Text {
            width: parent.width - Style.space(18) - Style.space(80) - Style.space(100) - Style.space(8) * 3
            anchors.verticalCenter: parent.verticalCenter
            text: String(line.modelData.description || "")
            color: line.on ? card.fg : card.dim
            font.family: card.ff
            font.pixelSize: Style.font.bodySmall
            elide: Text.ElideRight
          }
          Text {
            width: Style.space(100)
            horizontalAlignment: Text.AlignRight
            anchors.verticalCenter: parent.verticalCenter
            text: card.money(line.modelData.signed)
            color: Number(line.modelData.signed) < 0
              ? (card.app ? card.app.expense : card.fg)
              : (card.app ? card.app.income : card.fg)
            font.family: card.ff
            font.pixelSize: Style.font.bodySmall
          }
        }
        MouseArea {
          anchors.fill: parent
          onClicked: { card.cursor = line.index; card.toggleRow() }
        }
      }
    }

    Row {
      spacing: Style.space(8)
      Button {
        id: settleButton
        text: "Settle   Enter"
        tooltipText: "Every ticked line becomes reconciled and closes for good"
        foreground: card.balanced ? card.fg : card.dimmer
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        bordered: true
        focusable: true
        onClicked: card.settle()
        Keys.onEscapePressed: card.closed()
      }
      Button {
        id: closeButton
        text: "Close   Esc"
        foreground: card.dim
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        bordered: true
        focusable: true
        onClicked: card.closed()
        Keys.onEscapePressed: card.closed()
      }
    }
    Note {
      width: parent.width
      text: "j k move   Space tick   A transfer is one line with one status, so settling it here settles it on both accounts."
    }
  }
}
