pragma Singleton

import QtQuick
import Quickshell.Io

// Interface language. The strings live in JSON files beside this one and are
// read at runtime, so a translation is data and nothing here is compiled.
//
// A singleton rather than a property on the App hub, because the bar widget
// and the service each run from their own root and share nothing with the app:
// all three resolve pluginDir and spawn their own helper independently. A
// hub property would reach the views and leave the bar in English.
//
// The qmldir beside this file carries no module line on purpose. These files
// are imported by relative path, `import "i18n"`, which is how the plugin
// already pulls in components and charts, and which needs no import path from
// the host.
QtObject {
  id: root

  // Our own directory, which is where the language files are. The same idiom
  // the rest of the plugin uses to find bin/omabudget.
  readonly property string dir: Qt.resolvedUrl(".").toString()
                                  .replace(/^file:\/\//, "").replace(/\/$/, "")

  // What a language file may declare about itself, so adding one is data.
  readonly property var available: [
    { tag: "en", name: "English" },
    { tag: "pt-BR", name: "Português (Brasil)" }
  ]

  // The active language tag. The app assigns this from the stored setting.
  property string language: "en"

  // Parsed documents. English is always loaded and is the fallback; the active
  // language is loaded over it. Reassigned wholesale, never mutated: a binding
  // that reads t() re-evaluates when these change, and mutating in place fires
  // no change signal and silently leaves the old language on screen.
  property var base: ({})
  property var active: ({})

  // The locale drives separators and dates, which a budget gets wrong in a
  // more obvious way than any label. Declared by the language file so a new
  // translation does not need a code change.
  readonly property string localeCode: {
    var l = root.active && root.active.locale ? String(root.active.locale) : ""
    if (l !== "") return l
    var b = root.base && root.base.locale ? String(root.base.locale) : ""
    return b !== "" ? b : "en_US"
  }

  readonly property var locale: Qt.locale(root.localeCode)

  // t resolves a key: the active language, then English, then the key itself.
  // A key on screen means a missing entry, which is the intended tell.
  function t(key) {
    var k = String(key)
    var a = root.active && root.active.strings ? root.active.strings : null
    if (a && a[k] !== undefined) return a[k]
    var b = root.base && root.base.strings ? root.base.strings : null
    if (b && b[k] !== undefined) return b[k]
    return k
  }

  // tf fills %1, %2 ... in a resolved string. Translations need to move the
  // values around, so the order lives in the translation rather than in the
  // concatenation at the call site.
  function tf(key, args) {
    var s = String(root.t(key))
    if (args === undefined || args === null) return s
    var list = Array.isArray(args) ? args : [args]
    for (var i = 0; i < list.length; i++) {
      s = s.split("%" + (i + 1)).join(String(list[i]))
    }
    return s
  }

  // parse keeps a malformed or missing file from taking the UI down: the
  // fallback chain already answers for anything that fails to load.
  function parse(text, what) {
    var raw = String(text || "").trim()
    if (raw === "") return {}
    try {
      var doc = JSON.parse(raw)
      return (doc && typeof doc === "object") ? doc : {}
    } catch (e) {
      console.warn("omabudget: i18n: " + what + " is not readable:", e)
      return {}
    }
  }

  // blockLoading, so the first frame is already translated. Without it every
  // label renders as its key and then flips, which reads as a broken window.
  // The files are a few KB and this is the only read.
  readonly property FileView baseFile: FileView {
    path: root.dir + "/en.json"
    blockLoading: true
    printErrors: false
    onLoaded: root.base = root.parse(text(), "en.json")
    onLoadFailed: root.base = ({})
  }

  readonly property FileView activeFile: FileView {
    path: root.dir + "/" + root.language + ".json"
    blockLoading: true
    printErrors: false
    onLoaded: root.active = root.parse(text(), root.language + ".json")
    onLoadFailed: root.active = ({})
  }
}
