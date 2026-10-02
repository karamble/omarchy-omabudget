import QtQuick
import Quickshell
import qs.Commons
import qs.Ui
import "../components"
import "../i18n"

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
    if (!f) return I18n.t("settings.neverUsed")
    return I18n.tf("privacy.lastUsedFor", [String(f.at || "").slice(0, 10), String(f.host || "")])
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
    app.run(["export", "-o", p, "-format", view.format], I18n.tf("privacy.toast.exported", [p]))
  }
  function doBackup() {
    var p = backupPath.text.trim()
    if (p === "") { backupPath.forceActiveFocus(); return }
    app.run(["backup", "-o", p], I18n.tf("privacy.toast.backedUp", [p]))
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
        text: I18n.t("privacy.DataPrivacy")
        color: view.fg
        font.family: view.ff
        font.pixelSize: Style.font.heading
      }
      Body {
        width: parent.width
        text: I18n.t("privacy.EverythingLivesInOneSqlite")
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
            title: I18n.t("privacy.WhereItWrites")
            Body { text: view.dataDir + "/ledger.db"; font.pixelSize: Style.font.bodySmall }
            Note { width: parent.width; text: I18n.t("privacy.TheLedgerAccountsTransactionsBudgets") }
            Body { text: view.dataDir + "/config.json"; font.pixelSize: Style.font.bodySmall }
            Note { width: parent.width; text: I18n.t("privacy.SettingsAndTheApiToken") }
            Body { text: view.dataDir + "/triggers.json"; font.pixelSize: Style.font.bodySmall }
            Note { width: parent.width; text: I18n.t("privacy.AlertTriggersIfYouArmed") }
            Note { width: parent.width; topPadding: Style.space(6); text: I18n.t("privacy.KeepTheFolderOutOf") }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("privacy.Network")
            Body { width: parent.width; text: view.networkText() }
            Note { width: parent.width; text: I18n.t("privacy.OneConnectionOnlyWhenYou") }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("privacy.DeletedTransactions")
            Note { width: parent.width; text: I18n.t("privacy.ADeletedTransactionStaysIn") }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("privacy.RemovingEverything")
            Note { width: parent.width; text: I18n.t("privacy.InATerminalInThis") }
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
            title: I18n.t("privacy.Export")
            Note { width: parent.width; text: I18n.t("privacy.APlainTextJournalThat") }
            ButtonGroup {
              id: formatGroup
              options: [
                { value: "journal", label: I18n.t("privacy.Journal") },
                { value: "csv", label: I18n.t("privacy.Csv") }
              ]
              value: view.format
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.bodySmall
              focusable: true
              onChanged: function (v) { view.format = v }
            }
            Caption { text: I18n.t("privacy.WriteTo"); topPadding: Style.space(4) }
            PathField {
              id: exportPath
              width: parent.width
              Keys.onReturnPressed: view.doExport()
              Keys.onEnterPressed: view.doExport()
            }
            Go { text: I18n.tf("privacy.Export2", ["e"]); onClicked: view.doExport() }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("privacy.Backup")
            Note { width: parent.width; text: I18n.t("privacy.AConsistentCopyOfThe") }
            Caption { text: I18n.t("privacy.WriteTo"); topPadding: Style.space(4) }
            PathField {
              id: backupPath
              width: parent.width
              Keys.onReturnPressed: view.doBackup()
              Keys.onEnterPressed: view.doBackup()
            }
            Go { text: I18n.tf("privacy.BackUp", ["b"]); onClicked: view.doBackup() }
            Note { width: parent.width; text: I18n.t("privacy.PathsStayInsideYourHome") }
          }

          TitledCard {

            app: view.app
            width: parent.width
            title: I18n.t("privacy.WhatAnAgentCanSee")
            Note { width: parent.width; text: I18n.t("privacy.OnlyWhenTheAgentEndpoint") }
          }
        }
      }
    }
  }
}
