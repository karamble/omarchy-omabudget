import QtQuick
import qs.Commons
import qs.Ui
import "../components"

// Settings: the budgeting model, the period, alerts, the agent endpoint
// and the token. Every change goes through the CLI like everything else.
Item {
  id: view
  property var app: null
  readonly property bool formFocused: startField.activeFocus || largeField.activeFocus || modelGroup.activeFocus
    || monitoringToggle.activeFocus || mcpToggle.activeFocus

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  property var settings: null
  property var health: null
  property var endpoint: null

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
      if (!startField.activeFocus) startField.text = String(data.periodStartDay)
      if (!largeField.activeFocus) largeField.text = data.largeAmount > 0 ? view.plain(data.largeAmount) : ""
    })
    app.query(["health"], function (data, err) { if (!err) view.health = data })
    app.query(["mcp"], function (data, err) { if (!err) view.endpoint = data })
  }
  onAppChanged: reload()
  Connections {
    target: view.app
    function onChanged() { view.reload() }
  }

  function setModel(m) { app.run(["settings", "model", m], "budgeting model: " + m) }
  function applyStart() {
    var n = Number(startField.text.trim())
    if (!(n >= 1 && n <= 28)) { view.app.lastError = "the period start day is between 1 and 28"; return }
    app.run(["settings", "period-start", String(n)], "periods now begin on day " + n)
  }
  function applyLarge() {
    var v = largeField.text.trim()
    app.run(["settings", "large-amount", v === "" ? "0" : v], v === "" ? "large-amount alerts off" : "large amount set to " + v)
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
            Body { text: "Base currency  " + (view.settings ? view.settings.baseCurrency : "") }
            Note { width: parent.width; text: "Every statistic is kept in it. It is fixed once the ledger has postings, because base amounts are frozen at entry." }
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
