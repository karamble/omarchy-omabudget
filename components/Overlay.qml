import QtQuick
import qs.Commons
import qs.Ui

// The surface every form floats on.
//
// Lifted from the inner card of the kit's PopupCard, which cannot be used
// directly: that one is a Quickshell popup window and needs a bar to anchor
// itself against, so it would open a second compositor surface rather than an
// overlay inside this window.
//
// Taking the border from a surface spec rather than a flat width means a theme
// can set [popups] border-width and this follows it, which eight hand-rolled
// copies of `border.width: 1` never did.
BorderSurface {
  id: surface

  // The loaders that carry these forms destroy them on close, which is what
  // keeps a reopened card blank, so only the arrival can be animated. It is
  // also the half worth animating: a form closing is accompanied by a toast
  // and a list that redraws, while a form arriving has nothing else to say
  // where it came from.
  property int fadeMs: 140

  radius: Style.cornerRadius
  color: Color.popups.background
  borderSpec: Border.surfaceSpec("popups", "border", Color.popups.border, Style.normalBorderWidth)

  opacity: 0
  scale: 0.98
  Component.onCompleted: {
    surface.opacity = 1
    surface.scale = 1
  }
  Behavior on opacity { NumberAnimation { duration: surface.fadeMs; easing.type: Easing.OutCubic } }
  Behavior on scale { NumberAnimation { duration: surface.fadeMs; easing.type: Easing.OutCubic } }
}
