import QtQuick
import qs.Commons
import qs.Ui

// Add or edit an account. Emits argv for the CLI.
Overlay {
  id: card
  property var app: null
  // The account being edited, or null for a new one.
  property var editing: null

  signal submitted(var argv, string doneText)
  signal cancelled()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property bool formFocused:
    nameField.activeFocus || currencyField.activeFocus || openingField.activeFocus
    || openingDateField.activeFocus || institutionField.activeFocus || last4Field.activeFocus
    || lowField.activeFocus || typeBox.popupOpen || networthToggle.activeFocus
    || activeToggle.activeFocus || saveButton.activeFocus || cancelButton.activeFocus

  property string type: editing ? String(editing.type) : "checking"
  property bool networth: editing ? editing.includeInNetWorth !== false : true
  property bool active: editing ? editing.active !== false : true

  readonly property var typeOptions: [
    { value: "checking", label: "Checking" },
    { value: "savings", label: "Savings" },
    { value: "cash", label: "Cash" },
    { value: "credit_card", label: "Credit card" },
    { value: "loan", label: "Loan" },
    { value: "investment", label: "Investment" },
    { value: "prepaid", label: "Prepaid" },
    { value: "receivable", label: "Receivable" },
    { value: "payable", label: "Payable" }
  ]

  implicitHeight: col.implicitHeight + Style.space(32)

  function focusFirst() { nameField.forceActiveFocus() }

  function plain(minor, currency) {
    var d = app ? app.decimalsFor(currency) : 2
    var neg = Number(minor) < 0
    var s = String(Math.abs(Number(minor) || 0))
    while (s.length <= d) s = "0" + s
    return (neg ? "-" : "") + (d > 0 ? s.slice(0, s.length - d) + "." + s.slice(s.length - d) : s)
  }

  // The loader hands this form its subject after the component is
  // built, so the fields are filled when it arrives, not before.
  function prefill() {
    if (!editing) return
    nameField.text = editing.name || ""
    currencyField.text = editing.currency || ""
    openingField.text = card.plain(editing.openingBalance, editing.currency)
    openingDateField.text = editing.openingDate || ""
    institutionField.text = editing.institution || ""
    last4Field.text = editing.last4 || ""
    lowField.text = editing.lowBalance !== undefined && editing.lowBalance !== null ? card.plain(editing.lowBalance, editing.currency) : ""
  }

  onEditingChanged: card.prefill()
  Component.onCompleted: card.prefill()

  function submit() {
    var name = nameField.text.trim()
    if (name === "") { nameField.forceActiveFocus(); return }
    var argv
    if (card.editing) {
      argv = ["edit-account", String(card.editing.id), "-name", name, "-type", card.type,
              "-institution", institutionField.text.trim(), "-last4", last4Field.text.trim(),
              "-low-balance", lowField.text.trim(), "-opening-date", openingDateField.text.trim(),
              "-networth=" + (card.networth ? "true" : "false"), "-active=" + (card.active ? "true" : "false")]
      if (openingField.text.trim() !== "") argv.push("-opening", openingField.text.trim())
    } else {
      argv = ["add-account", name, "-type", card.type]
      if (currencyField.text.trim() !== "") argv.push("-currency", currencyField.text.trim().toUpperCase())
      if (openingField.text.trim() !== "") argv.push("-opening", openingField.text.trim())
      if (openingDateField.text.trim() !== "") argv.push("-opening-date", openingDateField.text.trim())
      if (institutionField.text.trim() !== "") argv.push("-institution", institutionField.text.trim())
      if (last4Field.text.trim() !== "") argv.push("-last4", last4Field.text.trim())
      if (lowField.text.trim() !== "") argv.push("-low-balance", lowField.text.trim())
      argv.push("-networth=" + (card.networth ? "true" : "false"))
    }
    card.submitted(argv, card.editing ? "saved " + name : "added " + name)
  }

  component Field: TextField {
    foreground: card.fg
    accent: card.accent
    font.family: card.ff
    font.pixelSize: Style.font.body
    Keys.onReturnPressed: card.submit()
    Keys.onEnterPressed: card.submit()
    Keys.onEscapePressed: card.cancelled()
  }
  component Label: Text {
    color: card.dimmer
    font.family: card.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }

  Column {
    id: col
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    anchors.margins: Style.space(16)
    spacing: Style.space(10)

    Text {
      text: card.editing ? "Edit account" : "New account"
      color: card.fg
      font.family: card.ff
      font.pixelSize: Style.font.title
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * 0.6
        spacing: Style.space(4)
        Label { text: "NAME" }
        Field {
          id: nameField
          width: parent.width
          placeholderText: "Checking"
          KeyNavigation.tab: currencyField.visible ? currencyField : openingField
          KeyNavigation.backtab: cancelButton
        }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.4
        spacing: Style.space(4)
        Label { text: "TYPE" }
        Dropdown {
          id: typeBox
          width: parent.width
          showLabel: false
          options: card.typeOptions
          value: card.type
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { card.type = v }
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        visible: !card.editing
        width: (parent.width - parent.spacing * 2) * 0.25
        spacing: Style.space(4)
        Label { text: "CURRENCY" }
        Field {
          id: currencyField
          width: parent.width
          placeholderText: card.app && card.app.snap ? card.app.snap.baseCurrency : "EUR"
          KeyNavigation.tab: openingField
          KeyNavigation.backtab: nameField
        }
      }
      Column {
        width: (parent.width - parent.spacing * 2) * (card.editing ? 0.5 : 0.35)
        spacing: Style.space(4)
        Label { text: "OPENING BALANCE" }
        Field {
          id: openingField
          width: parent.width
          placeholderText: "0"
          KeyNavigation.tab: openingDateField
          KeyNavigation.backtab: currencyField.visible ? currencyField : nameField
        }
      }
      Column {
        width: (parent.width - parent.spacing * 2) * (card.editing ? 0.5 : 0.4)
        spacing: Style.space(4)
        Label { text: "OPENING DATE" }
        Field {
          id: openingDateField
          width: parent.width
          placeholderText: "YYYY-MM-DD, today if empty"
          KeyNavigation.tab: institutionField
          KeyNavigation.backtab: openingField
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing * 2) * 0.5
        spacing: Style.space(4)
        Label { text: "INSTITUTION" }
        Field {
          id: institutionField
          width: parent.width
          placeholderText: "Bank or issuer, optional"
          KeyNavigation.tab: last4Field
          KeyNavigation.backtab: openingDateField
        }
      }
      Column {
        width: (parent.width - parent.spacing * 2) * 0.2
        spacing: Style.space(4)
        Label { text: "LAST 4" }
        Field {
          id: last4Field
          width: parent.width
          placeholderText: "1234"
          maximumLength: 4
          KeyNavigation.tab: lowField
          KeyNavigation.backtab: institutionField
        }
      }
      Column {
        width: (parent.width - parent.spacing * 2) * 0.3
        spacing: Style.space(4)
        Label { text: "LOW BALANCE" }
        Field {
          id: lowField
          width: parent.width
          placeholderText: "Warn under"
          KeyNavigation.tab: networthToggle
          KeyNavigation.backtab: last4Field
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(16)
      Toggle {
        id: networthToggle
        width: (parent.width - parent.spacing) * 0.5
        label: "Counts in net worth"
        checked: card.networth
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onClicked: card.networth = !card.networth
        KeyNavigation.tab: card.editing ? activeToggle : saveButton
        KeyNavigation.backtab: lowField
        Keys.onEscapePressed: card.cancelled()
      }
      Toggle {
        id: activeToggle
        visible: !!card.editing
        width: (parent.width - parent.spacing) * 0.5
        label: "Open"
        description: card.active ? "" : "Closed accounts stay in reports but not in forms"
        checked: card.active
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onClicked: card.active = !card.active
        KeyNavigation.tab: saveButton
        KeyNavigation.backtab: networthToggle
        Keys.onEscapePressed: card.cancelled()
      }
    }

    Row {
      spacing: Style.space(8)
      Button {
        id: saveButton
        text: "Save   Enter"
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        bordered: true
        focusable: true
        onClicked: card.submit()
        KeyNavigation.tab: cancelButton
        KeyNavigation.backtab: card.editing ? activeToggle : networthToggle
        Keys.onEscapePressed: card.cancelled()
      }
      Button {
        id: cancelButton
        text: "Cancel   Esc"
        foreground: card.dim
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        bordered: true
        focusable: true
        onClicked: card.cancelled()
        KeyNavigation.tab: nameField
        KeyNavigation.backtab: saveButton
        Keys.onEscapePressed: card.cancelled()
      }
    }
  }
}
