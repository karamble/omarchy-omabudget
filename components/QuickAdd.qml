import QtQuick
import qs.Commons
import qs.Ui

// Quick-add, spec 4.1: amount and category, everything else optional, under
// ten seconds. Enter submits, Escape cancels, Tab walks the fields. The card
// emits argv for the CLI rather than touching the daemon itself, so the app
// keeps one mutation path.
Overlay {
  id: card
  property var app: null

  signal submitted(var argv, string doneText)
  signal cancelled()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  // Spelled out as an OR of every field, so a field missed here cannot
  // silently swallow the app's shortcuts. A picker counts twice: its popup
  // owns the keys while open, and its trigger keeps the focus after it
  // closes.
  readonly property bool formFocused:
    amountField.activeFocus || descField.activeFocus || kindGroup.activeFocus
    || saveButton.activeFocus || cancelButton.activeFocus
    || categoryBox.popupOpen || accountBox.popupOpen || toBox.popupOpen
    || categoryFocus.activeFocus || accountFocus.activeFocus || toFocus.activeFocus

  property string kind: "expense"
  readonly property bool transfer: kind === "transfer"

  readonly property var accountOptions: (app ? app.accounts : [])
    .filter(function (a) { return a.active !== false })
    .map(function (a) { return { value: a.id, label: a.name } })
  readonly property var categoryOptions: (app ? app.byRecentUse(app.categories) : [])
    .filter(function (c) { return !c.system && !c.archived && c.kind === (card.kind === "income" ? "income" : "expense") })
    .map(function (c) { return { value: c.id, label: (c.parentName ? c.parentName + " / " : "") + c.name } })

  // Where an entry lands when nobody names an account. Filled in rather than
  // left blank, because the amount is read in that account's currency.
  readonly property string suggestedAccountId:
    app && app.snap && app.snap.defaultAccountId ? String(app.snap.defaultAccountId) : ""

  function applyDefaults() {
    if (accountBox.value === "" && card.suggestedAccountId !== "")
      accountBox.value = card.suggestedAccountId
  }
  // app arrives from the loader after this component is built, so the default
  // lands when it does, not at completion.
  onAppChanged: card.applyDefaults()
  onSuggestedAccountIdChanged: card.applyDefaults()

  // The currency figures are kept in and rates are quoted against.
  readonly property string reference: app ? app.rateReference : ""
  readonly property string accountCurrency: {
    var a = card.app ? card.app.accountOf(accountBox.value) : null
    return a ? String(a.currency) : card.reference
  }
  readonly property string toCurrency: {
    var a = card.app ? card.app.accountOf(toBox.value) : null
    return a ? String(a.currency) : ""
  }
  readonly property string entryDate: app ? app.today() : ""
  readonly property var effectiveRate:
    card.app ? card.app.rateFor(card.accountCurrency, card.entryDate) : null

  // A picker is a popup, not a field, so it cannot sit in the Tab ring: the
  // field on either side steps into it, and it steps back out. Qt hands focus
  // back to whatever held it before a popup opened, so where to go next has
  // to be named on the way in, or Tab would reopen the same picker for ever.
  property var pickerExit: null

  function enterPicker(box, exit) {
    if (!box || !box.visible) { if (exit) exit(); return }
    card.pickerExit = exit
    box.open()
  }

  function leavePicker() {
    if (!card.pickerExit) return
    var next = card.pickerExit
    card.pickerExit = null
    // Deferred: the popup restores focus to its own trigger as it closes, and
    // a move made now would be undone by that.
    Qt.callLater(next)
  }

  function pickersForward() {
    card.enterPicker(card.transfer ? toBox : categoryBox, function () {
      card.enterPicker(accountBox, function () { descField.forceActiveFocus() })
    })
  }
  function pickersBackward() {
    card.enterPicker(accountBox, function () {
      card.enterPicker(card.transfer ? toBox : categoryBox, function () {
        amountField.forceActiveFocus()
      })
    })
  }

  implicitHeight: col.implicitHeight + Style.space(32)

  // Last resort for Escape. A key not consumed inside the card climbs to
  // here, which covers anything holding focus that handles nothing itself,
  // such as a picker's trigger once its popup has closed.
  Keys.onEscapePressed: card.cancelled()

  function focusFirst() { amountField.forceActiveFocus() }

  function submit() {
    var amount = amountField.text.trim()
    if (amount === "") { amountField.forceActiveFocus(); return }
    // A category id is a slug, so it never starts with a dash and never holds
    // a space: it survives being passed as a bare positional.
    var argv = ["add", amount]
    var cat = card.transfer ? "" : categoryBox.value
    if (cat !== "") argv.push(cat)
    if (card.kind === "income") argv.push("-income")
    if (card.transfer) {
      if (toBox.value === "") { card.enterPicker(toBox, null); return }
      // Two currencies need the amount that landed on the other side, and
      // this card has nowhere to put it.
      if (card.toCurrency !== "" && card.toCurrency !== card.accountCurrency) {
        if (card.app) card.app.lastError = "a transfer from " + card.accountCurrency
                                         + " to " + card.toCurrency
                                         + " needs the amount received: use the full form"
        return
      }
      argv.push("-transfer-to", toBox.value)
    }
    // Empty only when there are no accounts at all, and the daemon says so
    // better than an empty reference would.
    if (accountBox.value !== "") argv.push("-account", accountBox.value)
    if (descField.text.trim() !== "") argv.push("-desc", descField.text.trim())
    var done = card.transfer
      ? "moved " + amount + " to " + (card.app ? card.app.accountName(toBox.value) : "")
      : (card.kind === "income" ? "received " : "spent ") + amount
        + (cat !== "" ? " on " + categoryBox.currentLabel() : "")
    card.submitted(argv, done)
  }

  component Label: Text {
    color: card.dimmer
    font.family: card.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }

  // What the amount becomes in the reference currency, and at which rate.
  // Shown only for an account in another currency, where the answer is not
  // obvious and where a currency with no rate at all means the entry will be
  // refused.
  component CurrencyNote: Text {
    readonly property var known: card.effectiveRate
    visible: card.accountCurrency !== "" && card.reference !== ""
             && card.accountCurrency !== card.reference
    text: {
      if (!known)
        return "no " + card.accountCurrency + " to " + card.reference
             + " rate on file: the entry will be refused until one is added"
      var at = "at " + String(known.rate) + (known.date ? " (" + String(known.date) + ")" : "")
      if (known.ahead) at += ", the latest on file"
      var v = card.app ? card.app.evaluate(amountField.text) : NaN
      if (!isFinite(v)) return at
      var d = card.app.decimalsFor(card.reference)
      var conv = v * Number(known.rate)
      var pow = Math.pow(10, d)
      var rounded = (conv < 0 ? -1 : 1) * Math.round(Math.abs(conv) * pow) / pow
      return "= " + rounded.toFixed(d) + " " + card.reference + " " + at
    }
    color: known ? card.dim : (card.app ? card.app.urgent : card.fg)
    font.family: card.ff
    font.pixelSize: Style.font.caption
    wrapMode: Text.WordWrap
  }

  // One key behaviour for every field: Enter submits, Escape cancels.
  component Field: TextField {
    foreground: card.fg
    accent: card.accent
    font.family: card.ff
    font.pixelSize: Style.font.body
    width: parent.width
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
      text: "Quick add"
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
      KeyNavigation.tab: amountField
      KeyNavigation.backtab: cancelButton
      Keys.onEscapePressed: card.cancelled()
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: card.accountCurrency !== "" ? "AMOUNT, " + card.accountCurrency : "AMOUNT" }
      Field {
        id: amountField
        placeholderText: "Amount, arithmetic allowed: 23.50+18"
        KeyNavigation.backtab: kindGroup
        // Tab steps straight into the picker and opens it, so typing carries
        // on without a keystroke spent opening anything.
        Keys.onTabPressed: function (event) { card.pickersForward(); event.accepted = true }
      }
      CurrencyNote { width: parent.width }
    }

    // Each picker sits in a FocusScope: a scope reports activeFocus when
    // anything inside it holds focus, which a plain Item does not, and that
    // is what tells the card a trigger is still focused.
    Column {
      width: parent.width
      spacing: Style.space(4)
      visible: !card.transfer
      Label { text: "CATEGORY" }
      FocusScope {
        id: categoryFocus
        Keys.onEscapePressed: card.cancelled()
        width: parent.width
        height: categoryBox.implicitHeight
        SearchableDropdown {
          id: categoryBox
          anchors.fill: parent
          showLabel: false
          options: card.categoryOptions
          placeholderText: "Type to find a category"
          triggerLabel: value === "" ? "Uncategorised" : currentLabel()
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
          onPopupOpenChanged: if (!popupOpen) card.leavePicker()
        }
      }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      visible: card.transfer
      Label { text: "TO ACCOUNT" }
      FocusScope {
        id: toFocus
        Keys.onEscapePressed: card.cancelled()
        width: parent.width
        height: toBox.implicitHeight
        Dropdown {
          id: toBox
          anchors.fill: parent
          showLabel: false
          options: card.accountOptions
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
          onPopupOpenChanged: if (!popupOpen) card.leavePicker()
        }
      }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: card.transfer ? "FROM ACCOUNT" : "ACCOUNT" }
      FocusScope {
        id: accountFocus
        Keys.onEscapePressed: card.cancelled()
        width: parent.width
        height: accountBox.implicitHeight
        Dropdown {
          id: accountBox
          anchors.fill: parent
          showLabel: false
          options: card.accountOptions
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          onChanged: function (v) { value = v }
          onPopupOpenChanged: if (!popupOpen) card.leavePicker()
        }
      }
    }

    Field {
      id: descField
      placeholderText: "Description (optional)"
      KeyNavigation.tab: saveButton
      Keys.onBacktabPressed: function (event) { card.pickersBackward(); event.accepted = true }
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
        KeyNavigation.backtab: descField
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

    Text {
      text: "Amount and category are enough. Tab from the amount to find a category: Enter picks it, Enter again saves. Escape closes a picker, then the card."
      color: card.dimmer
      font.family: card.ff
      font.pixelSize: Style.font.caption
      wrapMode: Text.WordWrap
      width: parent.width
    }
  }
}
