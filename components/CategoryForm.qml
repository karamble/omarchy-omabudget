import QtQuick
import qs.Commons
import qs.Ui

// Add or edit a category. Emits argv for the CLI's category verbs, so one
// submit is one command and the daemon validates it.
Overlay {
  id: card
  property var app: null
  // The category being edited, or null for a new one.
  property var editing: null
  // Which group a new category starts under.
  property string parentHint: ""

  signal submitted(var argv, string doneText)
  signal removeRequested(var category)
  signal cancelled()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property bool formFocused:
    nameField.activeFocus || iconField.activeFocus || kindGroup.activeFocus
    || goalField.activeFocus || goalDueField.activeFocus
    || behaviourGroup.activeFocus || parentBox.popupOpen || excludeToggle.activeFocus
    || archiveToggle.activeFocus || saveButton.activeFocus || cancelButton.activeFocus
    || removeButton.activeFocus

  property string parentId: editing ? String(editing.parentId || "") : parentHint
  property string kind: editing ? String(editing.kind) : "expense"
  property string behaviour: editing && editing.behaviour ? String(editing.behaviour) : "monthly"
  property bool excluded: editing ? editing.excludedFromStatistics === true : false
  property bool archived: editing ? editing.archived === true : false
  readonly property bool expense: card.kind === "expense"

  readonly property bool isChild: card.parentId !== ""
  readonly property var groups: (app ? app.categories : [])
    .filter(function (c) { return !c.parentId && !c.system && !c.archived })
  readonly property var groupOptions: [{ value: "", label: "A group of its own" }]
    .concat(card.groups.map(function (c) { return { value: c.id, label: c.name + "  " + c.kind } }))

  // Goal is not offered until a target exists; the Budget screen sets that.
  readonly property var behaviourOptions: {
    var out = [
      { value: "monthly", label: "Monthly" },
      { value: "rollover", label: "Rollover" },
      { value: "untracked", label: "Untracked" }
    ]
    if (card.editing && card.editing.goalTarget > 0) out.push({ value: "goal", label: "Goal" })
    return out
  }

  implicitHeight: col.implicitHeight + Style.space(32)

  function focusFirst() { nameField.forceActiveFocus() }

  // The loader hands this form its subject after the component is
  // built, so the fields are filled when it arrives, not before.
  function prefill() {
    if (!editing) return
    nameField.text = editing.name || ""
    iconField.text = editing.icon || ""
    goalField.text = editing.goalTarget > 0 ? card.plain(editing.goalTarget) : ""
    goalDueField.text = editing.goalDue || ""
  }

  // Minor units as typed text, for the goal target.
  function plain(minor) {
    var cur = app && app.snap ? app.snap.baseCurrency : ""
    var d = app ? app.decimalsFor(cur) : 2
    var v = String(Math.abs(Number(minor) || 0))
    while (v.length <= d) v = "0" + v
    return d > 0 ? v.slice(0, v.length - d) + "." + v.slice(v.length - d) : v
  }

  onEditingChanged: card.prefill()
  Component.onCompleted: card.prefill()

  // A child takes its parent's kind, so the picker follows the dropdown.
  onParentIdChanged: {
    if (card.editing || card.parentId === "") return
    for (var i = 0; i < card.groups.length; i++)
      if (card.groups[i].id === card.parentId) card.kind = card.groups[i].kind
  }

  function submit() {
    var name = nameField.text.trim()
    if (name === "") { nameField.forceActiveFocus(); return }
    var icon = iconField.text.trim()
    var goal = goalField.text.trim()
    var argv
    if (card.editing) {
      argv = ["category", "edit", String(card.editing.id), "-name", name, "-icon", icon,
              "-exclude=" + (card.excluded ? "true" : "false"),
              "-archived=" + (card.archived ? "true" : "false")]
      // A goal sets the behaviour itself, so only one of the two is sent.
      if (card.expense && (goal !== "" || card.editing.goalTarget > 0)) {
        argv.push("-goal", goal === "" ? "0" : goal, "-goal-due", goalDueField.text.trim())
        if (goal === "" || goal === "0") argv.push("-behaviour", card.behaviour)
      } else {
        argv.push("-behaviour", card.behaviour)
      }
    } else {
      argv = ["category", "add", name]
      if (card.parentId !== "") argv.push("-parent", card.parentId)
      else argv.push("-kind", card.kind)
      if (icon !== "") argv.push("-icon", icon)
      argv.push("-behaviour", card.behaviour)
      if (card.excluded) argv.push("-exclude=true")
    }
    card.submitted(argv, (card.editing ? "saved " : "added ") + name)
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
      text: card.editing ? "Edit category" : (card.isChild ? "New category" : "New group")
      color: card.fg
      font.family: card.ff
      font.pixelSize: Style.font.title
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * (card.editing ? 1 : 0.55)
        spacing: Style.space(4)
        Label { text: "NAME" }
        TextField {
          id: nameField
          width: parent.width
          foreground: card.fg
          accent: card.accent
          font.family: card.ff
          font.pixelSize: Style.font.body
          placeholderText: card.isChild ? "Coffee & snacks" : "Food"
          Keys.onReturnPressed: card.submit()
          Keys.onEnterPressed: card.submit()
          Keys.onEscapePressed: card.cancelled()
        }
        Note {
          width: parent.width
          visible: !!card.editing
          text: "The id and every transaction behind it stay as they are."
        }
      }
      Column {
        visible: !card.editing
        width: (parent.width - parent.spacing) * 0.45
        spacing: Style.space(4)
        Label { text: "UNDER" }
        Dropdown {
          id: parentBox
          width: parent.width
          showLabel: false
          options: card.groupOptions
          value: card.parentId
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { card.parentId = v }
        }
      }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      visible: !card.editing && !card.isChild
      Label { text: "KIND" }
      ButtonGroup {
        id: kindGroup
        options: [
          { value: "expense", label: "Expense" },
          { value: "income", label: "Income" }
        ]
        value: card.kind
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        focusable: true
        onChanged: function (v) { card.kind = v }
        Keys.onEscapePressed: card.cancelled()
      }
    }
    Note {
      width: parent.width
      visible: card.isChild
      text: card.editing
        ? (card.editing.parentName ? "Under " + card.editing.parentName + ", " + card.kind : card.kind)
        : "It takes its group's kind: " + card.kind + "."
    }

    Column {
      width: parent.width
      spacing: Style.space(6)
      Label { text: "GLYPH" }
      Row {
        width: parent.width
        spacing: Style.space(10)
        TextField {
          id: iconField
          width: Style.space(90)
          anchors.verticalCenter: parent.verticalCenter
          foreground: card.fg
          accent: card.accent
          font.family: card.ff
          font.pixelSize: Style.font.body
          placeholderText: "none"
          Keys.onReturnPressed: card.submit()
          Keys.onEnterPressed: card.submit()
          Keys.onEscapePressed: card.cancelled()
        }
        Note {
          anchors.verticalCenter: parent.verticalCenter
          width: parent.width - Style.space(100)
          text: "Groups carry the glyph; a child without one borrows it."
        }
      }
      Grid {
        width: parent.width
        columns: 11
        spacing: Style.space(4)
        Repeater {
          model: ["", "󰄔", "󰃖", "󰈙", "󰄨", "󰒃", "󰌋", "󰦖", "󰓅", "󰋜", "󱐋", "󰖩", "󰍹", "󰄛", "󰅶", "󰄋", "󰀝", "󰀄", "󰠖", "󰋠", "󰔟", "󰆘", "󰝚", "󰋋", "󱚣", "󰃭", "󰑐", "󰒓", "󰆦", "󱇢", "", "󰂚", "󱓞"]
          delegate: Rectangle {
            required property var modelData
            width: Style.space(28)
            height: Style.space(28)
            radius: Style.cornerRadius
            readonly property bool current: iconField.text === modelData
            color: current ? Style.selectedAccentFill : "transparent"
            border.width: Style.normalBorderWidth
            border.color: current ? Style.selectedBorderColor : (card.app ? card.app.cardBorder : Style.normalBorderColor)
            Text {
              anchors.centerIn: parent
              text: modelData === "" ? "·" : modelData
              color: parent.current ? card.accent : card.dim
              font.family: card.ff
              font.pixelSize: Style.font.icon
            }
            MouseArea {
              anchors.fill: parent
              cursorShape: Qt.PointingHandCursor
              onClicked: iconField.text = modelData
            }
          }
        }
      }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: "POT BEHAVIOUR" }
      ButtonGroup {
        id: behaviourGroup
        options: card.behaviourOptions
        value: card.behaviour
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        focusable: true
        onChanged: function (v) { card.behaviour = v }
        Keys.onEscapePressed: card.cancelled()
      }
      Note {
        width: parent.width
        text: "Under the envelope model: monthly starts again each period, rollover keeps what is left or short, untracked is never budgeted."
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      visible: card.expense && !!card.editing
      Column {
        width: (parent.width - parent.spacing) * 0.5
        spacing: Style.space(4)
        Label { text: "SAVE TOWARD, 0 CLEARS" }
        TextField {
          id: goalField
          width: parent.width
          foreground: card.fg
          accent: card.accent
          font.family: card.ff
          font.pixelSize: Style.font.body
          placeholderText: "1200"
          Keys.onReturnPressed: card.submit()
          Keys.onEnterPressed: card.submit()
          Keys.onEscapePressed: card.cancelled()
        }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.5
        spacing: Style.space(4)
        Label { text: "BY, YYYY-MM" }
        TextField {
          id: goalDueField
          width: parent.width
          foreground: card.fg
          accent: card.accent
          font.family: card.ff
          font.pixelSize: Style.font.body
          placeholderText: "2027-06"
          Keys.onReturnPressed: card.submit()
          Keys.onEnterPressed: card.submit()
          Keys.onEscapePressed: card.cancelled()
        }
      }
    }
    Note {
      width: parent.width
      visible: card.expense && !!card.editing
      text: "A goal keeps what is left each period, and the Budget screen suggests what to put in to reach the target in time."
    }

    Row {
      width: parent.width
      spacing: Style.space(12)
      Toggle {
        id: excludeToggle
        width: (parent.width - parent.spacing) * 0.5
        label: "Keep out of statistics"
        description: "Totals and reports skip it"
        checked: card.excluded
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onClicked: card.excluded = !card.excluded
        Keys.onEscapePressed: card.cancelled()
      }
      Toggle {
        id: archiveToggle
        visible: !!card.editing
        width: (parent.width - parent.spacing) * 0.5
        label: "Archived"
        description: "Out of the pickers, history kept"
        checked: card.archived
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        onClicked: card.archived = !card.archived
        Keys.onEscapePressed: card.cancelled()
      }
    }
    Note {
      width: parent.width
      visible: !card.editing || !card.isChild
      text: "A group hands both of those down to the categories under it."
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
        visible: !!card.editing
        text: "Remove"
        tooltipText: "Only a category nothing points at can go; anything with history is archived instead"
        foreground: card.app ? card.app.expense : card.dim
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        bordered: true
        focusable: true
        onClicked: card.removeRequested(card.editing)
        Keys.onEscapePressed: card.cancelled()
      }
    }
  }
}
