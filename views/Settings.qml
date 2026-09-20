import QtQuick
import qs.Commons
import qs.Ui
import "../components"

// Settings: the budgeting model, the period, exchange rates, alerts, the
// agent endpoint and the token. Every change goes through the CLI like
// everything else, the rate fetch included: it runs only from the button
// here or the command.
Item {
  id: view
  property var app: null
  readonly property bool formFocused: baseField.activeFocus || startField.activeFocus || largeField.activeFocus
    || modelGroup.activeFocus || monitoringToggle.activeFocus || mcpToggle.activeFocus
    || sourceGroup.activeFocus || sourceUrlField.activeFocus

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  property var settings: null
  property var health: null
  property var endpoint: null
  // The rate sources a fetch can read from, and what the last press did.
  property var sources: []
  property var fetched: null
  property bool fetching: false
  readonly property var chosenSource: {
    var id = view.settings ? String(view.settings.rateSource || "") : ""
    for (var i = 0; i < view.sources.length; i++)
      if (view.sources[i].id === id) return view.sources[i]
    return null
  }
  readonly property string reference: view.settings ? String(view.settings.rateReference || "") : ""

  function money(minor) { return app && settings ? app.fmt(minor, settings.baseCurrency) : String(minor) }
  function plain(minor) {
    var d = app && settings ? app.decimalsFor(settings.baseCurrency) : 2
    var s = String(Math.abs(Number(minor) || 0))
    while (s.length <= d) s = "0" + s
    return d > 0 ? s.slice(0, s.length - d) + "." + s.slice(s.length - d) : s
  }

  function reload() {
    if (!app) return
    app.query(["settings"], function (data, err) {
      if (err) { view.app.lastError = err; return }
      view.settings = data
      if (!baseField.activeFocus) baseField.text = String(data.baseCurrency || "")
      if (!startField.activeFocus) startField.text = String(data.periodStartDay)
      if (!largeField.activeFocus) largeField.text = data.largeAmount > 0 ? view.plain(data.largeAmount) : ""
      if (!sourceUrlField.activeFocus) sourceUrlField.text = String(data.rateSourceUrl || "")
    })
    app.query(["health"], function (data, err) { if (!err) view.health = data })
    app.query(["mcp"], function (data, err) { if (!err) view.endpoint = data })
    app.query(["rate", "sources"], function (data, err) { if (!err) view.sources = Array.isArray(data) ? data : [] })
  }
  onAppChanged: reload()
  Connections {
    target: view.app
    function onChanged() { view.reload() }
  }

  function setModel(m) { app.run(["settings", "model", m], "budgeting model: " + m) }
  function applyBase() {
    var code = baseField.text.trim().toUpperCase()
    if (!/^[A-Z]{3}$/.test(code)) { view.app.lastError = "a currency is three letters"; return }
    app.run(["settings", "base-currency", code], "figures are shown in " + code)
  }
  function applyStart() {
    var n = Number(startField.text.trim())
    if (!(n >= 1 && n <= 28)) { view.app.lastError = "the period start day is between 1 and 28"; return }
    app.run(["settings", "period-start", String(n)], "periods now begin on day " + n)
  }
  function applyLarge() {
    var v = largeField.text.trim()
    app.run(["settings", "large-amount", v === "" ? "0" : v], v === "" ? "large-amount alerts off" : "large amount set to " + v)
  }
  function setSource(id) {
    var name = id
    for (var i = 0; i < view.sources.length; i++) if (view.sources[i].id === id) name = view.sources[i].name
    app.run(["settings", "rate-source", id], "rates are read from " + name)
  }
  function applySourceUrl() {
    if (!view.settings) return
    var url = sourceUrlField.text.trim()
    app.run(["settings", "rate-source", String(view.settings.rateSource), "-url", url],
            url === "" ? "rates are read from the public instance" : "rates are read from " + url)
  }
  // The one press that opens a connection outward. It goes through query
  // rather than run because the result is what is shown, and every view
  // is told afterwards since the rates it filed move figures everywhere.
  function fetchRates() {
    if (view.fetching || !app) return
    view.fetching = true
    app.query(["rate", "fetch"], function (data, err) {
      view.fetching = false
      if (err) { view.fetched = null; view.app.lastError = err; return }
      view.fetched = data
      view.app.lastError = ""
      view.app.refresh()
      view.app.changed()
    })
  }
  function acceptHeld(h) {
    if (!view.fetched) return
    app.run(["rate", "accept", String(h.currency), String(h.rate), "-date", String(h.date), "-source", String(view.fetched.source)],
            "filed 1 " + h.currency + " = " + h.rate + " " + view.reference)
    var rest = []
    var held = view.fetched.held || []
    for (var i = 0; i < held.length; i++) if (held[i].currency !== h.currency) rest.push(held[i])
    var next = Object.assign({}, view.fetched)
    next.held = rest
    view.fetched = next
  }
  function lastFetchText() {
    var f = view.settings ? view.settings.lastFetch : null
    if (!f) return "Never used"
    return "Last used " + String(f.at || "").slice(0, 10) + ", " + String(f.host || "")
  }

  function handleKey(e) {
    if (e.modifiers !== Qt.NoModifier) return false
    if (e.key === Qt.Key_R) { view.reload(); return true }
    return false
  }

  component Caption: Text {
    color: view.dimmer
    font.family: view.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }
  component Body: Text {
    color: view.fg
    font.family: view.ff
    font.pixelSize: Style.font.body
    wrapMode: Text.WordWrap
  }
  component Note: Text {
    color: view.dim
    font.family: view.ff
    font.pixelSize: Style.font.caption
    wrapMode: Text.WordWrap
  }
  component Apply: Button {
    foreground: view.fg
    accent: view.accent
    fontFamily: view.ff
    fontSize: Style.font.caption
    bordered: true
  }

  Flickable {
    anchors.fill: parent
    contentWidth: width
    contentHeight: col.implicitHeight
    interactive: contentHeight > height
    boundsBehavior: Flickable.StopAtBounds
    clip: true

    Column {
      id: col
      width: parent.width
      spacing: Style.space(14)

      Text {
        text: "Settings"
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }

      Row {
        width: parent.width
        spacing: Style.space(12)
        readonly property real colW: (width - spacing) / 2

        Column {
          width: parent.colW
          spacing: Style.space(12)

          TitledCard {

            app: view.app
            width: parent.width
            title: "BUDGETING"
            Caption { text: "SHOW FIGURES IN" }
            Row {
              spacing: Style.space(8)
              TextField {
                id: baseField
                width: Style.space(70)
                foreground: view.fg
                accent: view.accent
                font.family: view.ff
                font.pixelSize: Style.font.body
                placeholderText: view.settings ? view.settings.rateReference : "EUR"
                Keys.onReturnPressed: view.applyBase()
                Keys.onEnterPressed: view.applyBase()
                Keys.onEscapePressed: view.forceActiveFocus()
              }
              Apply { text: "Apply"; onClicked: view.applyBase() }
            }
            Note {
              width: parent.width
              text: "Every figure is converted into it at today's rate on file, so it needs a rate unless it is "
                + (view.settings ? view.settings.rateReference : "the reference")
                + ", which the ledger keeps its figures in and quotes every rate against. Nothing stored moves when this changes."
            }
            Caption { text: "MODEL"; topPadding: Style.space(6) }
            ButtonGroup {
              id: modelGroup
              options: [
                { value: "limits", label: "Category limits" },
                { value: "envelope", label: "Envelopes" }
              ]
              value: view.settings ? view.settings.model : "limits"
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.bodySmall
              focusable: true
              onChanged: function (v) { view.setModel(v) }
            }
            Note { width: parent.width; text: "Limits: a planned amount per category, overspending is shown. Envelopes: money is assigned to pots first, what is left rolls over by each category's behaviour." }
            Caption { text: "PERIOD BEGINS ON DAY"; topPadding: Style.space(6) }
            Row {
              spacing: Style.space(8)
              TextField {
                id: startField
                width: Style.space(70)
                foreground: view.fg
                accent: view.accent
                font.family: view.ff
                font.pixelSize: Style.font.body
                placeholderText: "1"
                Keys.onReturnPressed: view.applyStart()
                Keys.onEnterPressed: view.applyStart()
                Keys.onEscapePressed: view.forceActiveFocus()
              }
              Apply { text: "Apply"; onClicked: view.applyStart() }
            }
            Note { width: parent.width; text: "1 to 28. Pick your payday and every period, statistic and budget follows it." }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "EXCHANGE RATES"
            Caption { text: "SOURCE" }
            ButtonGroup {
              id: sourceGroup
              options: view.sources.map(function (s) { return { value: s.id, label: s.name } })
              value: view.settings ? String(view.settings.rateSource || "") : ""
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.bodySmall
              focusable: true
              onChanged: function (v) { view.setSource(v) }
            }
            Note { width: parent.width; text: view.chosenSource ? view.chosenSource.what : "Choosing a source is choosing who sees your address when you press Fetch now." }
            Caption { text: "READS FROM"; topPadding: Style.space(6); visible: view.chosenSource !== null && view.chosenSource.custom === true }
            Row {
              spacing: Style.space(8)
              visible: view.chosenSource !== null && view.chosenSource.custom === true
              TextField {
                id: sourceUrlField
                width: Style.space(260)
                foreground: view.fg
                accent: view.accent
                font.family: view.ff
                font.pixelSize: Style.font.body
                placeholderText: view.chosenSource ? String(view.chosenSource.url || "") : ""
                Keys.onReturnPressed: view.applySourceUrl()
                Keys.onEnterPressed: view.applySourceUrl()
                Keys.onEscapePressed: view.forceActiveFocus()
              }
              Apply { text: "Apply"; onClicked: view.applySourceUrl() }
            }
            Note {
              width: parent.width
              visible: view.chosenSource !== null && view.chosenSource.custom === true
              text: "Empty reads the public instance. An instance you run yourself is read over https, or plain http only on this machine or a private address."
            }
            Row {
              spacing: Style.space(8)
              topPadding: Style.space(6)
              Apply {
                text: view.fetching ? "Fetching" : "Fetch now"
                enabled: !view.fetching && view.settings !== null
                tooltipText: "One request to the source above, every currency it publishes"
                onClicked: view.fetchRates()
              }
              Note { anchors.verticalCenter: parent.verticalCenter; text: "Network: " + view.lastFetchText() }
            }
            Note {
              width: parent.width
              text: "Nothing is fetched unless you press this or run omabudget rate fetch. The whole list is read, so the request says nothing about what you hold; a rate you typed is never overwritten. Bitcoin, Decred, Litecoin and Ether are on neither source and stay hand entered under Manage."
            }
            Column {
              width: parent.width
              spacing: Style.space(6)
              visible: view.fetched !== null
              Caption { text: "LAST PRESS"; topPadding: Style.space(6) }
              Body {
                width: parent.width
                text: view.fetched ? "Read " + view.fetched.name + " at " + view.fetched.host + ", published " + view.fetched.published : ""
              }
              Body {
                width: parent.width
                visible: view.fetched && (view.fetched.filed || []).length > 0
                text: view.fetched ? "Filed: " + (view.fetched.filed || []).join(", ") : ""
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.unchanged || []).length > 0
                text: view.fetched ? "Already on file for that day: " + (view.fetched.unchanged || []).join(", ") : ""
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.kept || []).length > 0
                text: view.fetched ? "Kept as typed by hand: " + (view.fetched.kept || []).join(", ") : ""
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.filed || []).length + (view.fetched.unchanged || []).length + (view.fetched.kept || []).length + (view.fetched.held || []).length === 0
                text: "Nothing to file: no currency in use is quoted by this source."
              }
              Repeater {
                model: view.fetched ? (view.fetched.held || []) : []
                delegate: Column {
                  required property var modelData
                  width: parent ? parent.width : 0
                  spacing: Style.space(2)
                  Body {
                    width: parent.width
                    text: "Held: 1 " + modelData.currency + " = " + modelData.rate + " " + view.reference
                      + (modelData.previous ? "  (was " + modelData.previous + " on " + modelData.previousDate + ")" : "")
                  }
                  Note { width: parent.width; text: modelData.reason }
                  Apply { text: "Apply " + modelData.currency; onClicked: view.acceptHeld(modelData) }
                }
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.unquoted || []).length > 0
                text: view.fetched ? "Not quoted by " + view.fetched.name + ", entered by hand: " + (view.fetched.unquoted || []).join(", ") : ""
              }
              Repeater {
                model: view.fetched ? (view.fetched.notes || []) : []
                delegate: Note {
                  required property var modelData
                  width: parent ? parent.width : 0
                  text: String(modelData)
                }
              }
              Note {
                width: parent.width
                visible: view.fetched && view.fetched.rederived > 0
                text: view.fetched ? "Re-derived " + view.fetched.rederived + " transactions" : ""
              }
            }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "ALERTS"
            Toggle {
              id: monitoringToggle
              width: parent.width
              label: "Evaluate alert triggers"
              description: "Budget lines at warn or over, bills due or overdue, large postings, low accounts"
              checked: view.settings ? view.settings.monitoring === true : true
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              onClicked: view.app.run(["monitoring", checked ? "off" : "on"], "monitoring " + (checked ? "off" : "on"))
            }
            Caption { text: "LARGE AMOUNT"; topPadding: Style.space(6) }
            Row {
              spacing: Style.space(8)
              TextField {
                id: largeField
                width: Style.space(140)
                foreground: view.fg
                accent: view.accent
                font.family: view.ff
                font.pixelSize: Style.font.body
                placeholderText: "off"
                Keys.onReturnPressed: view.applyLarge()
                Keys.onEnterPressed: view.applyLarge()
                Keys.onEscapePressed: view.forceActiveFocus()
              }
              Apply { text: "Apply"; onClicked: view.applyLarge() }
            }
            Note { width: parent.width; text: "Postings at or above this amount appear in the large-postings list that triggers can watch. Empty turns it off." }
          }
        }

        Column {
          width: parent.colW
          spacing: Style.space(12)

          TitledCard {

            app: view.app
            width: parent.width
            title: "AGENT ENDPOINT"
            Toggle {
              id: mcpToggle
              width: parent.width
              label: "Answer agents over MCP"
              description: "Tools on 127.0.0.1 only, behind the token below"
              checked: view.health ? view.health.mcpEnabled === true : false
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              onClicked: view.app.run(["mcp-endpoint", checked ? "off" : "on"], "agent endpoint " + (checked ? "off" : "on"))
            }
            Caption { text: "FOR YOUR AGENT'S CONFIG"; topPadding: Style.space(6) }
            Rectangle {
              width: parent.width
              height: snippet.implicitHeight + Style.space(16)
              radius: Style.cornerRadius
              color: Qt.rgba(view.fg.r, view.fg.g, view.fg.b, 0.05)
              Text {
                id: snippet
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.margins: Style.space(8)
                text: view.endpoint
                  ? "\"omabudget\": {\n  \"type\": \"http\",\n  \"url\": \"" + view.endpoint.url + "\",\n  \"headers\": { \"Authorization\": \"Bearer " + (view.app && view.app.blurAmounts ? "••••••••" : view.endpoint.apiToken) + "\" }\n}"
                  : "Run: omabudget mcp"
                color: view.dim
                font.family: view.ff
                font.pixelSize: Style.font.caption
                wrapMode: Text.WrapAnywhere
              }
            }
            Row {
              spacing: Style.space(8)
              Apply {
                text: "Recycle the token"
                tooltipText: "Every client holding the old token is locked out"
                onClicked: view.app.run(["recycle"], "a new token is in place")
              }
              Note { anchors.verticalCenter: parent.verticalCenter; text: "h hides the token with the amounts" }
            }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "THE DAEMON"
            Body { text: (view.app && view.app.running ? "Running" : "Not running") + "  ·  " + (view.app ? view.app.addr : "") }
            Body { text: "Version " + (view.app && view.app.manifest && view.app.manifest.version ? view.app.manifest.version : "dev") }
            Note { width: parent.width; text: "Listens on the loopback address only. Every read and write, this window included, goes through the CLI and the token." }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "THE WINDOW"
            Note { width: parent.width; text: "Hyprland tiles this window unless told otherwise. Add to ~/.config/hypr/bindings.lua:" }
            Rectangle {
              width: parent.width
              height: rule.implicitHeight + Style.space(16)
              radius: Style.cornerRadius
              color: Qt.rgba(view.fg.r, view.fg.g, view.fg.b, 0.05)
              Text {
                id: rule
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.margins: Style.space(8)
                text: "o.window({ class = \"^org.quickshell$\", title = \"^OMABUDGET$\" },\n  { float = true, center = true, size = { 1180, 720 } })\no.bind(\"SUPER + ALT + B\", \"OMABUDGET\",\n  \"omarchy-shell shell toggle karamble.omabudget '{}'\")"
                color: view.dim
                font.family: view.ff
                font.pixelSize: Style.font.caption
                wrapMode: Text.WrapAnywhere
              }
            }
          }
        }
      }
    }
  }
}
