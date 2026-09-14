import QtQuick
import qs.Commons
import qs.Ui

// The full entry form: every field of a transaction, for editing one or
// adding one with more than quick-add offers. Emits argv for the CLI, so the
// daemon validates it like any other entry.
Overlay {
  id: card
  property var app: null
  // The transaction being edited, or null for a new one.
  property var editing: null

  signal submitted(var argv, string doneText)
  signal cancelled()

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  // Split lines come and go, so their fields cannot be named here: each one
  // counts itself in and out instead.
  property int lineFocus: 0

  // A picker counts twice: its popup owns the keys while open, and its
  // trigger keeps the focus after it closes. Without the second term a key
  // pressed right after picking reaches the app as a shortcut.
  readonly property bool formFocused:
    amountField.activeFocus || dateField.activeFocus || descField.activeFocus
    || notesField.activeFocus || tagsField.activeFocus || kindGroup.activeFocus
    || statusGroup.activeFocus || categoryBox.popupOpen || accountBox.popupOpen
    || toBox.popupOpen || rateField.activeFocus || receivedField.activeFocus || payeeField.activeFocus
    || categoryFocus.activeFocus || accountFocus.activeFocus || toFocus.activeFocus
    || card.lineFocus > 0 || saveButton.activeFocus || cancelButton.activeFocus

  property string kind: editing ? String(editing.kind) : "expense"
  property string status: editing && editing.status ? String(editing.status) : "cleared"
  readonly property bool transfer: kind === "transfer"

  // Split across categories rather than booking the whole amount to one.
  property bool split: false

  readonly property var accountOptions: (app ? app.accounts : [])
    .filter(function (a) { return a.active !== false })
    .map(function (a) { return { value: a.id, label: a.name } })
  readonly property var categoryOptions: (app ? app.byRecentUse(app.categories) : [])
    .filter(function (c) { return !c.system && !c.archived && c.kind === (card.kind === "income" ? "income" : "expense") })
    .map(function (c) { return { value: c.id, label: (c.parentName ? c.parentName + " / " : "") + c.name } })

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

  // The field the pickers sit between, which moves with the form's shape.
  function fieldBeforePickers() {
    if (card.crossCurrency) return receivedField
    if (card.foreign) return rateField
    return dateField
  }
  function firstPicker() {
    if (card.transfer) return toBox
    return card.split ? null : categoryBox
  }
  function pickersForward() {
    card.enterPicker(card.firstPicker(), function () {
      card.enterPicker(accountBox, function () { descField.forceActiveFocus() })
    })
  }
  function pickersBackward() {
    card.enterPicker(accountBox, function () {
      card.enterPicker(card.firstPicker(), function () {
        card.fieldBeforePickers().forceActiveFocus()
      })
    })
  }

  // The rate this entry would freeze at, spec 8: a rate typed here wins, and
  // otherwise the table is asked for the one filed on or before the date.
  readonly property var effectiveRate: {
    if (rateField.text.trim() !== "")
      return { rate: rateField.text.trim(), date: card.entryDate }
    return card.app ? card.app.rateFor(card.accountCurrency, card.entryDate) : null
  }

  // The date this entry would carry, which is the date its rate is read at.
  readonly property string entryDate:
    dateField.text.trim() !== "" ? dateField.text.trim() : (app ? app.today() : "")

  function accountOf(id) {
    var list = app ? app.accounts : []
    for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i]
    return null
  }

  // Every statistic is kept in the base currency, so an account in another
  // one needs the rate to freeze the base amount with, spec 8.
  readonly property string baseCurrency: app && app.snap && app.snap.baseCurrency ? String(app.snap.baseCurrency) : ""
  readonly property string accountCurrency: {
    var a = card.accountOf(accountBox.value)
    return a ? String(a.currency) : card.baseCurrency
  }
  readonly property string toCurrency: {
    var a = card.accountOf(toBox.value)
    return a ? String(a.currency) : ""
  }
  readonly property bool foreign: card.accountCurrency !== "" && card.baseCurrency !== ""
                                 && card.accountCurrency !== card.baseCurrency
  readonly property bool crossCurrency: card.transfer && card.toCurrency !== ""
                                        && card.toCurrency !== card.accountCurrency

  implicitHeight: col.implicitHeight + Style.space(32)

  // Last resort for Escape. A key not consumed inside the card climbs to
  // here, which covers anything holding focus that handles nothing itself,
  // such as a picker's trigger once its popup has closed.
  Keys.onEscapePressed: card.cancelled()

  function focusFirst() { amountField.forceActiveFocus() }

  // Minor units as typed text, without separators, for the amount field.
  function plain(minor, currency) {
    var d = app ? app.decimalsFor(currency) : 2
    var s = String(Math.abs(Number(minor) || 0))
    while (s.length <= d) s = "0" + s
    return d > 0 ? s.slice(0, s.length - d) + "." + s.slice(s.length - d) : s
  }

  ListModel { id: lineModel }

  function addLine(categoryId, amount, note) {
    lineModel.append({ category: categoryId || "", amount: amount || "", note: note || "" })
    card.recount()
  }

  // Where an entry lands when nobody names an account. Shown rather than left
  // blank, because the amount is read in that account's currency and a blank
  // box used to mean the label said the base currency while the ledger booked
  // another one.
  readonly property string suggestedAccountId:
    app && app.snap && app.snap.defaultAccountId ? String(app.snap.defaultAccountId) : ""

  function applyDefaults() {
    if (card.editing) return
    if (accountBox.value === "" && card.suggestedAccountId !== "")
      accountBox.value = card.suggestedAccountId
  }
  // app arrives from the loader after this component is built, so the default
  // is applied when it lands, not at completion.
  onAppChanged: card.applyDefaults()
  onSuggestedAccountIdChanged: card.applyDefaults()

  // The loader hands this form its subject after the component is
  // built, so the fields are filled when it arrives, not before.
  function prefill() {
    lineModel.clear()
    card.split = false
    if (!editing) { card.applyDefaults(); return }
    amountField.text = card.plain(editing.amount, editing.currency)
    dateField.text = editing.date || ""
    descField.text = editing.description || ""
    notesField.text = editing.notes || ""
    tagsField.text = (editing.tags || []).join(", ")
    payeeField.text = editing.payee || ""
    categoryBox.value = editing.categoryId || ""
    accountBox.value = editing.accountId || ""
    toBox.value = editing.counterAccountId || ""
    if (editing.fxRate && String(editing.fxRate) !== "1") rateField.text = String(editing.fxRate)
    if (editing.counterAmount !== undefined && editing.counterAmount !== null)
      receivedField.text = card.plain(editing.counterAmount, card.toCurrency)
    var lines = editing.splits || []
    for (var i = 0; i < lines.length; i++)
      card.addLine(lines[i].categoryId, card.plain(lines[i].amount, editing.currency), lines[i].note || "")
    if (lineModel.count > 0) card.split = true
  }

  onEditingChanged: card.prefill()
  Component.onCompleted: card.prefill()

  function tagList() {
    return tagsField.text.split(",").map(function (s) { return s.trim() }).filter(function (s) { return s !== "" }).join(",")
  }

  // ---- the running remainder, so a split is fixed before it is sent
  property real remainder: 0
  property bool remainderKnown: true

  function number(text) {
    var t = String(text || "").trim()
    if (t === "") return NaN
    return Number(t)
  }

  function recount() {
    var total = card.number(amountField.text)
    var sum = 0
    var known = isFinite(total)
    for (var i = 0; i < lineModel.count; i++) {
      var raw = String(lineModel.get(i).amount).trim()
      if (raw === "") continue          // a line not filled in yet is nothing, not an error
      var v = Number(raw)
      if (!isFinite(v)) { known = false; continue }
      sum += v
    }
    card.remainderKnown = known
    card.remainder = known ? total - sum : 0
  }

  readonly property int decimals: app ? app.decimalsFor(card.accountCurrency) : 2
  readonly property bool balanced: card.remainderKnown && Math.abs(card.remainder) < Math.pow(10, -card.decimals) / 2

  function submit() {
    var amount = amountField.text.trim()
    if (amount === "") { amountField.forceActiveFocus(); return }
    if (card.transfer && toBox.value === "") { card.app.lastError = "a transfer needs a destination account"; return }
    if (card.split) {
      if (lineModel.count === 0) { card.app.lastError = "a split needs at least one line"; return }
      for (var i = 0; i < lineModel.count; i++) {
        var l = lineModel.get(i)
        if (String(l.category) === "" || String(l.amount).trim() === "") {
          card.app.lastError = "every line needs a category and an amount"
          return
        }
      }
    }
    var argv
    var done
    if (card.editing) {
      argv = ["edit", String(card.editing.id), "-amount", amount, "-date", dateField.text.trim(),
              "-desc", descField.text.trim(), "-notes", notesField.text.trim(),
              "-tag", card.tagList(), "-payee", payeeField.text.trim(), "-status", card.status]
      if (card.transfer) argv.push("-transfer-to", toBox.value)
      else {
        argv.push(card.kind === "income" ? "-income" : "-expense")
        if (!card.split) argv.push("-category", categoryBox.value)
      }
      if (accountBox.value !== "") argv.push("-account", accountBox.value)
      done = "saved"
    } else {
      argv = ["add", amount]
      if (!card.transfer && !card.split && categoryBox.value !== "") argv.push(categoryBox.value)
      if (card.kind === "income") argv.push("-income")
      if (card.transfer) argv.push("-transfer-to", toBox.value)
      if (accountBox.value !== "") argv.push("-account", accountBox.value)
      if (dateField.text.trim() !== "") argv.push("-date", dateField.text.trim())
      if (descField.text.trim() !== "") argv.push("-desc", descField.text.trim())
      if (notesField.text.trim() !== "") argv.push("-notes", notesField.text.trim())
      if (card.tagList() !== "") argv.push("-tag", card.tagList())
      if (payeeField.text.trim() !== "") argv.push("-payee", payeeField.text.trim())
      if (card.status !== "cleared") argv.push("-status", card.status)
      done = "added " + amount
    }
    if (card.foreign && rateField.text.trim() !== "") argv.push("-rate", rateField.text.trim())
    if (card.crossCurrency && receivedField.text.trim() !== "") argv.push("-received", receivedField.text.trim())
    if (card.split && !card.transfer) {
      for (var n = 0; n < lineModel.count; n++) {
        var line = lineModel.get(n)
        var spec = String(line.category) + "=" + String(line.amount).trim()
        if (String(line.note).trim() !== "") spec += ":" + String(line.note).trim()
        argv.push("-split", spec)
      }
    }
    card.submitted(argv, done)
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

  // What the amount becomes in the base currency, and at which rate. Shown
  // only for an account in another currency, where the answer is not obvious
  // and where a missing rate means the entry will be refused.
  component CurrencyNote: Text {
    readonly property var known: card.effectiveRate
    visible: card.accountCurrency !== "" && card.baseCurrency !== ""
             && card.accountCurrency !== card.baseCurrency
    text: {
      if (!known)
        return "no " + card.accountCurrency + " to " + card.baseCurrency
             + " rate on file for " + card.entryDate + ": the entry will be refused until one is added"
      var at = "at " + String(known.rate) + (known.date ? " (" + String(known.date) + ")" : "")
      var v = card.app ? card.app.evaluate(amountField.text) : NaN
      if (!isFinite(v)) return at
      var d = card.app.decimalsFor(card.baseCurrency)
      var conv = v * Number(known.rate)
      var pow = Math.pow(10, d)
      var rounded = (conv < 0 ? -1 : 1) * Math.round(Math.abs(conv) * pow) / pow
      return "= " + rounded.toFixed(d) + " " + card.baseCurrency + " " + at
    }
    color: known ? card.dim : (card.app ? card.app.urgent : card.fg)
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
      text: card.editing ? "Edit transaction" : "New transaction"
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
      onChanged: function (v) {
        card.kind = v
        categoryBox.value = ""
        if (v === "transfer") card.split = false
        else if (lineModel.count > 0) lineModel.clear()
      }
      KeyNavigation.tab: amountField
      KeyNavigation.backtab: cancelButton
      Keys.onEscapePressed: card.cancelled()
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * 0.55
        spacing: Style.space(4)
        Label { text: card.accountCurrency !== "" ? "AMOUNT, " + card.accountCurrency : "AMOUNT" }
        Field {
          id: amountField
          width: parent.width
          placeholderText: "23.50+18"
          onTextChanged: card.recount()
          KeyNavigation.tab: dateField
          KeyNavigation.backtab: kindGroup
        }
        CurrencyNote { width: parent.width }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.45
        spacing: Style.space(4)
        Label { text: "DATE" }
        Field {
          id: dateField
          width: parent.width
          placeholderText: "YYYY-MM-DD, today if empty"
          KeyNavigation.backtab: amountField
          Keys.onTabPressed: function (event) {
            if (card.foreign) rateField.forceActiveFocus()
            else card.pickersForward()
            event.accepted = true
          }
        }
      }
    }

    // ---- what a foreign account needs, spec 8: the rate is frozen at entry
    Row {
      width: parent.width
      spacing: Style.space(10)
      visible: card.foreign || card.crossCurrency
      Column {
        visible: card.foreign
        width: (parent.width - parent.spacing) * 0.5
        spacing: Style.space(4)
        Label { text: "RATE, 1 " + card.accountCurrency + " IN " + card.baseCurrency }
        Field {
          id: rateField
          width: parent.width
          placeholderText: "0.92"
          KeyNavigation.backtab: dateField
          Keys.onTabPressed: function (event) {
            if (card.crossCurrency) receivedField.forceActiveFocus()
            else card.pickersForward()
            event.accepted = true
          }
        }
      }
      Column {
        visible: card.crossCurrency
        width: (parent.width - parent.spacing) * (card.foreign ? 0.5 : 1)
        spacing: Style.space(4)
        Label { text: "RECEIVED, " + card.toCurrency }
        Field {
          id: receivedField
          width: parent.width
          placeholderText: "what landed in the other account"
          KeyNavigation.backtab: card.foreign ? rateField : dateField
          Keys.onTabPressed: function (event) { card.pickersForward(); event.accepted = true }
        }
      }
    }
    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * 0.55
        spacing: Style.space(4)
        Label { text: card.transfer ? "TO ACCOUNT" : "CATEGORY" }
        // Each picker sits in a FocusScope: a scope reports activeFocus when
        // anything inside it holds focus, which a plain Item does not, and
        // that is what tells the form a trigger is still focused.
        FocusScope {
          id: categoryFocus
          Keys.onEscapePressed: card.cancelled()
          visible: !card.transfer && !card.split
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
        FocusScope {
          id: toFocus
          Keys.onEscapePressed: card.cancelled()
          visible: card.transfer
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
        Text {
          visible: card.split && !card.transfer
          width: parent.width
          text: "Split across the lines below"
          color: card.dim
          font.family: card.ff
          font.pixelSize: Style.font.body
        }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.45
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
            onChanged: function (v) { value = v; card.recount() }
            onPopupOpenChanged: if (!popupOpen) card.leavePicker()
          }
        }
      }
    }

    // ---- split lines, spec 1.3: they must add up to the amount above
    Column {
      width: parent.width
      spacing: Style.space(6)
      visible: !card.transfer

      Row {
        width: parent.width
        spacing: Style.space(10)
        Button {
          text: card.split ? "󰄬  Split into lines" : "Split into lines"
          tooltipText: "Book one amount across several categories"
          foreground: card.split ? card.accent : card.dim
          accent: card.accent
          fontFamily: card.ff
          fontSize: Style.font.caption
          bordered: true
          selected: card.split
          onClicked: {
            card.split = !card.split
            if (card.split && lineModel.count === 0) card.addLine("", "", "")
            card.recount()
          }
        }
        Text {
          anchors.verticalCenter: parent.verticalCenter
          visible: card.split && amountField.text.trim() !== ""
          text: !card.remainderKnown ? "check the amounts"
              : card.balanced ? "the lines add up"
              : (card.remainder > 0 ? "still to place " : "over by ")
                + Math.abs(card.remainder).toFixed(card.decimals) + " " + card.accountCurrency
          color: !card.split ? card.dim
               : card.balanced ? card.dim
               : (card.app ? card.app.expense : card.fg)
          font.family: card.ff
          font.pixelSize: Style.font.caption
        }
      }

      Repeater {
        model: card.split ? lineModel : 0
        delegate: Row {
          id: lineRow
          required property int index
          required property string category
          required property string amount
          required property string note
          width: parent.width
          spacing: Style.space(8)

          SearchableDropdown {
            width: (parent.width - Style.space(28) - parent.spacing * 3) * 0.42
            showLabel: false
            options: card.categoryOptions
            value: lineRow.category
            placeholderText: "Type to find a category"
            triggerLabel: value === "" ? "Category" : currentLabel()
            foreground: card.fg
            accent: card.accent
            fontFamily: card.ff
            onChanged: function (v) { lineModel.setProperty(lineRow.index, "category", v) }
          }
          TextField {
            width: (parent.width - Style.space(28) - parent.spacing * 3) * 0.23
            text: lineRow.amount
            placeholderText: "0.00"
            foreground: card.fg
            accent: card.accent
            font.family: card.ff
            font.pixelSize: Style.font.body
            onTextChanged: {
              lineModel.setProperty(lineRow.index, "amount", text)
              card.recount()
            }
            onActiveFocusChanged: card.lineFocus += activeFocus ? 1 : -1
            Keys.onReturnPressed: card.submit()
            Keys.onEnterPressed: card.submit()
            Keys.onEscapePressed: card.cancelled()
          }
          TextField {
            width: (parent.width - Style.space(28) - parent.spacing * 3) * 0.35
            text: lineRow.note
            placeholderText: "Note, optional"
            foreground: card.fg
            accent: card.accent
            font.family: card.ff
            font.pixelSize: Style.font.body
            onTextChanged: lineModel.setProperty(lineRow.index, "note", text)
            onActiveFocusChanged: card.lineFocus += activeFocus ? 1 : -1
            Keys.onReturnPressed: card.submit()
            Keys.onEnterPressed: card.submit()
            Keys.onEscapePressed: card.cancelled()
          }
          Button {
            text: "󰅙"
            tooltipText: "Take this line out"
            foreground: card.dim
            accent: card.accent
            fontFamily: card.ff
            fontSize: Style.font.caption
            bordered: true
            onClicked: {
              lineModel.remove(lineRow.index)
              if (lineModel.count === 0) card.split = false
              card.recount()
            }
          }
        }
      }

      Row {
        visible: card.split
        spacing: Style.space(8)
        Button {
          text: "Add a line"
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          fontSize: Style.font.caption
          bordered: true
          onClicked: card.addLine("", "", "")
        }
        Button {
          visible: card.remainderKnown && !card.balanced && lineModel.count > 0
          text: "Put the rest on the last line"
          foreground: card.fg
          accent: card.accent
          fontFamily: card.ff
          fontSize: Style.font.caption
          bordered: true
          onClicked: {
            var last = lineModel.count - 1
            var v = card.number(lineModel.get(last).amount)
            if (!isFinite(v)) v = 0
            lineModel.setProperty(last, "amount", (v + card.remainder).toFixed(card.decimals))
            card.recount()
          }
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * 0.6
        spacing: Style.space(4)
        Label { text: "DESCRIPTION" }
        Field {
          id: descField
          width: parent.width
          placeholderText: "What it was"
          KeyNavigation.tab: payeeField
          Keys.onBacktabPressed: function (event) { card.pickersBackward(); event.accepted = true }
        }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.4
        spacing: Style.space(4)
        Label { text: "PAYEE" }
        Field {
          id: payeeField
          width: parent.width
          placeholderText: "Who it went to"
          KeyNavigation.tab: notesField
          KeyNavigation.backtab: descField
        }
      }
    }

    Row {
      width: parent.width
      spacing: Style.space(10)
      Column {
        width: (parent.width - parent.spacing) * 0.55
        spacing: Style.space(4)
        Label { text: "NOTES" }
        Field {
          id: notesField
          width: parent.width
          placeholderText: "Optional"
          KeyNavigation.tab: tagsField
          KeyNavigation.backtab: descField
        }
      }
      Column {
        width: (parent.width - parent.spacing) * 0.45
        spacing: Style.space(4)
        Label { text: "TAGS" }
        Field {
          id: tagsField
          width: parent.width
          placeholderText: "work, client-a"
          KeyNavigation.tab: statusGroup
          KeyNavigation.backtab: notesField
        }
      }
    }

    Column {
      width: parent.width
      spacing: Style.space(4)
      Label { text: "STATUS" }
      ButtonGroup {
        id: statusGroup
        options: [
          { value: "pending", label: "Pending" },
          { value: "cleared", label: "Cleared" },
          { value: "reconciled", label: "Reconciled" }
        ]
        value: card.status
        foreground: card.fg
        accent: card.accent
        fontFamily: card.ff
        fontSize: Style.font.bodySmall
        focusable: true
        onChanged: function (v) { card.status = v }
        KeyNavigation.tab: saveButton
        KeyNavigation.backtab: tagsField
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
        KeyNavigation.backtab: statusGroup
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
