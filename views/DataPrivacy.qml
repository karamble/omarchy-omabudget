import QtQuick
import Quickshell
import qs.Commons
import qs.Ui
import "../components"

// Data and privacy: where the data is, how to take it with you, and how to
// remove it. Exports and backups are written by the daemon, inside home.
Item {
  id: view
  property var app: null
  readonly property bool formFocused: exportPath.activeFocus || backupPath.activeFocus || formatGroup.activeFocus

  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property string ff: app ? app.fontFamily : Style.font.family

  readonly property string home: Quickshell.env("HOME") || "~"
  readonly property string dataDir: home + "/.config/omabudget"
  property string format: "journal"
  // The settings carry the one record kept of the network: the last fetch.
  property var settings: null

  function reload() {
    if (!app) return
    app.query(["settings"], function (data, err) { if (!err) view.settings = data })
  }
  Connections {
    target: view.app
    function onChanged() { view.reload() }
  }
  function networkText() {
    var f = view.settings ? view.settings.lastFetch : null
    if (!f) return "Never used"
    return "Last used " + String(f.at || "").slice(0, 10) + ", " + String(f.host || "") + ", for exchange rates"
  }

  function today() { return Qt.formatDate(new Date(), "yyyy-MM-dd") }
  function defaultExport() { return view.home + "/Documents/omabudget-" + view.today() + (view.format === "csv" ? ".csv" : ".journal") }
  function defaultBackup() { return view.home + "/Documents/omabudget-" + view.today() + ".db" }

  onAppChanged: {
    exportPath.text = view.defaultExport()
    backupPath.text = view.defaultBackup()
    view.reload()
  }
  onFormatChanged: exportPath.text = view.defaultExport()

  function doExport() {
    var p = exportPath.text.trim()
    if (p === "") { exportPath.forceActiveFocus(); return }
    app.run(["export", "-o", p, "-format", view.format], "exported to " + p)
  }
  function doBackup() {
    var p = backupPath.text.trim()
    if (p === "") { backupPath.forceActiveFocus(); return }
    app.run(["backup", "-o", p], "backed up to " + p)
  }

  function handleKey(e) {
    if (e.modifiers !== Qt.NoModifier) return false
    switch (e.key) {
    case Qt.Key_E: exportPath.forceActiveFocus(); exportPath.selectAll(); return true
    case Qt.Key_B: backupPath.forceActiveFocus(); backupPath.selectAll(); return true
    }
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
  component PathField: TextField {
    foreground: view.fg
    accent: view.accent
    font.family: view.ff
    font.pixelSize: Style.font.body
    Keys.onEscapePressed: view.forceActiveFocus()
  }
  component Go: Button {
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
        text: "Data & Privacy"
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }
      Body {
        width: parent.width
        text: "Everything lives in one SQLite file on this machine. No cloud, no tracking, no bank connection. The daemon listens on the loopback address and opens one connection outward only when you press Fetch now in Settings or run omabudget rate fetch: to the exchange rate source chosen there, and never on its own."
        color: view.dim
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
            title: "WHERE IT WRITES"
            Body { text: view.dataDir + "/ledger.db"; font.pixelSize: Style.font.bodySmall }
            Note { width: parent.width; text: "The ledger: accounts, transactions, budgets, rules. Kept at mode 0600." }
            Body { text: view.dataDir + "/config.json"; font.pixelSize: Style.font.bodySmall }
            Note { width: parent.width; text: "Settings and the API token. Never synced anywhere by the app." }
            Body { text: view.dataDir + "/triggers.json"; font.pixelSize: Style.font.bodySmall }
            Note { width: parent.width; text: "Alert triggers, if you armed any." }
            Note { width: parent.width; topPadding: Style.space(6); text: "Keep the folder out of file sync: a database synced while open ends up corrupt. Take a backup or an export instead." }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "NETWORK"
            Body { width: parent.width; text: view.networkText() }
            Note { width: parent.width; text: "One connection, only when you press Fetch now in Settings or run omabudget rate fetch, to the exchange rate source chosen there. The whole list of rates is read, so the request says nothing about what you hold. Nothing else leaves this machine." }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "DELETED TRANSACTIONS"
            Note { width: parent.width; text: "A deleted transaction stays in the recycle bin for thirty days, restorable from the Transactions view with u or b, then it is gone for good." }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "REMOVING EVERYTHING"
            Note { width: parent.width; text: "In a terminal, in this order: the data first, because the second command deletes the program that knows how to do it." }
            Rectangle {
              width: parent.width
              height: purgeText.implicitHeight + Style.space(16)
              radius: Style.cornerRadius
              color: Qt.rgba(view.fg.r, view.fg.g, view.fg.b, 0.05)
              Text {
                id: purgeText
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.margins: Style.space(8)
                text: "omabudget purge\nomarchy plugin remove karamble.omabudget"
                color: view.dim
                font.family: view.ff
                font.pixelSize: Style.font.caption
              }
            }
          }
        }

        Column {
          width: parent.colW
          spacing: Style.space(12)

          TitledCard {

            app: view.app
            width: parent.width
            title: "EXPORT"
            Note { width: parent.width; text: "A plain-text journal that accounting tools read, every entry balanced, or the transactions as CSV." }
            ButtonGroup {
              id: formatGroup
              options: [
                { value: "journal", label: "Journal" },
                { value: "csv", label: "CSV" }
              ]
              value: view.format
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.bodySmall
              focusable: true
              onChanged: function (v) { view.format = v }
            }
            Caption { text: "WRITE TO"; topPadding: Style.space(4) }
            PathField {
              id: exportPath
              width: parent.width
              Keys.onReturnPressed: view.doExport()
              Keys.onEnterPressed: view.doExport()
            }
            Go { text: "Export   e"; onClicked: view.doExport() }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "BACKUP"
            Note { width: parent.width; text: "A consistent copy of the database, taken while the daemon runs. Restore by stopping the daemon and putting the copy in place of ledger.db." }
            Caption { text: "WRITE TO"; topPadding: Style.space(4) }
            PathField {
              id: backupPath
              width: parent.width
              Keys.onReturnPressed: view.doBackup()
              Keys.onEnterPressed: view.doBackup()
            }
            Go { text: "Back up   b"; onClicked: view.doBackup() }
            Note { width: parent.width; text: "Paths stay inside your home directory; the daemon refuses anything else." }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: "WHAT AN AGENT CAN SEE"
            Note { width: parent.width; text: "Only when the agent endpoint is on, in Settings, and only with the token: the dashboard, transactions, the budget, bills and the spending report, one tool to record a transaction, and the alert watches. Nothing reaches an agent on its own, and nothing leaves this machine." }
          }
        }
      }
    }
  }
}
