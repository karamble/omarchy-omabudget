import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

// The application. The shell loads this Item when the panel is opened and
// calls open() and close() on it; the window itself is ours.
Item {
  id: root

  // Injected by the shell.
  property var shell: null
  property var manifest: null
  property var service: null
  property bool opened: false
  property bool closingFromHost: false

  readonly property string pluginId: manifest && manifest.id ? String(manifest.id) : "karamble.omabudget"

  // ---- theme tokens, derived so the app follows whatever theme is running
  readonly property color foreground: Color.foreground
  readonly property color background: Color.background
  readonly property color accent: Color.accent
  readonly property color urgent: Color.urgent
  readonly property color cardBorder: Style.normalBorderColor
  readonly property color dim: Qt.rgba(
    foreground.r * 0.68 + background.r * 0.32,
    foreground.g * 0.68 + background.g * 0.32,
    foreground.b * 0.68 + background.b * 0.32, 1)
  readonly property color dimmer: Qt.rgba(
    foreground.r * 0.45 + background.r * 0.55,
    foreground.g * 0.45 + background.g * 0.55,
    foreground.b * 0.45 + background.b * 0.55, 1)
  // Semantic colours, defined once and derived from the theme's own tokens,
  // so they change with the theme like everything else.
  readonly property color income: accent
  readonly property color expense: urgent
  readonly property color net: foreground
  readonly property color onBudget: accent
  readonly property color nearLimit: Qt.rgba(
    (accent.r + urgent.r) / 2, (accent.g + urgent.g) / 2, (accent.b + urgent.b) / 2, 1)
  readonly property color overLimit: urgent
  readonly property string fontFamily: Style.font.family

  // The kit hardcodes its durations per component rather than exposing
  // tokens, so the house language is named once here and referenced from
  // everywhere: a colour settles quickly, geometry and opacity take the
  // longer curve, and the cursor has to feel attached to the key.
  readonly property int quickMs: 60
  readonly property int colourMs: 120
  readonly property int moveMs: 140

  // Hide every figure, for screen sharing. Toggled with h.
  property bool blurAmounts: false

  // ---- the daemon
  readonly property string pluginDir: Qt.resolvedUrl(".").toString()
                                        .replace(/^file:\/\//, "").replace(/\/$/, "")
  readonly property string helperPath: pluginDir + "/bin/omabudget"
  readonly property string addr: "127.0.0.1:8097"
  readonly property bool built: !!service && service.built === true
  readonly property bool running: !!service && service.running === true
  // The helper is older than the source it was built from. A plugin update
  // leaves bin/ in place, so without this the daemon quietly keeps running
  // the previous version behind the new QML.
  property bool stale: false
  // Whether there is anything to build. A directory deployed by make install
  // carries the QML and no source, and the build button would fail with a
  // message from make rather than from us.
  property bool buildable: true

  readonly property var childEnv: ({
    "PATH": "/usr/bin:/bin",
    "HOME": Quickshell.env("HOME") || ""
  })

  // The dashboard document, re-read after every change and on a timer.
  property var snap: null
  property string lastError: ""
  property string toast: ""

  // Reference lists for forms and name lookups, read with the dashboard.
  property var accounts: []
  property var categories: []

  // Fired after a change went through, so a view re-reads what it shows.
  signal changed()

  function refresh() {
    if (!buildProbe.running) buildProbe.running = true
    if (root.built && !staleProbe.running) staleProbe.running = true
    if (!root.running) return
    if (!fetchProc.running) fetchProc.running = true
    if (!accountsProc.running) accountsProc.running = true
    if (!categoriesProc.running) categoriesProc.running = true
  }

  function accountName(id) {
    for (var i = 0; i < root.accounts.length; i++)
      if (root.accounts[i].id === id) return root.accounts[i].name
    return id || ""
  }
  function category(id) {
    for (var i = 0; i < root.categories.length; i++)
      if (root.categories[i].id === id) return root.categories[i]
    return null
  }
  function categoryName(id) { var c = root.category(id); return c ? c.name : (id || "") }
  function accountOf(id) {
    for (var i = 0; i < root.accounts.length; i++)
      if (root.accounts[i].id === id) return root.accounts[i]
    return null
  }

  // The daemon's own date, so a form dates an entry the way the ledger will
  // rather than the way this machine's clock happens to read.
  function today() {
    return (root.snap && root.snap.today) ? String(root.snap.today)
                                          : Qt.formatDate(new Date(), "yyyy-MM-dd")
  }

  // The currency every rate is quoted against and every figure is kept in.
  // Figures are shown in snap.baseCurrency, which may differ from it.
  readonly property string rateReference: root.snap && root.snap.rateReference ? String(root.snap.rateReference)
    : (root.snap && root.snap.baseCurrency ? String(root.snap.baseCurrency) : "")

  // The rate a new entry would read, spec 8: the newest on file for the
  // currency, and nothing at all when there is none, which is when the ledger
  // refuses the entry. An entry dated before that rate reads the table at its
  // own date, so the row comes back flagged ahead rather than withheld. A rate
  // is against the reference, not the currency shown.
  function rateFor(currency, date) {
    if (!currency || root.rateReference === "") return null
    if (currency === root.rateReference) return { rate: "1", date: date, ahead: false }
    var list = root.snap.rates ? root.snap.rates : []
    var on = date && date !== "" ? date : root.today()
    for (var i = 0; i < list.length; i++)
      if (list[i].currency === currency)
        return { rate: list[i].rate, date: list[i].date, ahead: String(list[i].date) > on }
    return null
  }

  // The arithmetic the ledger accepts in an amount, so a preview reads the
  // same number the ledger will. Double precision here against exact rational
  // arithmetic there, so a preview can differ by a minor unit on an absurd
  // amount; it is a preview, not the record.
  function evaluate(text) {
    var str = String(text || "").replace(/[ _]/g, "")
    if (str === "") return NaN
    if (!/^[-+]?[0-9.,]+([-+][0-9.,]+)*$/.test(str)) return NaN
    var total = 0, sign = 1, part = ""
    for (var i = 0; i < str.length; i++) {
      var c = str[i]
      if ((c === "+" || c === "-") && part !== "") {
        var v = root.term(part)
        if (!isFinite(v)) return NaN
        total += sign * v
        part = ""
        sign = c === "-" ? -1 : 1
      } else if ((c === "+" || c === "-") && part === "") {
        if (c === "-") sign = -sign
      } else {
        part += c
      }
    }
    if (part === "") return NaN
    var last = root.term(part)
    if (!isFinite(last)) return NaN
    return total + sign * last
  }

  // 1,250.00 carries a thousands comma; 4,50 carries a decimal one.
  function term(t) {
    if (t.indexOf(".") >= 0) t = t.replace(/,/g, "")
    else if ((t.match(/,/g) || []).length === 1) t = t.replace(",", ".")
    else t = t.replace(/,/g, "")
    var n = Number(t)
    return (t === "" || t === "." || !isFinite(n)) ? NaN : n
  }

  // A picker puts the categories actually being used at the top, so the
  // ten-second path usually needs no typing. The Manage tree keeps the
  // taxonomy's own order, which is what a taxonomy is for.
  function byRecentUse(list) {
    return (list || []).slice().sort(function (a, b) {
      var d = (b.recent || 0) - (a.recent || 0)
      if (d !== 0) return d
      var pa = String(a.parentName || ""), pb = String(b.parentName || "")
      if (pa !== pb) return pa < pb ? -1 : 1
      return String(a.name) < String(b.name) ? -1 : 1
    })
  }
  function categoryIcon(id) {
    var c = root.category(id)
    if (!c) return ""
    if (c.icon) return c.icon
    var p = c.parentId ? root.category(c.parentId) : null
    return p && p.icon ? p.icon : ""
  }

  Process {
    id: accountsProc
    command: [root.helperPath, "accounts", "-json", "-addr", root.addr]
    clearEnvironment: true
    environment: root.childEnv
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try { var v = JSON.parse(String(text || "")); root.accounts = Array.isArray(v) ? v : [] } catch (e) {}
      }
    }
  }

  Process {
    id: categoriesProc
    command: [root.helperPath, "categories", "-json", "-addr", root.addr]
    clearEnvironment: true
    environment: root.childEnv
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        try { var v = JSON.parse(String(text || "")); root.categories = Array.isArray(v) ? v : [] } catch (e) {}
      }
    }
  }

  // Reads on behalf of a view: one at a time, the answer parsed and handed
  // to the callback as (data, errorText).
  property var queryQueue: []
  property var queryCallback: null

  function query(argv, cb) {
    if (!root.running) { if (cb) cb(null, "the daemon is not running"); return }
    root.queryQueue.push({ argv: argv, cb: cb })
    root.pumpQueries()
  }

  function pumpQueries() {
    if (queryProc.running || root.queryQueue.length === 0) return
    var job = root.queryQueue.shift()
    root.queryCallback = job.cb
    queryProc.out = ""
    queryProc.errText = ""
    queryProc.command = [root.helperPath].concat(job.argv).concat(["-json", "-addr", root.addr])
    queryProc.running = true
  }

  Process {
    id: queryProc
    clearEnvironment: true
    environment: root.childEnv
    property string out: ""
    property string errText: ""
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: queryProc.out = String(text || "")
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: queryProc.errText = String(text || "").trim()
    }
    onExited: function (code, status) {
      var cb = root.queryCallback
      root.queryCallback = null
      var data = null
      var err = ""
      if (code === 0) {
        try { data = JSON.parse(queryProc.out) } catch (e) { err = "unreadable answer from the helper" }
      } else {
        err = queryProc.errText.replace(/^omabudget: /, "").split("\n")[0] || ("the helper exited with " + code)
      }
      if (cb) cb(data, err)
      Qt.callLater(root.pumpQueries)
    }
  }

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
        try { root.snap = JSON.parse(raw); root.lastError = "" }
        catch (e) { root.lastError = "could not read the dashboard" }
      }
    }
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var msg = String(text || "").trim()
        if (msg !== "") root.lastError = msg.split("\n")[0]
      }
    }
  }

  // Every mutation goes through the CLI as an argv array: no shell, no string
  // building, the daemon's own validation, and stderr is the message shown.
  Process {
    id: mutateProc
    clearEnvironment: true
    environment: root.childEnv
    property string pending: ""
    stderr: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        var msg = String(text || "").trim()
        if (msg !== "") root.lastError = msg.replace(/^omabudget: /, "").split("\n")[0]
      }
    }
    onExited: function (code, status) {
      if (code === 0) {
        root.lastError = ""
        root.showToast(mutateProc.pending)
        root.refresh()
        root.changed()
      }
      mutateProc.pending = ""
      Qt.callLater(root.pumpMutations)
    }
  }

  // Changes queue rather than collide: a second submit while the first is
  // still running used to be dropped without a word.
  property var mutateQueue: []

  function run(argv, doneText) {
    root.mutateQueue.push({ argv: argv, done: doneText || "" })
    root.pumpMutations()
  }

  function pumpMutations() {
    if (mutateProc.running || root.mutateQueue.length === 0) return
    var job = root.mutateQueue.shift()
    mutateProc.pending = job.done
    mutateProc.command = [root.helperPath].concat(job.argv).concat(["-addr", root.addr])
    mutateProc.running = true
  }

  function showToast(text) {
    root.toast = text
    toastTimer.restart()
  }
  Timer { id: toastTimer; interval: 4000; onTriggered: root.toast = "" }

  // Nothing here may run for ever: a read or a write that hangs is stopped.
  Timer {
    interval: 15000
    repeat: false
    running: fetchProc.running || mutateProc.running || queryProc.running || accountsProc.running || categoriesProc.running
    onTriggered: {
      if (fetchProc.running) fetchProc.running = false
      if (accountsProc.running) accountsProc.running = false
      if (categoriesProc.running) categoriesProc.running = false
      if (queryProc.running) queryProc.running = false
      if (mutateProc.running) {
        mutateProc.running = false
        root.mutateQueue = []
        root.lastError = "the helper did not answer"
      }
    }
  }

  onRunningChanged: if (running) Qt.callLater(root.refresh)
  onOpenedChanged: if (opened) Qt.callLater(root.refresh)
  Timer {
    interval: 30000
    running: root.opened && root.running
    repeat: true
    onTriggered: root.refresh()
  }

  // ---- money rendering
  // Minor-unit digits per currency, as the daemon serves them with the
  // dashboard for every currency in play; two until it has.
  function decimalsFor(c) {
    var table = root.snap && root.snap.decimals ? root.snap.decimals : null
    var d = table ? table[String(c || "").toUpperCase()] : undefined
    return d !== undefined && d !== null ? Number(d) : 2
  }

  // fmt renders minor units with thousands separators, or a placeholder when
  // amounts are hidden.
  function fmt(minor, currency) {
    if (root.blurAmounts) return "•••••"
    var d = root.decimalsFor(currency)
    var neg = minor < 0
    var s = String(Math.abs(Number(minor) || 0))
    while (s.length <= d) s = "0" + s
    var whole = d > 0 ? s.slice(0, s.length - d) : s
    var frac = d > 0 ? s.slice(s.length - d) : ""
    whole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",")
    return (neg ? "-" : "") + whole + (d > 0 ? "." + frac : "")
  }

  // ---- navigation
  readonly property var views: [
    { id: "dashboard", label: "Dashboard", key: "1", glyph: "󰕮" },
    { id: "accounts", label: "Accounts", key: "2", glyph: "󰆦" },
    { id: "transactions", label: "Transactions", key: "3", glyph: "󰈙" },
    { id: "budget", label: "Budget", key: "4", glyph: "󰄬" },
    { id: "bills", label: "Bills", key: "5", glyph: "󰃭" },
    { id: "reports", label: "Reports", key: "6", glyph: "󰕮" },
    { id: "manage", label: "Manage", key: "7", glyph: "󰅌" },
    { id: "settings", label: "Settings", key: "8", glyph: "󰒓" },
    { id: "privacy", label: "Data & Privacy", key: "9", glyph: "󰌾" }
  ]
  property string view: "dashboard"
  property int navCursor: 0

  function setView(id) {
    for (var i = 0; i < views.length; i++) {
      if (views[i].id === id) { root.view = id; root.navCursor = i; return }
    }
  }

  // A report hands the Transactions view a filter to open with.
  property var pendingFilter: null
  function openTransactions(filter) {
    root.pendingFilter = filter
    root.setView("transactions")
  }

  // ---- quick-add, reachable from every screen with n
  property bool quickAddOpen: false

  function openQuickAdd() {
    if (!root.running) return
    root.quickAddOpen = true
  }
  function closeQuickAdd() {
    root.quickAddOpen = false
    Qt.callLater(function () { focusScope.forceActiveFocus() })
  }

  // ---- shell contract
  function open(payloadJson) {
    var payload = ({})
    try { payload = JSON.parse(String(payloadJson || "{}")) || ({}) } catch (e) {}
    closingFromHost = false
    opened = true
    if (payload.view) root.setView(String(payload.view))
    if (payload.quickAdd) root.openQuickAdd()
    Qt.callLater(function () { focusScope.forceActiveFocus() })
  }

  function close() {
    closingFromHost = true
    opened = false
    closingFromHost = false
  }

  function requestClose() {
    if (shell && typeof shell.hide === "function") shell.hide(pluginId)
    else close()
  }

  FloatingWindow {
    id: window
    visible: root.opened
    title: "OMABUDGET"
    color: root.background
    implicitWidth: Style.space(1180)
    implicitHeight: Style.space(720)
    minimumSize: Qt.size(Style.space(880), Style.space(560))

    onVisibleChanged: {
      if (!visible && root.opened && !root.closingFromHost) root.requestClose()
    }

    FocusScope {
      id: focusScope
      anchors.fill: parent
      focus: true

      // A form inside a view, or the quick-add card, owns the keys while a
      // field has focus; otherwise bare keys are commands.
      readonly property bool formFocused:
        (quickAdd.item && quickAdd.item.formFocused === true)
        || (content.item && content.item.formFocused === true)

      Keys.onPressed: function (e) {
        if (focusScope.formFocused) return
        // The open view gets first refusal: row cursors, forms, its own verbs.
        if (content.item && typeof content.item.handleKey === "function" && content.item.handleKey(e)) {
          e.accepted = true; return
        }
        if (e.key === Qt.Key_Escape) {
          if (root.quickAddOpen) root.closeQuickAdd()
          else root.requestClose()
          e.accepted = true; return
        }
        if (e.modifiers !== Qt.NoModifier) return
        if (e.key === Qt.Key_N) { root.openQuickAdd(); e.accepted = true; return }
        if (e.key === Qt.Key_H) { root.blurAmounts = !root.blurAmounts; e.accepted = true; return }
        if (e.key === Qt.Key_R) { root.refresh(); e.accepted = true; return }
        if (e.key === Qt.Key_J || e.key === Qt.Key_Down) {
          root.navCursor = (root.navCursor + 1) % root.views.length
          root.view = root.views[root.navCursor].id
          e.accepted = true; return
        }
        if (e.key === Qt.Key_K || e.key === Qt.Key_Up) {
          root.navCursor = (root.navCursor - 1 + root.views.length) % root.views.length
          root.view = root.views[root.navCursor].id
          e.accepted = true; return
        }
        for (var i = 0; i < root.views.length; i++) {
          if (e.text === root.views[i].key) { root.setView(root.views[i].id); e.accepted = true; return }
        }
      }

      RowLayout {
        anchors.fill: parent
        spacing: 0

        // ---- nav rail
        Rectangle {
          Layout.fillHeight: true
          Layout.preferredWidth: Style.space(220)
          color: root.background

          Rectangle {
            anchors.right: parent.right
            width: Style.spacing.hairline
            height: parent.height
            color: root.cardBorder
          }

          Column {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            anchors.margins: Style.space(16)
            spacing: Style.space(4)

            Text {
              text: "OMABUDGET"
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.title
              font.bold: true
            }
            Text {
              text: "v" + (root.manifest && root.manifest.version ? root.manifest.version : "dev")
              color: root.dimmer
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              bottomPadding: Style.space(16)
            }

            Repeater {
              model: root.views
              delegate: Rectangle {
                required property var modelData
                required property int index
                width: parent.width
                height: Style.space(36)
                radius: Style.cornerRadius
                readonly property bool current: root.view === modelData.id
                color: current ? Style.selectedAccentFill : "transparent"
                // The width is reserved rather than switched, so becoming the
                // current row tints a border into view instead of growing the
                // row by a pixel a side and nudging its neighbours.
                border.width: Style.selectedBorderWidth
                border.color: current ? Style.selectedBorderColor : "transparent"
                Behavior on color { ColorAnimation { duration: root.colourMs } }
                Behavior on border.color { ColorAnimation { duration: root.colourMs } }

                Row {
                  anchors.fill: parent
                  anchors.leftMargin: Style.space(12)
                  spacing: Style.space(10)
                  Text {
                    anchors.verticalCenter: parent.verticalCenter
                    text: modelData.glyph
                    color: current ? root.accent : root.dim
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.icon
                    Behavior on color { ColorAnimation { duration: root.colourMs } }
                  }
                  Text {
                    anchors.verticalCenter: parent.verticalCenter
                    text: modelData.label
                    color: current ? root.foreground : root.dim
                    font.family: root.fontFamily
                    font.pixelSize: Style.font.body
                    Behavior on color { ColorAnimation { duration: root.colourMs } }
                  }
                }
                Text {
                  anchors.right: parent.right
                  anchors.rightMargin: Style.space(10)
                  anchors.verticalCenter: parent.verticalCenter
                  text: modelData.key
                  color: root.dimmer
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.caption
                }
                MouseArea {
                  anchors.fill: parent
                  cursorShape: Qt.PointingHandCursor
                  onClicked: root.setView(modelData.id)
                }
              }
            }
          }

          Column {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            anchors.margins: Style.space(16)
            spacing: Style.space(4)

            Button {
              width: parent.width
              text: "Quick add   n"
              tooltipText: "Log an expense in a few keystrokes"
              foreground: root.foreground
              accent: root.accent
              fontFamily: root.fontFamily
              fontSize: Style.font.caption
              bordered: true
              focusable: false
              onClicked: root.openQuickAdd()
            }
            Text {
              topPadding: Style.space(8)
              text: "Local. Private. Yours."
              color: root.dimmer
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
            Text {
              text: root.blurAmounts ? "amounts hidden  h" : "hide amounts  h"
              color: root.dimmer
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
          }
        }

        // ---- content
        Item {
          Layout.fillWidth: true
          Layout.fillHeight: true

          // Until the daemon is built and running, every screen shows the
          // same thing: how to get there.
          Column {
            anchors.centerIn: parent
            visible: !root.running
            spacing: Style.space(10)
            width: Math.min(parent.width - Style.space(48), Style.space(520))

            Text {
              text: root.built ? "The daemon is not running" : "Not built yet"
              color: root.foreground
              font.family: root.fontFamily
              font.pixelSize: Style.font.title
            }
            Text {
              width: parent.width
              wrapMode: Text.WordWrap
              text: root.built
                ? (root.service && root.service.lastError ? root.service.lastError : "It should start on its own within a few seconds.")
                : root.buildable
                  ? "OMABUDGET ships source only. Build it once and the daemon starts on its own."
                  : "There is no Makefile here, so this copy has the screens but not the source to build. Reinstall it with: omarchy plugin add https://github.com/karamble/omarchy-omabudget"
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.body
            }
            Button {
              visible: !root.built && root.buildable
              text: "Build now"
              tooltipText: "Runs make in the plugin directory, in a terminal"
              foreground: root.foreground
              accent: root.accent
              fontFamily: root.fontFamily
              bordered: true
              focusable: true
              onClicked: root.runBuild()
            }
            // The same thing by hand, for anyone whose terminal does not open
            // or who would rather watch the build.
            Text {
              width: parent.width
              visible: !root.built && root.buildable
              wrapMode: Text.WrapAnywhere
              text: "  cd " + root.pluginDir + "\n  make"
              color: root.dimmer
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
          }

          // A plugin update leaves bin/ alone, so the new screens can end up
          // in front of the old daemon. Say so rather than let the two drift.
          Rectangle {
            id: staleBanner
            anchors.top: parent.top
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.margins: Style.space(20)
            readonly property bool showing: root.stale && root.running
            visible: height > 0
            opacity: showing ? 1 : 0
            height: showing ? staleRow.implicitHeight + Style.space(16) : 0
            Behavior on height { NumberAnimation { duration: root.moveMs; easing.type: Easing.OutCubic } }
            Behavior on opacity { NumberAnimation { duration: root.moveMs; easing.type: Easing.OutCubic } }
            radius: Style.cornerRadius
            color: "transparent"
            border.width: Style.normalBorderWidth
            border.color: root.accent
            z: 5
            Row {
              id: staleRow
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              anchors.margins: Style.space(12)
              spacing: Style.space(10)
              Text {
                width: parent.width - Style.space(110) - parent.spacing
                anchors.verticalCenter: parent.verticalCenter
                wrapMode: Text.WordWrap
                text: "The helper is older than the source. Rebuild it, or the daemon keeps running the previous version."
                color: root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              Button {
                width: Style.space(110)
                anchors.verticalCenter: parent.verticalCenter
                text: "Rebuild"
                tooltipText: "Runs make in the plugin directory, then restarts the shell"
                foreground: root.foreground
                accent: root.accent
                fontFamily: root.fontFamily
                fontSize: Style.font.bodySmall
                bordered: true
                onClicked: root.runRebuild()
              }
            }
          }

          Loader {
            id: content
            anchors.fill: parent
            anchors.margins: Style.space(20)
            anchors.topMargin: Style.space(20) + (staleBanner.visible ? staleBanner.height + Style.space(12) : 0)
            visible: root.running
            source: root.running ? "views/" + root.viewFile(root.view) : ""
            onLoaded: {
              if (item && "app" in item) item.app = root
              // Held at nothing while the view is built, then faded up, so
              // walking the rail reads as movement rather than nine cuts.
              content.opacity = 0
              content.opacity = 1
            }
            Behavior on opacity { NumberAnimation { duration: root.moveMs; easing.type: Easing.OutCubic } }
            Behavior on anchors.topMargin { NumberAnimation { duration: root.moveMs; easing.type: Easing.OutCubic } }
          }

          // Quick-add floats over whatever view is open.
          Loader {
            id: quickAdd
            anchors.top: parent.top
            anchors.right: parent.right
            anchors.margins: Style.space(20)
            width: Math.min(parent.width - Style.space(40), Style.space(440))
            active: root.quickAddOpen
            source: "components/QuickAdd.qml"
            z: 10
            onLoaded: {
              item.app = root
              item.submitted.connect(function (argv, doneText) {
                root.run(argv, doneText)
                root.closeQuickAdd()
              })
              item.cancelled.connect(root.closeQuickAdd)
              Qt.callLater(function () { if (quickAdd.item) quickAdd.item.focusFirst() })
            }
          }

          // The message line: an error from the last command, or a toast.
          Rectangle {
            anchors.bottom: parent.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            readonly property bool showing: root.lastError !== "" || root.toast !== ""
            height: showing ? Style.space(32) : 0
            visible: height > 0
            opacity: showing ? 1 : 0
            color: root.background
            Behavior on height { NumberAnimation { duration: root.moveMs; easing.type: Easing.OutCubic } }
            Behavior on opacity { NumberAnimation { duration: root.moveMs; easing.type: Easing.OutCubic } }
            Rectangle { anchors.top: parent.top; width: parent.width; height: Style.spacing.hairline; color: root.cardBorder }
            Row {
              anchors.left: parent.left
              anchors.leftMargin: Style.space(20)
              anchors.right: parent.right
              anchors.rightMargin: Style.space(20)
              anchors.verticalCenter: parent.verticalCenter
              spacing: Style.space(8)
              readonly property bool failed: root.lastError !== ""
              // A glyph carries the outcome at a glance, so the strip can be
              // read without reading it.
              Text {
                anchors.verticalCenter: parent.verticalCenter
                text: parent.failed ? "󰀦" : "󰄬"
                color: parent.failed ? root.urgent : root.accent
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
              }
              Text {
                anchors.verticalCenter: parent.verticalCenter
                width: parent.width - Style.space(28)
                // Saying something worked was rendered dimmer than body text,
                // which made the confirmation the faintest thing on screen.
                text: parent.failed ? root.lastError : root.toast
                color: parent.failed ? root.urgent : root.foreground
                font.family: root.fontFamily
                font.pixelSize: Style.font.bodySmall
                elide: Text.ElideRight
              }
            }
          }
        }
      }
    }
  }

  function viewFile(id) {
    switch (id) {
    case "dashboard": return "Dashboard.qml"
    case "accounts": return "Accounts.qml"
    case "transactions": return "Transactions.qml"
    case "budget": return "Budget.qml"
    case "bills": return "Bills.qml"
    case "reports": return "Reports.qml"
    case "manage": return "Manage.qml"
    case "settings": return "Settings.qml"
    case "privacy": return "DataPrivacy.qml"
    }
    return "Dashboard.qml"
  }

  // ---- building
  readonly property string launcher:
    "/usr/share/omarchy/bin/omarchy-launch-floating-terminal-with-presentation"

  // The launcher embeds what it is given in a bash -c string, so anything
  // interpolated has to be quoted for a shell.
  function shellQuote(s) {
    return "'" + String(s).replace(/'/g, "'\\''") + "'"
  }

  // The shell owns the daemon, so a fresh binary on disk changes nothing until
  // the shell starts it. Absolute for the same reason the launcher is.
  readonly property string restarter:
    "/usr/share/omarchy/bin/omarchy-restart-shell"

  // Detached: a Process owned by this window would die with it.
  function runBuild() {
    root.build("")
  }

  // A stale helper is not a missing one. With nothing built the reprobe timer
  // starts the daemon on its own, but here the old daemon is already running
  // and the old views are loaded, so compiling alone changes nothing on
  // screen. Restart the shell after the build.
  function runRebuild() {
    root.build(" && " + root.shellQuote(root.restarter))
  }

  function build(then) {
    if (!root.buildable) {
      root.lastError = "no Makefile in " + root.pluginDir
                     + ": reinstall with omarchy plugin add"
      return
    }
    Quickshell.execDetached([root.launcher,
      "make -C " + root.shellQuote(root.pluginDir) + then])
  }

  // Is there a Makefile to run? Checked rather than assumed, so the build
  // screen can say what is wrong instead of opening a terminal that fails.
  Process {
    id: buildProbe
    command: ["/usr/bin/test", "-f", root.pluginDir + "/Makefile"]
    clearEnvironment: true
    environment: root.childEnv
    onExited: function (code, status) { root.buildable = code === 0 }
  }

  // A Go source file, tests excluded, or a module file newer than the helper
  // means the helper is behind. Asking the filesystem rather than git makes
  // a local edit read the same as an update.
  Process {
    id: staleProbe
    command: ["/usr/bin/find", root.pluginDir,
              "(", "-name", "*.go", "-not", "-name", "*_test.go",
              "-o", "-name", "go.mod", "-o", "-name", "go.sum", ")",
              "-newer", root.helperPath, "-print", "-quit"]
    clearEnvironment: true
    environment: root.childEnv
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.stale = String(text || "").trim().length > 0
    }
  }
}
