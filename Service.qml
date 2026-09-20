import QtQuick
import Quickshell
import Quickshell.Io

// The daemon, owned by the shell. Built when the plugin is enabled, destroyed
// when it is disabled or removed. No systemd unit is installed.
Item {
  id: root

  visible: false
  width: 0
  height: 0

  // Injected by the shell when it constructs the service.
  property var shell: null
  property var manifest: null
  property var pluginRegistry: null

  readonly property string pluginDir: Qt.resolvedUrl(".").toString()
                                        .replace(/^file:\/\//, "").replace(/\/$/, "")
  readonly property string daemonPath: pluginDir + "/bin/omabudgetd"
  readonly property string helperPath: pluginDir + "/bin/omabudget"

  // Respawn on failure, with backoff and a ceiling.
  readonly property int maxRestarts: 5
  property int restarts: 0
  property string lastError: ""
  // The bar and the app read these rather than probing the filesystem
  // themselves.
  property bool built: false
  property bool running: daemon.running

  function backoffMs() {
    return Math.min(30000, 1000 * Math.pow(2, root.restarts))
  }

  // Passed to every child. PATH reaches notify-send in /usr/bin, HOME finds
  // the configuration, and the session bus is what notify-send needs to reach
  // the notification daemon. The proxy and trust-root variables come through
  // when they are set, so the one connection the daemon opens outward, a rate
  // fetch a person asked for, goes the way this machine is configured to
  // send it. Nothing else.
  readonly property var childEnv: root.buildEnv()

  // The list lives in the function, not in a property: a property declared
  // after childEnv is still null when childEnv first evaluates.
  function buildEnv() {
    var env = {
      "PATH": "/usr/bin:/bin",
      "HOME": Quickshell.env("HOME") || "",
      "XDG_RUNTIME_DIR": Quickshell.env("XDG_RUNTIME_DIR") || "",
      "DBUS_SESSION_BUS_ADDRESS": Quickshell.env("DBUS_SESSION_BUS_ADDRESS") || ""
    }
    var passed = ["HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy",
                  "SSL_CERT_FILE", "SSL_CERT_DIR"]
    for (var i = 0; i < passed.length; i++) {
      var v = Quickshell.env(passed[i])
      if (v) env[passed[i]] = String(v)
    }
    return env
  }

  // bin/ is not shipped, so a fresh clone has nothing to run. Both binaries
  // are checked: the app and the bar shell out to the CLI for every read, and
  // a build interrupted between the two leaves one without the other, which
  // would otherwise report built and then fail every query.
  Process {
    id: probe
    command: ["/usr/bin/test", "-x", root.daemonPath, "-a", "-x", root.helperPath]
    running: true
    clearEnvironment: true
    environment: root.childEnv

    onExited: function (code, status) {
      root.built = code === 0
      if (code === 0) {
        root.lastError = ""
        if (!daemon.running) daemon.running = true
        return
      }
      root.lastError = "not built yet"
      reprobe.restart()
    }
  }

  // Keep looking while there is nothing to run, so building the helper starts
  // the daemon without a shell restart.
  Timer {
    id: reprobe
    interval: 5000
    repeat: false
    onTriggered: if (!daemon.running) probe.running = true
  }

  Process {
    id: daemon
    command: [root.daemonPath]
    clearEnvironment: true
    environment: root.childEnv

    onExited: function (code, status) {
      // A clean exit means it was told to stop.
      if (code === 0) return
      if (root.restarts >= root.maxRestarts) {
        root.lastError = "gave up after " + root.maxRestarts + " restarts"
        return
      }
      root.restarts++
      respawn.interval = root.backoffMs()
      respawn.restart()
    }

    stderr: StdioCollector {
      waitForEnd: false
      onStreamFinished: {
        var msg = String(text || "").trim()
        if (msg !== "") root.lastError = msg.split("\n")[0]
      }
    }
  }

  // Signal a process and its group. execDetached rather than a Process of our
  // own, because this is also called while this component is being destroyed,
  // and a Process owned by it would be torn down before it ran. A target that
  // has already gone makes kill fail harmlessly.
  function reap(pid) {
    if (!pid || pid <= 0) return
    Quickshell.execDetached(["/usr/bin/kill", "-TERM", "--", String(pid), "-" + pid])
  }

  // The probe is a one-shot test that should answer immediately. If it ever
  // does not, stop it rather than leaving a process and its collector alive.
  Timer {
    id: probeWatchdog
    interval: 10000
    repeat: false
    running: probe.running
    onTriggered: {
      if (!probe.running) return
      root.reap(probe.processId)
      probe.running = false
      root.lastError = "the build probe did not answer"
    }
  }

  // A daemon that has stayed up is not crash looping, so the budget is
  // returned rather than spent for the life of the session.
  Timer {
    interval: 120000
    repeat: true
    running: daemon.running
    onTriggered: if (root.restarts > 0) root.restarts = 0
  }

  Timer {
    id: respawn
    repeat: false
    onTriggered: if (!daemon.running) daemon.running = true
  }

  // Nothing destructive here: this also fires when the widget is toggled off.
  // Stopping the daemon is not destructive, and reaping its group is what
  // stops anything it spawned from outliving it.
  Component.onDestruction: {
    respawn.stop()
    reprobe.stop()
    probeWatchdog.stop()
    root.reap(daemon.processId)
    daemon.running = false
  }
}
