import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// The glance: one glyph and this period's spending, read from the daemon on a
// timer. Clicking opens the app as its own window.
BarWidget {
  id: root

  moduleName: "karamble.omabudget"

  readonly property var service: bar && bar.shell && typeof bar.shell.serviceFor === "function"
    ? bar.shell.serviceFor("karamble.omabudget") : null

  readonly property string addr: setting("addr", "127.0.0.1:8097")
  readonly property int refreshSec: Math.max(5, Number(setting("refreshSec", 30)))
  readonly property bool blurAmounts: setting("blurAmounts", false) === true

  readonly property color foreground: bar ? bar.barForeground : Color.foreground
  readonly property bool built: !!service && service.built === true
  readonly property bool running: !!service && service.running === true

  readonly property string pluginDir: Qt.resolvedUrl(".").toString()
                                        .replace(/^file:\/\//, "").replace(/\/$/, "")
  readonly property string helperPath: pluginDir + "/bin/omabudget"

  // The last dashboard read. Null until the daemon has answered once.
  property var snap: null

  // What the bar shows: spending this period, in the base currency.
  readonly property string figure: {
    if (!snap || !snap.totals) return ""
    if (root.blurAmounts) return "•••"
    return root.short(snap.totals.expense, snap.baseCurrency)
  }

  // A compact rendering for the bar: 1234.56 -> 1.2k, 64.70 -> 64.70.
  function short(minor, currency) {
    var d = root.decimalsFor(currency)
    var v = minor / Math.pow(10, d)
    if (Math.abs(v) >= 100000) return (v / 1000).toFixed(0) + "k"
    if (Math.abs(v) >= 10000) return (v / 1000).toFixed(1) + "k"
    return v.toFixed(d)
  }

  function decimalsFor(c) {
    switch (String(c || "").toUpperCase()) {
    case "JPY": case "KRW": case "HUF": case "ISK": return 0
    case "BTC": case "DCR": case "LTC": return 8
    }
    return 2
  }

  implicitWidth: row.implicitWidth
  implicitHeight: button.implicitHeight

  function toggleApp() {
    if (bar && bar.shell && typeof bar.shell.toggle === "function")
      bar.shell.toggle("karamble.omabudget", "{}")
  }

  function refresh() {
    if (!root.running || fetchProc.running) return
    fetchProc.running = true
  }

  onRunningChanged: if (running) Qt.callLater(root.refresh)
  Component.onCompleted: Qt.callLater(root.refresh)

  Timer {
    interval: root.refreshSec * 1000
    running: root.running
    repeat: true
    onTriggered: root.refresh()
  }

  readonly property var childEnv: ({
    "PATH": "/usr/bin:/bin",
    "HOME": Quickshell.env("HOME") || ""
  })

  Process {
    id: fetchProc
    command: [root.helperPath, "dashboard", "-json", "-addr", root.addr]
    clearEnvironment: true
    environment: root.childEnv
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var raw = String(text || "").trim()
        if (raw === "") return
        try { root.snap = JSON.parse(raw) } catch (e) { root.snap = null }
      }
    }
  }

  // A read that never returns must not sit on a collector forever.
  Timer {
    interval: 15000
    repeat: false
    running: fetchProc.running
    onTriggered: if (fetchProc.running) fetchProc.running = false
  }

  Row {
    id: row
    anchors.verticalCenter: parent.verticalCenter
    spacing: 0

    BarIconButton {
      id: button
      bar: root.bar
      // The bar always carries the glyph, so a plugin that has never been
      // built still has something to click: the app is where the build is.
      text: ""
      labelVisible: false
      tooltipText: !root.built ? "OMABUDGET: not built yet, open to build"
                 : !root.running ? "OMABUDGET: daemon not running"
                 : root.snap && root.snap.period
                   ? "OMABUDGET: spent " + root.figure + " " + root.snap.baseCurrency
                     + " since " + root.snap.period.from
                   : "OMABUDGET"
      dimmed: !root.running
      onPressed: root.toggleApp()
    }

    Text {
      anchors.verticalCenter: parent.verticalCenter
      visible: root.running && root.figure !== ""
      text: root.figure
      color: root.foreground
      font.family: bar ? bar.fontFamily : Style.font.family
      font.pixelSize: Style.font.bodySmall
      rightPadding: Style.space(6)
      MouseArea {
        anchors.fill: parent
        cursorShape: Qt.PointingHandCursor
        onClicked: root.toggleApp()
      }
    }
  }
}
