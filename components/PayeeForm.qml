import QtQuick
import qs.Commons
import qs.Ui

// Tidy up a payee: rename it, say what it is usually booked to, give it
// another name it goes by, fold it into another, or remove it.
Overlay {
  id: card
  property var app: null
  // The payee being changed.
  property var editing: null
  // Every payee, for the merge target.
  property var payees: []

  signal submitted(var argv, string doneText)
  signal cancelled()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property bool formFocused:
    nameField.activeFocus || aliasField.activeFocus || categoryBox.popupOpen
    || mergeBox.popupOpen || saveButton.activeFocus || cancelButton.activeFocus
    || mergeButton.activeFocus || removeButton.activeFocus

  readonly property var categoryOptions: [{ value: "", label: "No usual category" }]
    .concat((app ? app.byRecentUse(app.categories) : [])
      .filter(function (c) { return !c.system && !c.archived && c.kind === "expense" })
      .map(function (c) { return { value: c.id, label: (c.parentName ? c.parentName + " / " : "") + c.name } }))
  readonly property var mergeOptions: [{ value: "", label: "Pick a payee" }]
    .concat(card.payees
      .filter(function (p) { return !card.editing || p.id !== card.editing.id })
      .map(function (p) { return { value: p.id, label: p.name } }))
  readonly property var aliases: card.editing && card.editing.aliases ? card.editing.aliases : []

  implicitHeight: col.implicitHeight + Style.space(32)

  function focusFirst() { nameField.forceActiveFocus() }

  function prefill() {
    if (!editing) return
    nameField.text = editing.name || ""
    categoryBox.value = editing.defaultCategoryId || ""
  }
  onEditingChanged: card.prefill()
  Component.onCompleted: card.prefill()

  function submit() {
    if (!card.editing) return
    var name = nameField.text.trim()
    if (name === "") { nameField.forceActiveFocus(); return }
    var id = String(card.editing.id)
    // Changes queue, so a rename and an alias in one submit are two commands.
    if (name !== card.editing.name) card.app.run(["payee", "rename", id, name], "renamed to " + name)
    if (categoryBox.value !== (card.editing.defaultCategoryId || ""))
      card.app.run(["payee", "category", id, categoryBox.value === "" ? "" : categoryBox.value],
                   "set what " + name + " is usually booked to")
    if (aliasField.text.trim() !== "")
      card.app.run(["payee", "alias", id, aliasField.text.trim()], name + " also known as " + aliasField.text.trim())
    card.submitted([], "")
  }

  function merge() {
    if (!card.editing || mergeBox.value === "") { card.app.lastError = "pick the payee to fold it into"; return }
    card.submitted(["payee", "merge", String(card.editing.id), String(mergeBox.value)],
                   "folded " + card.editing.name + " into " + mergeBox.currentLabel())
  }

  function remove() {
    if (!card.editing) return
    card.submitted(["payee", "remove", String(card.editing.id)], "removed " + card.editing.name)
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
      text: card.editing ? card.editing.name : "Payee"
      color: card.fg
      font.family: card.ff
      font.pixelSize: Style.font.title
    }
    Note {
      width: parent.width
      text: card.editing
        ? "On " + card.editing.uses + (card.editing.uses === 1 ? " transaction" : " transactions")
        : ""
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: "NAME" }
      TextField {
        id: nameField
        width: parent.width
        foreground: card.fg
        accent: card.accent
        font.family: card.ff
        font.pixelSize: Style.font.body
        Keys.onReturnPressed: card.submit()
        Keys.onEnterPressed: card.submit()
        Keys.onEscapePressed: card.cancelled()
      }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: "USUALLY BOOKED TO" }
      SearchableDropdown {
        id: categoryBox
        width: parent.width
        showLabel: false
        options: card.categoryOptions
        placeholderText: "Type to find a category"
        triggerLabel: value === "" ? "No usual category" : currentLabel()
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onChanged: function (v) { value = v }
      }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: "ALSO KNOWN AS" }
      Note {
        width: parent.width
        visible: card.aliases.length > 0
        text: card.aliases.join(", ")
      }
      TextField {
        id: aliasField
        width: parent.width
        foreground: card.fg
        accent: card.accent
        font.family: card.ff
        font.pixelSize: Style.font.body
        placeholderText: "Another name it goes by"
        Keys.onReturnPressed: card.submit()
        Keys.onEnterPressed: card.submit()
        Keys.onEscapePressed: card.cancelled()
      }
      Note { width: parent.width; text: "A statement's spelling lands on this payee once it is an alias." }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: "OR FOLD IT INTO" }
      Row {
        width: parent.width
        spacing: Style.space(8)
        SearchableDropdown {
          id: mergeBox
          width: parent.width - Style.space(120) - parent.spacing
          showLabel: false
          options: card.mergeOptions
          placeholderText: "Type to find a payee"
          triggerLabel: value === "" ? "Pick a payee" : currentLabel()
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
        }
        Button {
          id: mergeButton
          width: Style.space(120)
          text: "Fold in"
          tooltipText: "Moves every transaction and keeps this name as an alias"
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          fontSize: Style.font.bodySmall
          bordered: true
          focusable: true
          onClicked: card.merge()
          Keys.onEscapePressed: card.cancelled()
        }
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
        Keys.onEscapePressed: card.cancelled()
      }
      Button {
        id: removeButton
        visible: !!card.editing && card.editing.uses === 0
        text: "Remove"
        tooltipText: "Only a payee no transaction names can go"
        foreground: card.app ? card.app.expense : card.dim
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        bordered: true
        focusable: true
        onClicked: card.remove()
        Keys.onEscapePressed: card.cancelled()
      }
    }
  }
}
