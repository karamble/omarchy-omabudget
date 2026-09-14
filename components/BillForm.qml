import QtQuick
import qs.Commons
import qs.Ui

// Add or edit a recurring rule. Emits argv for the CLI's bill verbs.
Overlay {
  id: card
  property var app: null
  // The rule being edited, or null for a new one.
  property var editing: null

  signal submitted(var argv, string doneText)
  signal cancelled()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property bool formFocused:
    nameField.activeFocus || amountField.activeFocus || startField.activeFocus || endField.activeFocus
    || countField.activeFocus || leadField.activeFocus || intervalField.activeFocus || descField.activeFocus
    || tagsField.activeFocus || kindGroup.activeFocus || categoryBox.popupOpen || accountBox.popupOpen
    || toBox.popupOpen || everyBox.popupOpen || lastToggle.activeFocus || autoToggle.activeFocus
    || variableToggle.activeFocus || saveButton.activeFocus || cancelButton.activeFocus

  readonly property var tpl: editing && editing.template ? editing.template : null
  property string kind: tpl ? String(tpl.kind) : "expense"
  property string every: editing ? String(editing.frequency) : "monthly"
  property bool lastDay: editing ? editing.dayRule === "last" : false
  property bool auto: editing ? editing.autoPost === true : false
  property bool variable: editing ? editing.variableAmount === true : false
  readonly property bool transfer: kind === "transfer"

  readonly property var accountOptions: (app ? app.accounts : [])
    .filter(function (a) { return a.active !== false })
    .map(function (a) { return { value: a.id, label: a.name } })
  readonly property var categoryOptions: (app ? app.byRecentUse(app.categories) : [])
    .filter(function (c) { return !c.system && !c.archived && c.kind === (card.kind === "income" ? "income" : "expense") })
    .map(function (c) { return { value: c.id, label: (c.parentName ? c.parentName + " / " : "") + c.name } })
  readonly property var everyOptions: [
    { value: "daily", label: "Every day" },
    { value: "weekly", label: "Every week" },
    { value: "biweekly", label: "Every two weeks" },
    { value: "monthly", label: "Every month" },
    { value: "quarterly", label: "Every quarter" },
    { value: "semiannual", label: "Twice a year" },
    { value: "annual", label: "Every year" },
    { value: "custom", label: "Every N days" }
  ]

  implicitHeight: col.implicitHeight + Style.space(32)

  function focusFirst() { nameField.forceActiveFocus() }

  function plain(minor, currency) {
    var d = app ? app.decimalsFor(currency) : 2
    var s = String(Math.abs(Number(minor) || 0))
    while (s.length <= d) s = "0" + s
    return d > 0 ? s.slice(0, s.length - d) + "." + s.slice(s.length - d) : s
  }

  // The loader hands this form its subject after the component is
  // built, so the fields are filled when it arrives, not before.
  function prefill() {
    if (!editing) return
    nameField.text = editing.name || ""
    amountField.text = tpl ? card.plain(tpl.amount, tpl.currency) : ""
    categoryBox.value = tpl && tpl.categoryId ? tpl.categoryId : ""
    accountBox.value = tpl && tpl.accountId ? tpl.accountId : ""
    toBox.value = tpl && tpl.counterAccountId ? tpl.counterAccountId : ""
    startField.text = editing.startDate || ""
    endField.text = editing.endDate || ""
    countField.text = editing.occurrenceCount ? String(editing.occurrenceCount) : ""
    leadField.text = editing.leadDays !== undefined ? String(editing.leadDays) : "3"
    intervalField.text = editing.intervalDays ? String(editing.intervalDays) : ""
    descField.text = tpl && tpl.description ? tpl.description : ""
    tagsField.text = tpl && tpl.tags ? tpl.tags.join(", ") : ""
  }

  onEditingChanged: card.prefill()
  Component.onCompleted: card.prefill()

  function tagList() {
    return tagsField.text.split(",").map(function (s) { return s.trim() }).filter(function (s) { return s !== "" }).join(",")
  }

  function submit() {
    var name = nameField.text.trim()
    var amount = amountField.text.trim()
    if (name === "") { nameField.forceActiveFocus(); return }
    if (amount === "") { amountField.forceActiveFocus(); return }
    if (card.transfer && toBox.value === "") { card.app.lastError = "a transfer needs a destination account"; return }
    if (card.every === "custom" && intervalField.text.trim() === "") { intervalField.forceActiveFocus(); return }
    var argv
    if (card.editing) {
      argv = ["bill", "edit", String(card.editing.id), "-name", name, "-amount", amount]
      if (!card.transfer) argv.push("-category", categoryBox.value)
    } else {
      argv = ["bill", "add", name, amount]
      if (!card.transfer && categoryBox.value !== "") argv.push(categoryBox.value)
    }
    argv.push("-every", card.every)
    if (card.every === "custom") argv.push("-interval-days", intervalField.text.trim())
    argv.push("-day", card.lastDay ? "last" : "")
    if (startField.text.trim() !== "" || card.editing) argv.push("-start", startField.text.trim())
    argv.push("-end", endField.text.trim())
    argv.push("-count", countField.text.trim() === "" ? "0" : countField.text.trim())
    if (accountBox.value !== "") argv.push("-account", accountBox.value)
    if (card.transfer) argv.push("-transfer-to", toBox.value)
    else if (card.kind === "income") argv.push("-income")
    else argv.push("-expense")
    argv.push("-auto=" + (card.auto ? "true" : "false"))
    argv.push("-variable=" + (card.variable ? "true" : "false"))
    argv.push("-lead", leadField.text.trim() === "" ? "3" : leadField.text.trim())
    argv.push("-desc", descField.text.trim())
    argv.push("-tag", card.tagList())
    card.submitted(argv, (card.editing ? "saved " : "added ") + name)
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
  component Cell: Column {
    property real share: 0.5
    spacing: Style.space(4)
    width: (parent.width - parent.spacing * (parent.children.length - 1)) * share
  }

  Column {
    id: col
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    anchors.margins: Style.space(16)
    spacing: Style.space(10)

    Text {
      text: card.editing ? "Edit bill" : "New bill"
      color: card.fg
      font.family: card.ff
      font.pixelSize: Style.font.title
    }

    ButtonGroup {
      id: kindGroup
      options: [
        { value: "expense", label: "Expense" },
        { value: "income", label: "Income" },
        { value: "transfer", label: "Transfer" }
      ]
      value: card.kind
      foreground: card.fg
      accent: card.accent
      fontFamily: card.ff
      fontSize: Style.font.bodySmall
      focusable: true
      onChanged: function (v) { card.kind = v; categoryBox.value = "" }
      KeyNavigation.tab: nameField
      KeyNavigation.backtab: cancelButton
      Keys.onEscapePressed: card.cancelled()
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Cell {
        share: 0.6
        Label { text: "NAME" }
        Field { id: nameField; width: parent.width; placeholderText: "Rent"; KeyNavigation.tab: amountField; KeyNavigation.backtab: kindGroup }
      }
      Cell {
        share: 0.4
        Label { text: "AMOUNT" }
        Field { id: amountField; width: parent.width; placeholderText: "1200"; KeyNavigation.tab: startField; KeyNavigation.backtab: nameField }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Cell {
        share: 0.55
        Label { text: card.transfer ? "TO ACCOUNT" : "CATEGORY" }
        SearchableDropdown {
          id: categoryBox
          visible: !card.transfer
          width: parent.width
          showLabel: false
          options: card.categoryOptions
          placeholderText: "Type to find a category"
          triggerLabel: value === "" ? "Uncategorised" : currentLabel()
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
        }
        Dropdown {
          id: toBox
          visible: card.transfer
          width: parent.width
          showLabel: false
          options: card.accountOptions
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
        }
      }
      Cell {
        share: 0.45
        Label { text: card.transfer ? "FROM ACCOUNT" : "ACCOUNT" }
        Dropdown {
          id: accountBox
          width: parent.width
          showLabel: false
          options: card.accountOptions
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Cell {
        share: 0.4
        Label { text: "REPEATS" }
        Dropdown {
          id: everyBox
          width: parent.width
          showLabel: false
          options: card.everyOptions
          value: card.every
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { card.every = v }
        }
      }
      Cell {
        share: 0.2
        visible: card.every === "custom"
        Label { text: "DAYS" }
        Field { id: intervalField; width: parent.width; placeholderText: "30"; KeyNavigation.tab: startField; KeyNavigation.backtab: amountField }
      }
      Cell {
        share: card.every === "custom" ? 0.4 : 0.6
        Label { text: "STARTS" }
        Field { id: startField; width: parent.width; placeholderText: "YYYY-MM-DD, today if empty"; KeyNavigation.tab: endField; KeyNavigation.backtab: amountField }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Cell {
        share: 0.4
        Label { text: "ENDS" }
        Field { id: endField; width: parent.width; placeholderText: "YYYY-MM-DD, optional"; KeyNavigation.tab: countField; KeyNavigation.backtab: startField }
      }
      Cell {
        share: 0.3
        Label { text: "TIMES" }
        Field { id: countField; width: parent.width; placeholderText: "unlimited"; KeyNavigation.tab: leadField; KeyNavigation.backtab: endField }
      }
      Cell {
        share: 0.3
        Label { text: "NOTICE, DAYS" }
        Field { id: leadField; width: parent.width; placeholderText: "3"; KeyNavigation.tab: descField; KeyNavigation.backtab: countField }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Cell {
        share: 0.6
        Label { text: "DESCRIPTION OF EACH POSTING" }
        Field { id: descField; width: parent.width; placeholderText: "The name, if empty"; KeyNavigation.tab: tagsField; KeyNavigation.backtab: leadField }
      }
      Cell {
        share: 0.4
        Label { text: "TAGS" }
        Field { id: tagsField; width: parent.width; placeholderText: "fixed, home"; KeyNavigation.tab: lastToggle; KeyNavigation.backtab: descField }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(12)
      Toggle {
        id: lastToggle
        width: (parent.width - parent.spacing * 2) / 3
        label: "Last day of month"
        checked: card.lastDay
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onClicked: card.lastDay = !card.lastDay
        KeyNavigation.tab: autoToggle
        KeyNavigation.backtab: tagsField
        Keys.onEscapePressed: card.cancelled()
      }
      Toggle {
        id: autoToggle
        width: (parent.width - parent.spacing * 2) / 3
        label: "Post automatically"
        checked: card.auto
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onClicked: card.auto = !card.auto
        KeyNavigation.tab: variableToggle
        KeyNavigation.backtab: lastToggle
        Keys.onEscapePressed: card.cancelled()
      }
      Toggle {
        id: variableToggle
        width: (parent.width - parent.spacing * 2) / 3
        label: "Amount varies"
        checked: card.variable
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onClicked: card.variable = !card.variable
        KeyNavigation.tab: saveButton
        KeyNavigation.backtab: autoToggle
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
        KeyNavigation.backtab: variableToggle
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
        KeyNavigation.tab: kindGroup
        KeyNavigation.backtab: saveButton
        Keys.onEscapePressed: card.cancelled()
      }
    }
  }
}
