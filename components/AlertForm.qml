import QtQuick
import qs.Commons
import qs.Ui

// Arm a watch on one of the catalogue's paths. Until now only an agent over
// MCP could do this, which is a strange place to keep the one subsystem whose
// whole job is telling a person something.
Overlay {
  id: card
  property var app: null
  // Every path a watch can be armed on, as the daemon describes them.
  property var catalogue: []
  // The watch being changed, or null to arm a new one.
  property var editing: null

  signal submitted(var argv, string doneText)
  signal cancelled()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property bool formFocused:
    pathBox.popupOpen || fieldBox.popupOpen || operatorGroup.activeFocus || whereOp.activeFocus
    || aboveField.activeFocus || belowField.activeFocus || valueField.activeFocus
    || olderField.activeFocus || whereValue.activeFocus || expiresField.activeFocus
    || reasonField.activeFocus || standingToggle.activeFocus
    || saveButton.activeFocus || cancelButton.activeFocus

  property string path: editing ? String(editing.path) : ""
  property string operator: editing ? String(editing.operator) : ""
  property bool standing: editing ? editing.standing === true : false

  readonly property var leaf: {
    for (var i = 0; i < card.catalogue.length; i++)
      if (card.catalogue[i].path === card.path) return card.catalogue[i]
    return null
  }
  readonly property var pathOptions: card.catalogue.map(function (l) {
    return { value: l.path, label: l.path }
  })
  readonly property var operatorOptions: (card.leaf && card.leaf.operators ? card.leaf.operators : [])
    .map(function (o) { return { value: o, label: o } })
  readonly property var fieldOptions: (card.leaf && card.leaf.fields ? card.leaf.fields : [])
    .map(function (f) { return { value: f, label: f } })

  readonly property bool takesBound: card.operator === "count" || card.operator === "crosses"
  readonly property bool takesValue: card.operator === "becomes"
  readonly property bool takesAge: card.operator === "ages"
  readonly property bool takesFilter: !!card.leaf && card.leaf.kind === "list"

  implicitHeight: col.implicitHeight + Style.space(32)

  function focusFirst() { pathBox.forceActiveFocus() }

  function prefill() {
    if (!editing) return
    var p = editing.params || ({})
    aboveField.text = p.above !== undefined && p.above !== null ? String(p.above) : ""
    belowField.text = p.below !== undefined && p.below !== null ? String(p.below) : ""
    valueField.text = p.value || ""
    olderField.text = p.olderThan || ""
    reasonField.text = editing.reason || ""
    var w = editing.where || []
    if (w.length > 0) {
      fieldBox.value = w[0].field || ""
      whereOp.value = w[0].op || "="
      whereValue.text = w[0].value || ""
    }
  }
  onEditingChanged: card.prefill()
  Component.onCompleted: card.prefill()

  // The operator has to be one this path accepts.
  onPathChanged: {
    if (!card.leaf) return
    var ops = card.leaf.operators || []
    if (ops.indexOf(card.operator) === -1) card.operator = ops.length > 0 ? ops[0] : ""
  }

  function submit() {
    if (card.path === "") { card.app.lastError = "pick a path to watch"; return }
    if (card.operator === "") { card.app.lastError = "pick an operator"; return }
    var argv = card.editing
      ? ["alert", "edit", String(card.editing.id), "-path", card.path, "-operator", card.operator]
      : ["arm", card.path, card.operator]
    if (card.takesBound) {
      if (aboveField.text.trim() !== "") argv.push("-above", aboveField.text.trim())
      if (belowField.text.trim() !== "") argv.push("-below", belowField.text.trim())
      if (aboveField.text.trim() === "" && belowField.text.trim() === "") {
        card.app.lastError = card.operator + " needs a bound: above or below"
        return
      }
    }
    if (card.takesValue) {
      if (valueField.text.trim() === "") { valueField.forceActiveFocus(); return }
      argv.push("-value", valueField.text.trim())
    }
    if (card.takesAge) {
      if (olderField.text.trim() === "") { olderField.forceActiveFocus(); return }
      argv.push("-older-than", olderField.text.trim())
      if (fieldBox.value !== "") argv.push("-field", fieldBox.value)
    }
    if (card.takesFilter && whereValue.text.trim() !== "" && fieldBox.value !== "") {
      argv.push("-where", fieldBox.value + whereOp.value + whereValue.text.trim())
    }
    if (expiresField.text.trim() !== "") argv.push("-expires-in", expiresField.text.trim())
    if (card.standing) argv.push("-standing")
    if (reasonField.text.trim() !== "") argv.push("-reason", reasonField.text.trim())
    card.submitted(argv, (card.editing ? "changed the watch on " : "watching ") + card.path)
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
  component Field: TextField {
    foreground: card.fg
    accent: card.accent
    font.family: card.ff
    font.pixelSize: Style.font.body
    Keys.onReturnPressed: card.submit()
    Keys.onEnterPressed: card.submit()
    Keys.onEscapePressed: card.cancelled()
  }

  Column {
    id: col
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    anchors.margins: Style.space(16)
    spacing: Style.space(10)

    Text {
      text: card.editing ? "Change this watch" : "Arm a watch"
      color: card.fg
      font.family: card.ff
      font.pixelSize: Style.font.title
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: "WATCH" }
      SearchableDropdown {
        id: pathBox
        width: parent.width
        showLabel: false
        options: card.pathOptions
        value: card.path
        placeholderText: "Type to find a path"
        triggerLabel: value === "" ? "Pick what to watch" : value
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onChanged: function (v) { card.path = v }
      }
      Note { width: parent.width; text: card.leaf ? card.leaf.describes : "The catalogue is every figure and list the daemon keeps." }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      visible: card.operatorOptions.length > 0
      Label { text: "WHEN IT" }
      ButtonGroup {
        id: operatorGroup
        options: card.operatorOptions
        value: card.operator
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        focusable: true
        onChanged: function (v) { card.operator = v }
        Keys.onEscapePressed: card.cancelled()
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      visible: card.takesBound
      Column {
        width: (parent.width - parent.spacing) * 0.5
        spacing: Style.space(4)
        Label { text: "ABOVE" }
        Field { id: aboveField; width: parent.width; placeholderText: "0" }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.5
        spacing: Style.space(4)
        Label { text: "OR BELOW" }
        Field { id: belowField; width: parent.width; placeholderText: "10" }
      }
    }
    Note {
      width: parent.width
      visible: card.takesBound
      text: "One of the two: a watch fires on the sample that carries the figure past the bound."
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      visible: card.takesValue
      Label { text: "BECOMES" }
      Field { id: valueField; width: parent.width; placeholderText: "true" }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      visible: card.takesAge
      Label { text: "OLDER THAN" }
      Field { id: olderField; width: parent.width; placeholderText: "72h" }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      visible: card.takesFilter
      Label { text: "ONLY THE ONES WHERE" }
      Row {
        width: parent.width
        spacing: Style.space(8)
        Dropdown {
          id: fieldBox
          width: (parent.width - parent.spacing * 2) * 0.35
          showLabel: false
          options: card.fieldOptions
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
        }
        ButtonGroup {
          id: whereOp
          anchors.verticalCenter: parent.verticalCenter
          options: [{ value: "=", label: "is" }, { value: "~=", label: "contains" }]
          value: "="
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          fontSize: Style.font.caption
          focusable: true
          onChanged: function (v) { value = v }
          Keys.onEscapePressed: card.cancelled()
        }
        Field {
          id: whereValue
          width: (parent.width - parent.spacing * 2) * 0.35
          placeholderText: "Checking"
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * 0.4
        spacing: Style.space(4)
        Label { text: "STAYS ARMED FOR" }
        Field { id: expiresField; width: parent.width; placeholderText: "168h" }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.6
        spacing: Style.space(4)
        Label { text: "REASON" }
        Field { id: reasonField; width: parent.width; placeholderText: "Shown with the alarm" }
      }
    }

    Toggle {
      id: standingToggle
      width: parent.width
      label: "Fire every time"
      description: "Off: it rings once and disarms itself"
      checked: card.standing
      foreground: card.fg
      accent: card.accent
      fontFamily: card.ff
      onClicked: card.standing = !card.standing
      Keys.onEscapePressed: card.cancelled()
    }
    Note { width: parent.width; text: "Nothing stays armed for ever; a watch expires when its span runs out." }

    Row {
      spacing: Style.space(8)
      Button {
        id: saveButton
        text: card.editing ? "Save   Enter" : "Arm   Enter"
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
    }
  }
}
