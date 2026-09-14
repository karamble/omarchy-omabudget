import QtQuick
import qs.Commons
import qs.Ui

// One verb on a row, shown when the pointer is on it. The keys do the same
// things and say so in the footer; this is for the hand that is already on the
// mouse, and for the verbs a footer legend cannot make discoverable.
//
// It paints nothing of its own until hovered, so a row of these is quiet until
// wanted.
Rectangle {
  id: act

  property var app: null
  property string glyph: ""
  property string hint: ""
  // The colour the glyph takes under the pointer. Destructive verbs pass the
  // expense colour so they do not read like the rest.
  property color tint: app ? app.accent : Color.accent
  property real size: Style.space(28)

  signal triggered()

  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property string ff: app ? app.fontFamily : Style.font.family
  readonly property int colourMs: app ? app.colourMs : 120

  width: size
  height: size
  radius: Style.cornerRadius
  color: actMouse.containsMouse ? Style.hoverFill : "transparent"
  Behavior on color { ColorAnimation { duration: act.colourMs } }

  Text {
    anchors.centerIn: parent
    text: act.glyph
    color: actMouse.containsMouse ? act.tint : act.dimmer
    font.family: act.ff
    font.pixelSize: Style.font.body
    Behavior on color { ColorAnimation { duration: act.colourMs } }
  }

  MouseArea {
    id: actMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: act.triggered()
    // The kit's tooltip, not the attached one from Controls, which is not
    // imported in these views.
    PanelToolTip {
      visible: actMouse.containsMouse && act.hint !== ""
      text: act.hint
      fontFamily: act.ff
    }
  }
}
