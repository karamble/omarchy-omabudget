import QtQuick
import qs.Commons
import qs.Ui
import "../components"
import "../i18n"

// Settings: the budgeting model, the period, exchange rates, alerts, the
// agent endpoint and the token. Every change goes through the CLI like
// everything else, the rate fetch included: it runs only from the button
// here or the command.
Item {
  id: view
  property var app: null
  readonly property bool formFocused: baseField.activeFocus || startField.activeFocus || largeField.activeFocus
    || modelGroup.activeFocus || monitoringToggle.activeFocus || mcpToggle.activeFocus
    || sourceGroup.activeFocus || sourceUrlField.activeFocus || languageGroup.activeFocus

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

  // The daemon authors the rate sources, so their name and description arrive
  // in English whatever the interface language is. Their ids are stable, so a
  // translation is kept here and keyed by id; the English the daemon sent is
  // the fallback, which is what a source added later will show.
  function sourceText(s, field) {
    if (!s) return ""
    var key = "source." + String(s.id || "") + "." + field
    var t = I18n.t(key)
    return t === key ? String(s[field] || "") : t
  }

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

  function setModel(m) { app.run(["settings", "model", m], I18n.tf("settings.toast.model", [m])) }

  // The name is shown rather than the tag, because the tag is only meaningful
  // to the file it names.
  function setLanguage(tag) {
    var name = tag
    for (var i = 0; i < I18n.available.length; i++)
      if (I18n.available[i].tag === tag) name = I18n.available[i].name
    app.run(["settings", "language", tag], I18n.tf("settings.language.done", [name]))
  }
  function applyBase() {
    var code = baseField.text.trim().toUpperCase()
    if (!/^[A-Z]{3}$/.test(code)) { view.app.lastError = I18n.t("settings.err.currency"); return }
    app.run(["settings", "base-currency", code], I18n.tf("settings.toast.base", [code]))
  }
  function applyStart() {
    var n = Number(startField.text.trim())
    if (!(n >= 1 && n <= 28)) { view.app.lastError = I18n.t("settings.err.startDay"); return }
    app.run(["settings", "period-start", String(n)], I18n.tf("settings.toast.start", [n]))
  }
  function applyLarge() {
    var v = largeField.text.trim()
    app.run(["settings", "large-amount", v === "" ? "0" : v], v === "" ? I18n.t("settings.toast.largeOff") : I18n.tf("settings.toast.large", [v]))
  }
  function setSource(id) {
    var name = id
    for (var i = 0; i < view.sources.length; i++) if (view.sources[i].id === id) name = view.sources[i].name
    app.run(["settings", "rate-source", id], I18n.tf("settings.toast.source", [view.sourceText({id: id, name: name}, "name")]))
  }
  function applySourceUrl() {
    if (!view.settings) return
    var url = sourceUrlField.text.trim()
    app.run(["settings", "rate-source", String(view.settings.rateSource), "-url", url],
            url === "" ? I18n.t("settings.toast.sourcePublic") : I18n.tf("settings.toast.sourceUrl", [url]))
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
            I18n.tf("settings.toast.filedOne", [h.currency, h.rate, view.reference]))
    var rest = []
    var held = view.fetched.held || []
    for (var i = 0; i < held.length; i++) if (held[i].currency !== h.currency) rest.push(held[i])
    var next = Object.assign({}, view.fetched)
    next.held = rest
    view.fetched = next
  }
  function lastFetchText() {
    var f = view.settings ? view.settings.lastFetch : null
    if (!f) return I18n.t("settings.neverUsed")
    return I18n.tf("settings.lastUsed", [String(f.at || "").slice(0, 10), String(f.host || "")])
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
        text: I18n.t("settings.Settings")
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
            title: I18n.t("settings.Budgeting")
            Caption { text: I18n.t("settings.ShowFiguresIn") }
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
              Apply { text: I18n.t("settings.Apply"); onClicked: view.applyBase() }
            }
            Note {
              width: parent.width
              text: I18n.t("settings.EveryFigureIsConvertedInto")
                + (view.settings ? view.settings.rateReference : I18n.t("settings.theReference"))
                + I18n.t("settings.whichTheLedger")
            }
            Caption { text: I18n.t("settings.Model"); topPadding: Style.space(6) }
            ButtonGroup {
              id: modelGroup
              options: [
                { value: "limits", label: I18n.t("settings.CategoryLimits") },
                { value: "envelope", label: I18n.t("settings.Envelopes") }
              ]
              value: view.settings ? view.settings.model : "limits"
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.bodySmall
              focusable: true
              onChanged: function (v) { view.setModel(v) }
            }
            Note { width: parent.width; text: I18n.t("settings.LimitsAPlannedAmountPer") }
            Caption { text: I18n.t("settings.PeriodBeginsOnDay"); topPadding: Style.space(6) }
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
              Apply { text: I18n.t("settings.Apply"); onClicked: view.applyStart() }
            }
            Note { width: parent.width; text: I18n.t("settings.1To28PickYour") }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("settings.ExchangeRates")
            Caption { text: I18n.t("settings.Source") }
            ButtonGroup {
              id: sourceGroup
              options: view.sources.map(function (s) { return { value: s.id, label: view.sourceText(s, "name") } })
              value: view.settings ? String(view.settings.rateSource || "") : ""
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.bodySmall
              focusable: true
              onChanged: function (v) { view.setSource(v) }
            }
            Note { width: parent.width; text: view.chosenSource ? view.sourceText(view.chosenSource, "what") : I18n.t("settings.ChoosingASourceIsChoosing") }
            Caption { text: I18n.t("settings.ReadsFrom"); topPadding: Style.space(6); visible: view.chosenSource !== null && view.chosenSource.custom === true }
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
              Apply { text: I18n.t("settings.Apply"); onClicked: view.applySourceUrl() }
            }
            Note {
              width: parent.width
              visible: view.chosenSource !== null && view.chosenSource.custom === true
              text: I18n.t("settings.EmptyReadsThePublicInstance")
            }
            Row {
              spacing: Style.space(8)
              topPadding: Style.space(6)
              Apply {
                text: view.fetching ? I18n.t("settings.fetching") : I18n.t("settings.fetchNow")
                enabled: !view.fetching && view.settings !== null
                tooltipText: I18n.t("settings.OneRequestToTheSource")
                onClicked: view.fetchRates()
              }
              Note { anchors.verticalCenter: parent.verticalCenter; text: I18n.t("settings.Network") + view.lastFetchText() }
            }
            Note {
              width: parent.width
              text: I18n.t("settings.NothingIsFetchedUnlessYou")
            }
            Column {
              width: parent.width
              spacing: Style.space(6)
              visible: view.fetched !== null
              Caption { text: I18n.t("settings.LastPress"); topPadding: Style.space(6) }
              Body {
                width: parent.width
                text: view.fetched ? I18n.tf("settings.readAt", [view.fetched.name, view.fetched.host, view.fetched.published]) : ""
              }
              Body {
                width: parent.width
                visible: view.fetched && (view.fetched.filed || []).length > 0
                text: view.fetched ? I18n.tf("settings.filed", [(view.fetched.filed || []).join(", ")]) : ""
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.unchanged || []).length > 0
                text: view.fetched ? I18n.tf("settings.alreadyOnFile", [(view.fetched.unchanged || []).join(", ")]) : ""
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.kept || []).length > 0
                text: view.fetched ? I18n.tf("settings.keptByHand", [(view.fetched.kept || []).join(", ")]) : ""
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.filed || []).length + (view.fetched.unchanged || []).length + (view.fetched.kept || []).length + (view.fetched.held || []).length === 0
                text: I18n.t("settings.NothingToFileNoCurrency")
              }
              Repeater {
                model: view.fetched ? (view.fetched.held || []) : []
                delegate: Column {
                  required property var modelData
                  width: parent ? parent.width : 0
                  spacing: Style.space(2)
                  Body {
                    width: parent.width
                    text: I18n.t("settings.Held1") + modelData.currency + " = " + modelData.rate + " " + view.reference
                      + (modelData.previous ? "  (was " + modelData.previous + " on " + modelData.previousDate + ")" : "")
                  }
                  Note { width: parent.width; text: modelData.reason }
                  Apply { text: I18n.t("settings.Apply2") + modelData.currency; onClicked: view.acceptHeld(modelData) }
                }
              }
              Note {
                width: parent.width
                visible: view.fetched && (view.fetched.unquoted || []).length > 0
                text: view.fetched ? I18n.tf("settings.notQuotedBy", [view.fetched.name, (view.fetched.unquoted || []).join(", ")]) : ""
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
                text: view.fetched ? I18n.tf("settings.rederived", [view.fetched.rederived]) : ""
              }
            }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("settings.Alerts")
            Toggle {
              id: monitoringToggle
              width: parent.width
              label: I18n.t("settings.EvaluateAlertTriggers")
              description: I18n.t("settings.BudgetLinesAtWarnOr")
              checked: view.settings ? view.settings.monitoring === true : true
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              onClicked: view.app.run(["monitoring", checked ? "off" : "on"], "monitoring " + (checked ? "off" : "on"))
            }
            Caption { text: I18n.t("settings.LargeAmount"); topPadding: Style.space(6) }
            Row {
              spacing: Style.space(8)
              TextField {
                id: largeField
                width: Style.space(140)
                foreground: view.fg
                accent: view.accent
                font.family: view.ff
                font.pixelSize: Style.font.body
                placeholderText: I18n.t("settings.Off")
                Keys.onReturnPressed: view.applyLarge()
                Keys.onEnterPressed: view.applyLarge()
                Keys.onEscapePressed: view.forceActiveFocus()
              }
              Apply { text: I18n.t("settings.Apply"); onClicked: view.applyLarge() }
            }
            Note { width: parent.width; text: I18n.t("settings.PostingsAtOrAboveThis") }
          }
        }

        Column {
          width: parent.colW
          spacing: Style.space(12)

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("settings.AgentEndpoint")
            Toggle {
              id: mcpToggle
              width: parent.width
              label: I18n.t("settings.AnswerAgentsOverMcp")
              description: I18n.t("settings.ToolsOn12700")
              checked: view.health ? view.health.mcpEnabled === true : false
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              onClicked: view.app.run(["mcp-endpoint", checked ? "off" : "on"], checked ? I18n.t("settings.toast.mcpOff") : I18n.t("settings.toast.mcpOn"))
            }
            Caption { text: I18n.t("settings.ForYourAgentSConfig"); topPadding: Style.space(6) }
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
                text: I18n.t("settings.RecycleTheToken")
                tooltipText: I18n.t("settings.EveryClientHoldingTheOld")
                onClicked: view.app.run(["recycle"], I18n.t("settings.toast.recycled"))
              }
              Note { anchors.verticalCenter: parent.verticalCenter; text: I18n.t("settings.HHidesTheTokenWith") }
            }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("settings.TheDaemon")
            Body { text: (view.app && view.app.running ? I18n.t("settings.running") : I18n.t("settings.notRunning")) + "  ·  " + (view.app ? view.app.addr : "") }
            Body { text: I18n.t("settings.Version") + (view.app && view.app.manifest && view.app.manifest.version ? view.app.manifest.version : "dev") }
            Note { width: parent.width; text: I18n.t("settings.ListensOnTheLoopbackAddress") }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("settings.card.interface")
            Caption { text: I18n.t("settings.language") }
            ButtonGroup {
              id: languageGroup
              options: I18n.available.map(function (l) { return { value: l.tag, label: l.name } })
              value: I18n.tag
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.bodySmall
              focusable: true
              onChanged: function (v) { view.setLanguage(v) }
            }
            Note { width: parent.width; text: I18n.t("settings.language.note") }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("settings.TheWindow")
            Note { width: parent.width; text: I18n.t("settings.HyprlandTilesThisWindowUnless") }
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
