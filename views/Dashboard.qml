import QtQuick
import qs.Commons
import qs.Ui
import "../charts"

// This period at a glance: cash, spending, savings, budgets, cashflow, the
// latest transactions and the accounts. Everything drawn here comes from
// app.snap, the daemon's dashboard document; a field the daemon does not send
// leaves its card in the empty state.
Item {
  id: view
  property var app: null
  readonly property bool formFocused: false

  // ---- the document
  readonly property var snap: app ? app.snap : null
  readonly property var totals: snap && snap.totals ? snap.totals : null
  readonly property var previous: snap && snap.previous ? snap.previous : null
  readonly property var cash: snap && snap.cash ? snap.cash : null
  readonly property var budget: snap && snap.budget ? snap.budget : null
  readonly property var months: snap && snap.months && snap.months.length !== undefined ? snap.months : []
  readonly property var cards: budget && budget.cards && budget.cards.length !== undefined ? budget.cards : []
  readonly property var accounts: snap && snap.accounts && snap.accounts.length !== undefined ? snap.accounts : []
  readonly property var recent: snap && snap.recent && snap.recent.length !== undefined ? snap.recent : []
  readonly property var bills: snap && snap.bills && snap.bills.length !== undefined ? snap.bills : []
  readonly property var insights: snap && snap.insights && snap.insights.length !== undefined ? snap.insights : []
  // Currencies with no rate on file, left out of cash on hand and net worth.
  readonly property var unconverted: snap && snap.unconverted && snap.unconverted.length !== undefined ? snap.unconverted : []
  readonly property string cur: snap && snap.baseCurrency ? String(snap.baseCurrency) : ""

  readonly property real planned: budget ? Number(budget.planned) || 0 : 0
  // Spending against the plan is the daemon's figure over the budgeted
  // categories only; the headline is every expense in the period.
  readonly property real budgetSpent: budget ? Number(budget.spent) || 0 : 0
  readonly property int spendPct: budget ? Number(budget.pct) || 0 : 0
  // The envelope model's headline replaces the plan's share when it is on.
  readonly property bool envelope: !!snap && snap.model === "envelope"
  readonly property real toBeBudgeted: snap && snap.envelopes ? Number(snap.envelopes.toBeBudgeted) || 0 : 0
  readonly property real spent: totals ? Number(totals.expense) || 0 : 0
  readonly property real earned: totals ? Number(totals.income) || 0 : 0
  readonly property real delta: cash ? Number(cash.delta) || 0 : 0
  // The daemon leaves deltaPct out when there was nothing to compare with.
  readonly property var deltaPct: cash && cash.deltaPct !== undefined && cash.deltaPct !== null
    && isFinite(Number(cash.deltaPct)) ? Number(cash.deltaPct) : null
  readonly property int savingsRate: totals ? Number(totals.savingsRate) || 0 : 0
  readonly property int savingsDiff: totals && previous
    ? savingsRate - (Number(previous.savingsRate) || 0) : 0

  readonly property var cashPoints: view.pluck(cash ? cash.series : null, "liquid")
  readonly property var monthLabels: view.pluck(months, "label")
  readonly property var monthIncome: view.pluck(months, "income")
  readonly property var monthExpense: view.pluck(months, "expense")
  readonly property var monthNet: view.pluck(months, "net")
  readonly property var monthSavings: view.pluck(months, "savingsRate")

  // The month the current period starts in, from the last of the months or
  // from the period itself.
  readonly property string periodLabel: months.length > 0
    ? String(months[months.length - 1].label)
    : (snap && snap.period && snap.period.from ? view.monthName(snap.period.from) : "")
  readonly property string budgetLabel: budget && budget.period
    ? view.monthName(budget.period).toUpperCase() + " " + String(budget.period).slice(0, 4) : ""

  // ---- theme
  readonly property color fg: app ? app.foreground : Color.foreground
  readonly property color dim: app ? app.dim : Color.foreground
  readonly property color dimmer: app ? app.dimmer : Color.foreground
  readonly property color border: app ? app.cardBorder : Style.normalBorderColor
  readonly property color accent: app ? app.accent : Color.accent
  readonly property color income: app ? app.income : Color.accent
  readonly property color expense: app ? app.expense : Color.urgent
  readonly property color net: app ? app.net : Color.foreground
  readonly property color onBudget: app ? app.onBudget : Color.accent
  readonly property color nearLimit: app ? app.nearLimit : Color.urgent
  readonly property color overLimit: app ? app.overLimit : Color.urgent
  // The app names the house timings; the fallbacks keep this view honest if
  // it is ever built before its app arrives.
  readonly property int quickMs: app ? app.quickMs : 60
  readonly property int colourMs: app ? app.colourMs : 120
  readonly property int moveMs: app ? app.moveMs : 140
  readonly property string ff: app ? app.fontFamily : Style.font.family
  readonly property bool hidden: app ? app.blurAmounts === true : false

  // 3M, 6M or 1Y of cashflow.
  property string cashflowRange: "6M"

  property var now: new Date()
  Timer { interval: 60000; running: true; repeat: true; onTriggered: view.now = new Date() }

  readonly property var monthNames: ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

  function money(minor, currency) { return app ? app.fmt(minor, currency) : String(minor) }
  // A gain carries its plus sign; hidden amounts carry nothing.
  function signed(minor, currency) { return (!view.hidden && Number(minor) >= 0 ? "+" : "") + money(minor, currency) }
  function signedPct(n, digits) { return (n >= 0 ? "+" : "") + Number(n).toFixed(digits) + "%" }

  // One field out of every row, in order.
  function pluck(rows, field) {
    var out = []
    if (!rows || rows.length === undefined) return out
    for (var i = 0; i < rows.length; i++) out.push(rows[i] ? rows[i][field] : undefined)
    return out
  }

  // "2026-09-13" or "2026-09" to "Sep".
  function monthName(iso) {
    var m = parseInt(String(iso).slice(5, 7), 10)
    return m >= 1 && m <= 12 ? monthNames[m - 1] : ""
  }
  // "2026-09-13" to "Sep 13".
  function shortDate(iso) {
    var d = parseInt(String(iso).slice(8, 10), 10)
    return monthName(iso) + (d > 0 ? " " + d : "")
  }

  // Axis labels: minor units to a short figure, 150000 to 1.5K.
  function compact(minor) {
    var v = Number(minor) / Math.pow(10, app ? app.decimalsFor(cur) : 2)
    if (!isFinite(v)) return "0"
    var a = Math.abs(v), s
    if (a >= 1000000) s = view.oneDecimal(a / 1000000) + "M"
    else if (a >= 1000) s = view.oneDecimal(a / 1000) + "K"
    else s = view.oneDecimal(a)
    return (v < 0 ? "-" : "") + s
  }
  function oneDecimal(n) { return String(Math.round(n * 10) / 10) }
  // Swapped for a placeholder while amounts are hidden, so the axis hides
  // with the figures.
  readonly property var tickFormat: view.hidden
    ? function (v) { return "•••" }
    : function (v) { return view.compact(v) }

  function stateColor(state) {
    return state === "over" ? view.overLimit : state === "near" ? view.nearLimit : view.onBudget
  }

  // An insight's tone, in the theme's own colours: nothing here is a literal.
  function toneColour(tone) {
    switch (String(tone)) {
    case "good": return view.onBudget
    case "warn": return view.nearLimit
    case "bad": return view.overLimit
    }
    return view.dim
  }
  function toneGlyph(tone) {
    switch (String(tone)) {
    case "good": return "󰄬"
    case "warn": return "󰀪"
    case "bad": return "󰀦"
    }
    return "󰋼"
  }

  // ---- building blocks
  component Card: Rectangle {
    id: card
    default property alias body: slot.data
    property real pad: Style.space(14)
    radius: Style.cornerRadius
    color: "transparent"
    border.width: Style.normalBorderWidth
    border.color: view.border
    Item { id: slot; anchors.fill: parent; anchors.margins: card.pad }
  }

  component Caption: Text {
    color: view.dimmer
    font.family: view.ff
    font.pixelSize: Style.font.caption
    font.letterSpacing: 1
  }

  component Heading: Text {
    color: view.fg
    font.family: view.ff
    font.pixelSize: Style.font.title
    font.letterSpacing: 1
  }

  component Body: Text {
    color: view.fg
    font.family: view.ff
    font.pixelSize: Style.font.body
  }

  // Secondary inline text: a delta, a timestamp, a line of prose. The
  // small-caps heading the rest of the app calls Caption is Caption here too.
  component Note: Text {
    color: view.dim
    font.family: view.ff
    font.pixelSize: Style.font.caption
  }

  // A tile's headline figure: display size, shrinking to fit the width it
  // is given so a long amount never runs under the chart beside it.
  component Figure: Text {
    color: view.fg
    font.family: view.ff
    font.pixelSize: Style.font.display
    font.bold: true
    fontSizeMode: Text.HorizontalFit
    minimumPixelSize: Style.font.body
  }

  // A headline amount that counts to its new value. The whole document is
  // replaced on every poll, so without this each figure blinks. Only the
  // display rolls: shown is a plain number for the animation to move, and the
  // amount it renders is the ledger's own integer minor units.
  component RollingFigure: Figure {
    id: rolling
    property real value: 0
    property string suffix: ""
    property bool amount: true
    property real shown: 0
    Behavior on shown { NumberAnimation { duration: view.moveMs * 3; easing.type: Easing.OutCubic } }
    onValueChanged: rolling.shown = rolling.value
    Component.onCompleted: rolling.shown = rolling.value
    text: rolling.amount ? view.money(Math.round(rolling.shown), view.cur)
                         : Math.round(rolling.shown) + rolling.suffix
  }

  // A link at the right of a card header, opening another view.
  component ViewAll: Item {
    id: link
    property string target: ""
    property string label: "View all"
    width: linkRow.implicitWidth
    height: linkRow.implicitHeight
    Row {
      id: linkRow
      spacing: Style.space(4)
      Note { text: link.label; color: linkArea.containsMouse ? view.fg : view.dim }
      Note { text: "󰅂"; color: linkArea.containsMouse ? view.fg : view.dim }
    }
    MouseArea {
      id: linkArea
      anchors.fill: parent
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: if (view.app) view.app.setView(link.target)
    }
  }

  // A progress bar: the track in the card border colour, the fill by state.
  component Bar: Item {
    id: bar
    property real fraction: 0
    property color fill: view.onBudget
    // Eased on the fraction rather than the width, so the fill travels when
    // the figures move and stays still when only the window does.
    Behavior on fraction { NumberAnimation { duration: view.moveMs; easing.type: Easing.OutCubic } }
    height: Style.space(6)
    Rectangle { anchors.fill: parent; radius: height / 2; color: view.border }
    Rectangle {
      width: bar.width * Math.max(0, Math.min(1, bar.fraction))
      height: bar.height
      radius: height / 2
      color: bar.fill
    }
  }

  Flickable {
    id: flick
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

      // ---- headline
      Item {
        width: parent.width
        height: Math.max(headCol.implicitHeight, infoRow.implicitHeight)

        Column {
          id: headCol
          anchors.left: parent.left
          anchors.verticalCenter: parent.verticalCenter
          spacing: Style.space(4)
          Text {
            text: "Dashboard"
            color: view.fg
            font.family: view.ff
            font.pixelSize: Style.font.heading
          }
          Note {
            visible: !!view.snap && !!view.snap.period
            text: view.snap && view.snap.period
              ? view.snap.period.from + " to " + view.snap.period.to
                + "  ·  day " + view.snap.period.elapsed + " of " + view.snap.period.days
              : ""
            color: view.dimmer
          }
        }

        Row {
          id: infoRow
          anchors.right: parent.right
          anchors.verticalCenter: parent.verticalCenter
          spacing: Style.space(10)

          Card {
            id: privacyCard
            pad: Style.space(10)
            width: privacyRow.implicitWidth + privacyCard.pad * 2
            height: privacyRow.implicitHeight + privacyCard.pad * 2
            Row {
              id: privacyRow
              spacing: Style.space(12)
              Column {
                anchors.verticalCenter: parent.verticalCenter
                spacing: Style.space(2)
                Row {
                  spacing: Style.space(6)
                  Rectangle {
                    anchors.verticalCenter: parent.verticalCenter
                    width: Style.space(6)
                    height: Style.space(6)
                    radius: width / 2
                    color: view.accent
                  }
                  Note { text: "All data stored locally"; color: view.fg }
                }
                Note { text: "No cloud. No tracking. Just you."; color: view.dimmer }
              }
              Text {
                anchors.verticalCenter: parent.verticalCenter
                text: "󰋊"
                color: view.dim
                font.family: view.ff
                font.pixelSize: Style.font.icon
              }
            }
          }

          Card {
            id: clockCard
            pad: Style.space(10)
            width: clockCol.implicitWidth + clockCard.pad * 2
            height: clockCol.implicitHeight + clockCard.pad * 2
            Column {
              id: clockCol
              spacing: Style.space(2)
              Note { text: Qt.formatDateTime(view.now, "ddd").toUpperCase(); color: view.dimmer }
              Text {
                text: Qt.formatDateTime(view.now, "MMM d, yyyy").toUpperCase()
                color: view.fg
                font.family: view.ff
                font.pixelSize: Style.font.bodySmall
              }
              Note { text: Qt.formatDateTime(view.now, "h:mm AP"); color: view.dimmer }
            }
          }
        }
      }

      // ---- stat tiles
      Row {
        id: tiles
        width: parent.width
        spacing: Style.space(12)
        readonly property real tileWidth: (tiles.width - tiles.spacing * 2) / 3
        readonly property real tileHeight: Style.space(112)

        // cash on hand
        Card {
          width: tiles.tileWidth
          height: tiles.tileHeight

          Caption { id: cashLabel; text: "TOTAL CASH ON HAND" }
          RollingFigure {
            anchors.top: cashLabel.bottom
            anchors.topMargin: Style.space(4)
            anchors.left: parent.left
            anchors.right: cashSpark.left
            anchors.rightMargin: Style.space(8)
            value: view.snap ? view.snap.liquid : 0
          }
          Sparkline {
            id: cashSpark
            anchors.right: parent.right
            anchors.top: parent.top
            width: parent.width * 0.38
            height: Style.space(44)
            points: view.cashPoints
            lineColor: view.accent
            // The top of the fade; the chart takes it to nothing at the
            // baseline itself.
            fillColor: Util.alpha(view.accent, 0.22)
            emptyColor: view.dimmer
          }
          Row {
            id: deltaRow
            anchors.left: parent.left
            anchors.bottom: parent.bottom
            width: parent.width
            spacing: Style.space(6)
            visible: !!view.cash
            readonly property color tone: view.delta >= 0 ? view.income : view.expense
            // The figures always show; the trailing words go when the tile
            // is too narrow for the whole row.
            readonly property real figuresWidth: deltaArrow.implicitWidth + deltaRow.spacing + deltaAmount.implicitWidth
              + (deltaPctText.visible ? deltaRow.spacing + deltaPctText.implicitWidth : 0)
            Note { id: deltaArrow; text: view.delta >= 0 ? "▲" : "▼"; color: deltaRow.tone }
            Note { id: deltaAmount; text: view.signed(view.delta, view.cur); color: deltaRow.tone }
            Note {
              id: deltaPctText
              visible: view.deltaPct !== null
              text: view.deltaPct !== null ? view.signedPct(view.deltaPct, 1) : ""
              color: deltaRow.tone
            }
            Note {
              id: deltaWord
              text: "this period"
              visible: deltaRow.figuresWidth + deltaRow.spacing + deltaWord.implicitWidth <= deltaRow.width
            }
          }
        }

        // spending
        Card {
          width: tiles.tileWidth
          height: tiles.tileHeight

          Caption {
            id: spendLabel
            text: "SPENDING" + (view.periodLabel !== "" ? " (" + view.periodLabel.toUpperCase() + ")" : "")
          }
          RollingFigure {
            anchors.top: spendLabel.bottom
            anchors.topMargin: Style.space(4)
            anchors.left: parent.left
            anchors.right: parent.right
            value: view.spent
          }
          Item {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            height: spendText.implicitHeight
            Bar {
              visible: view.planned > 0
              anchors.left: parent.left
              anchors.right: spendText.left
              anchors.rightMargin: Style.space(10)
              anchors.verticalCenter: parent.verticalCenter
              fraction: view.planned > 0 ? view.budgetSpent / view.planned : 0
              // Over on the amounts, as the daemon states it; near on the percentage.
              fill: view.budgetSpent > view.planned ? view.overLimit
                  : view.spendPct >= 85 ? view.nearLimit : view.onBudget
            }
            Note {
              id: spendText
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              text: view.envelope
                ? "to be budgeted " + view.money(view.toBeBudgeted, view.cur)
                : view.planned > 0
                  ? view.spendPct + "% of " + view.money(view.planned, view.cur) + " budget"
                  : "of " + view.money(view.earned, view.cur) + " income"
              color: view.envelope && view.toBeBudgeted < 0 ? view.expense : view.dimmer
            }
          }
        }

        // savings rate
        Card {
          width: tiles.tileWidth
          height: tiles.tileHeight

          Caption { id: savingsLabel; text: "SAVINGS RATE" }
          RollingFigure {
            anchors.top: savingsLabel.bottom
            anchors.topMargin: Style.space(4)
            anchors.left: parent.left
            anchors.right: savingsBars.left
            anchors.rightMargin: Style.space(8)
            value: view.savingsRate
            amount: false
            suffix: "%"
          }
          BarChart {
            id: savingsBars
            anchors.right: parent.right
            anchors.top: parent.top
            width: parent.width * 0.38
            height: Style.space(44)
            values: view.monthSavings
            highlightIndex: view.monthSavings.length - 1
            barColor: view.dimmer
            negativeColor: view.expense
            highlightColor: view.accent
            baselineColor: view.border
            emptyColor: view.dimmer
            barRadius: Style.space(2)
            gap: Style.space(3)
          }
          Row {
            id: savingsRow
            anchors.left: parent.left
            anchors.bottom: parent.bottom
            spacing: Style.space(6)
            visible: !!view.previous && !!view.totals
            readonly property color tone: view.savingsDiff >= 0 ? view.income : view.expense
            Note { text: view.savingsDiff >= 0 ? "▲" : "▼"; color: savingsRow.tone }
            Note { text: view.signedPct(view.savingsDiff, 0); color: savingsRow.tone }
            Note { text: "from last period" }
          }
        }
      }

      // ---- what is worth saying about this period
      Row {
        id: insightRow
        width: parent.width
        visible: view.insights.length > 0
        spacing: Style.space(12)
        Repeater {
          model: view.insights
          delegate: Card {
            id: insightCard
            required property var modelData
            readonly property color tone: view.toneColour(modelData.tone)
            width: (insightRow.width - insightRow.spacing * (view.insights.length - 1)) / view.insights.length
            height: Style.space(54)
            pad: Style.space(12)
            border.color: insightCard.tone
            Row {
              anchors.fill: parent
              spacing: Style.space(8)
              Text {
                width: Style.space(22)
                anchors.verticalCenter: parent.verticalCenter
                text: view.toneGlyph(insightCard.modelData.tone)
                color: insightCard.tone
                font.family: view.ff
                font.pixelSize: Style.font.icon
              }
              Text {
                width: parent.width - Style.space(22) - parent.spacing
                anchors.verticalCenter: parent.verticalCenter
                text: String(insightCard.modelData.text || "")
                color: view.fg
                font.family: view.ff
                font.pixelSize: Style.font.bodySmall
                wrapMode: Text.WordWrap
                maximumLineCount: 2
                elide: Text.ElideRight
              }
            }
          }
        }
      }

      // ---- budget by category + cashflow
      Row {
        id: middle
        width: parent.width
        spacing: Style.space(12)
        readonly property real leftWidth: Math.floor((middle.width - middle.spacing) * 0.58)
        readonly property real rightWidth: middle.width - middle.spacing - middle.leftWidth

        Card {
          id: budgetCard
          width: middle.leftWidth
          height: Math.max(Style.space(236), budgetCol.implicitHeight + budgetCard.pad * 2)

          Column {
            id: budgetCol
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            spacing: Style.space(12)

            Item {
              width: parent.width
              height: Style.space(20)
              Heading { anchors.verticalCenter: parent.verticalCenter; text: "BUDGET BY CATEGORY" }
              ViewAll {
                anchors.right: parent.right
                anchors.verticalCenter: parent.verticalCenter
                label: view.budgetLabel !== "" ? view.budgetLabel : "View all"
                target: "budget"
              }
            }

            Body {
              visible: view.cards.length === 0
              width: parent.width
              wrapMode: Text.WordWrap
              text: "No budgets yet. Press 4 to plan one."
              color: view.dim
            }

            Grid {
              id: budgetGrid
              visible: view.cards.length > 0
              width: parent.width
              columns: 3
              spacing: Style.space(10)
              readonly property real cellWidth: (budgetGrid.width - budgetGrid.spacing * 2) / 3

              Repeater {
                model: view.cards.slice(0, 6)
                delegate: Rectangle {
                  id: cell
                  required property var modelData
                  width: budgetGrid.cellWidth
                  height: Style.space(82)
                  radius: Style.cornerRadius
                  color: "transparent"
                  border.width: Style.normalBorderWidth
                  border.color: view.border

                  Row {
                    id: cellHead
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.top: parent.top
                    anchors.margins: Style.space(10)
                    spacing: Style.space(8)
                    Text {
                      width: Style.space(20)
                      text: cell.modelData.icon ? String(cell.modelData.icon) : ""
                      color: view.dim
                      font.family: view.ff
                      font.pixelSize: Style.font.icon
                    }
                    Column {
                      width: cellHead.width - Style.space(28)
                      spacing: Style.space(2)
                      Body {
                        width: parent.width
                        elide: Text.ElideRight
                        text: cell.modelData.name ? String(cell.modelData.name) : ""
                      }
                      Row {
                        spacing: Style.space(4)
                        Note { text: view.money(cell.modelData.spent, view.cur); color: view.fg }
                        Note { text: "/ " + view.money(cell.modelData.planned, view.cur) }
                      }
                    }
                  }
                  Item {
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    anchors.margins: Style.space(10)
                    height: pctText.implicitHeight
                    Bar {
                      anchors.left: parent.left
                      anchors.right: pctText.left
                      anchors.rightMargin: Style.space(8)
                      anchors.verticalCenter: parent.verticalCenter
                      fraction: cell.modelData.planned > 0 ? cell.modelData.spent / cell.modelData.planned : 0
                      fill: view.stateColor(cell.modelData.state)
                    }
                    Note {
                      id: pctText
                      anchors.right: parent.right
                      anchors.verticalCenter: parent.verticalCenter
                      text: (Number(cell.modelData.pct) || 0) + "%"
                    }
                  }
                }
              }
            }

            Note {
              visible: view.cards.length > 6
              text: "and " + (view.cards.length - 6) + " more, press 4"
            }
          }
        }

        Card {
          id: cashflowCard
          width: middle.rightWidth
          height: budgetCard.height

          Item {
            id: cashflowHead
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            height: Math.max(Style.space(20), rangeGroup.implicitHeight)
            Heading { anchors.verticalCenter: parent.verticalCenter; text: "CASHFLOW" }
            ButtonGroup {
              id: rangeGroup
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              options: ["3M", "6M", "1Y"]
              value: view.cashflowRange
              foreground: view.fg
              accent: view.accent
              fontFamily: view.ff
              fontSize: Style.font.caption
              focusable: false
              onChanged: function (v) { view.cashflowRange = v }
            }
          }

          GroupedBarChart {
            id: cashflowChart
            anchors.left: parent.left
            anchors.right: legend.left
            anchors.rightMargin: Style.space(10)
            anchors.top: cashflowHead.bottom
            anchors.topMargin: Style.space(8)
            anchors.bottom: parent.bottom
            labels: view.monthLabels
            series: [
              { label: "Income", color: view.income, values: view.monthIncome },
              { label: "Expenses", color: view.expense, values: view.monthExpense },
              { label: "Net", color: view.net, values: view.monthNet }
            ]
            visibleGroups: view.cashflowRange === "3M" ? 3 : view.cashflowRange === "6M" ? 6 : 0
            axisColor: view.dimmer
            gridColor: view.border
            textColor: view.dimmer
            legendTextColor: view.dim
            fontFamily: view.ff
            fontSize: Style.font.caption
            formatValue: view.tickFormat
            // The legend is drawn here instead, in its own colour.
            showLegend: false
            emptyText: "Nothing to chart yet. Press n to add a transaction."

            // Grown from the zero line on arrival and whenever the range
            // changes, so switching 3M to 1Y reads as a zoom rather than a
            // different chart appearing.
            progress: 0
            Behavior on progress { NumberAnimation { duration: view.moveMs * 2; easing.type: Easing.OutCubic } }
            Component.onCompleted: cashflowChart.progress = 1
            onVisibleGroupsChanged: { cashflowChart.progress = 0; cashflowChart.progress = 1 }

            HoverHandler {
              id: cashflowHover
              onPointChanged: cashflowChart.hoverGroup = cashflowChart.groupAt(point.position.x)
              onHoveredChanged: if (!hovered) cashflowChart.hoverGroup = -1
            }

            // The readout. A grouped chart with no way to read a value sends
            // a person to the table below it to answer the obvious question.
            Rectangle {
              id: readout
              visible: cashflowHover.hovered && cashflowChart.hoverGroup >= 0
                       && cashflowChart.hoverGroup < view.months.length
              readonly property var month: visible
                ? view.months[view.months.length - (cashflowChart.visibleGroups > 0
                    ? Math.min(cashflowChart.visibleGroups, view.months.length) : view.months.length)
                    + cashflowChart.hoverGroup]
                : null
              width: readoutCol.implicitWidth + Style.space(16)
              height: readoutCol.implicitHeight + Style.space(12)
              x: Math.max(0, Math.min(parent.width - width, cashflowHover.point.position.x + Style.space(10)))
              y: Style.space(4)
              z: 5
              radius: Style.cornerRadius
              color: Color.popups.background
              border.width: Style.normalBorderWidth
              border.color: Color.popups.border
              Column {
                id: readoutCol
                anchors.centerIn: parent
                spacing: Style.space(2)
                Caption { text: readout.month ? String(readout.month.label) : "" }
                Note { text: readout.month ? "income  " + view.money(readout.month.income, view.cur) : ""; color: view.income }
                Note { text: readout.month ? "expense " + view.money(readout.month.expense, view.cur) : ""; color: view.expense }
                Note { text: readout.month ? "net     " + view.money(readout.month.net, view.cur) : ""; color: view.net }
              }
            }
          }

          Column {
            id: legend
            anchors.right: parent.right
            anchors.top: cashflowHead.bottom
            anchors.topMargin: Style.space(20)
            spacing: Style.space(8)
            Repeater {
              model: [
                { label: "Income", tone: view.income },
                { label: "Expenses", tone: view.expense },
                { label: "Net", tone: view.net }
              ]
              delegate: Row {
                id: key
                required property var modelData
                spacing: Style.space(6)
                Rectangle {
                  anchors.verticalCenter: parent.verticalCenter
                  width: Style.space(8)
                  height: Style.space(8)
                  color: key.modelData.tone
                }
                Note { text: key.modelData.label }
              }
            }
          }
        }
      }

      // ---- recent transactions + bills and accounts
      Row {
        id: bottom
        width: parent.width
        spacing: Style.space(12)

        Card {
          id: recentCard
          width: middle.leftWidth
          height: Math.max(recentCol.implicitHeight + recentCard.pad * 2,
                           billsCard.height + rightCol.spacing + accountsCol.implicitHeight + recentCard.pad * 2)

          Column {
            id: recentCol
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            spacing: Style.space(6)

            // Column widths of the table; they add up to the card's width.
            readonly property real dateW: Style.space(56)
            readonly property real iconW: Style.space(26)
            readonly property real amountW: Style.space(104)
            readonly property real catW: Math.floor((recentCol.width - recentCol.dateW - recentCol.iconW - recentCol.amountW) * 0.38)
            readonly property real descW: recentCol.width - recentCol.dateW - recentCol.iconW - recentCol.amountW - recentCol.catW

            Item {
              width: parent.width
              height: Style.space(20)
              Heading { anchors.verticalCenter: parent.verticalCenter; text: "RECENT TRANSACTIONS" }
              ViewAll { anchors.right: parent.right; anchors.verticalCenter: parent.verticalCenter; target: "transactions" }
            }

            Rectangle {
              width: parent.width
              height: Style.space(22)
              radius: Style.cornerRadius
              color: Qt.rgba(view.fg.r, view.fg.g, view.fg.b, 0.05)
              Caption { x: Style.space(6); anchors.verticalCenter: parent.verticalCenter; text: "DATE" }
              Caption { x: recentCol.dateW + recentCol.iconW; anchors.verticalCenter: parent.verticalCenter; text: "DESCRIPTION" }
              Caption { x: recentCol.dateW + recentCol.iconW + recentCol.descW; anchors.verticalCenter: parent.verticalCenter; text: "CATEGORY" }
              Caption {
                anchors.right: parent.right
                anchors.rightMargin: Style.space(6)
                anchors.verticalCenter: parent.verticalCenter
                text: "AMOUNT"
              }
            }

            Body {
              visible: view.recent.length === 0
              text: "Nothing yet. Press n to add one in under ten seconds."
              color: view.dim
              topPadding: Style.space(4)
            }

            Repeater {
              model: view.recent
              delegate: Item {
                id: row
                required property var modelData
                width: recentCol.width
                height: Style.space(26)
                readonly property bool transfer: row.modelData.kind === "transfer"
                readonly property string category: row.modelData.categoryName ? String(row.modelData.categoryName)
                                                 : (row.modelData.categoryId ? String(row.modelData.categoryId) : "")

                Note {
                  x: Style.space(6)
                  anchors.verticalCenter: parent.verticalCenter
                  text: view.shortDate(row.modelData.date)
                  color: view.dimmer
                  font.pixelSize: Style.font.bodySmall
                }
                Text {
                  x: recentCol.dateW
                  anchors.verticalCenter: parent.verticalCenter
                  text: row.modelData.categoryIcon ? String(row.modelData.categoryIcon) : ""
                  color: view.dim
                  font.family: view.ff
                  font.pixelSize: Style.font.icon
                }
                Body {
                  x: recentCol.dateW + recentCol.iconW
                  width: recentCol.descW - Style.space(8)
                  anchors.verticalCenter: parent.verticalCenter
                  elide: Text.ElideRight
                  text: row.modelData.description ? String(row.modelData.description)
                      : (row.transfer ? "Transfer" : row.category)
                }
                Body {
                  x: recentCol.dateW + recentCol.iconW + recentCol.descW
                  width: recentCol.catW - Style.space(8)
                  anchors.verticalCenter: parent.verticalCenter
                  elide: Text.ElideRight
                  text: row.transfer ? "" : row.category
                  color: view.dim
                }
                Body {
                  anchors.right: parent.right
                  anchors.rightMargin: Style.space(6)
                  anchors.verticalCenter: parent.verticalCenter
                  text: (!view.hidden && row.modelData.amount > 0 ? "+" : "") + view.money(row.modelData.amount, row.modelData.currency)
                  color: row.transfer ? view.dim : row.modelData.amount < 0 ? view.expense : view.income
                }
              }
            }
          }
        }

        Column {
          id: rightCol
          width: middle.rightWidth
          spacing: Style.space(12)

          Card {
            id: billsCard
            width: parent.width
            height: billsCol.implicitHeight + billsCard.pad * 2

            Column {
              id: billsCol
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.top: parent.top
              spacing: Style.space(10)

              Item {
                width: parent.width
                height: Style.space(20)
                Heading { anchors.verticalCenter: parent.verticalCenter; text: "UPCOMING BILLS" }
                ViewAll { anchors.right: parent.right; anchors.verticalCenter: parent.verticalCenter; target: "bills" }
              }
              Body {
                visible: view.bills.length === 0
                width: parent.width
                wrapMode: Text.WordWrap
                text: "Nothing due in the next thirty days. Press 5 to add one."
                color: view.dim
              }
              Repeater {
                model: view.bills.slice(0, 6)
                delegate: Item {
                  id: bill
                  required property var modelData
                  width: parent.width
                  height: Style.space(22)
                  readonly property bool soon: !modelData.overdue && modelData.daysUntil <= 3
                  Caption {
                    id: billDate
                    anchors.left: parent.left
                    anchors.verticalCenter: parent.verticalCenter
                    width: Style.space(48)
                    text: String(bill.modelData.date || "").slice(5)
                    color: bill.modelData.overdue ? view.expense : bill.soon ? view.nearLimit : view.dimmer
                  }
                  Text {
                    id: billIcon
                    anchors.left: billDate.right
                    anchors.verticalCenter: parent.verticalCenter
                    width: Style.space(24)
                    text: bill.modelData.kind === "transfer" ? "" : (bill.modelData.categoryIcon || "")
                    color: view.dim
                    font.family: view.ff
                    font.pixelSize: Style.font.icon
                  }
                  Body {
                    anchors.left: billIcon.right
                    anchors.right: billAmount.left
                    anchors.rightMargin: Style.space(8)
                    anchors.verticalCenter: parent.verticalCenter
                    text: bill.modelData.name + (bill.modelData.autoPost ? "  auto" : "")
                    color: bill.modelData.overdue ? view.fg : view.fg
                    elide: Text.ElideRight
                  }
                  Body {
                    id: billAmount
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    text: view.money(bill.modelData.amount, bill.modelData.currency)
                    color: bill.modelData.kind === "income" ? view.income : view.fg
                  }
                }
              }
              Note {
                visible: view.bills.length > 6
                text: "and " + (view.bills.length - 6) + " more, press 5"
                color: view.dimmer
              }
            }
          }

          Card {
            id: accountsCard
            width: parent.width
            height: recentCard.height - billsCard.height - rightCol.spacing

            Column {
              id: accountsCol
              anchors.left: parent.left
              anchors.right: parent.right
              anchors.top: parent.top
              spacing: Style.space(6)

              Item {
                width: parent.width
                height: Style.space(20)
                Heading { anchors.verticalCenter: parent.verticalCenter; text: "ACCOUNTS" }
                ViewAll { anchors.right: parent.right; anchors.verticalCenter: parent.verticalCenter; target: "accounts" }
              }
              Body {
                visible: view.accounts.length === 0
                width: parent.width
                wrapMode: Text.WordWrap
                text: "No accounts yet. Press 2 for Accounts, then a to add one."
                color: view.dim
                topPadding: Style.space(4)
              }
              Repeater {
                model: view.accounts
                delegate: Item {
                  id: acct
                  required property var modelData
                  width: accountsCol.width
                  height: Style.space(26)
                  Body {
                    anchors.left: parent.left
                    anchors.verticalCenter: parent.verticalCenter
                    text: acct.modelData.name ? String(acct.modelData.name) : ""
                    elide: Text.ElideRight
                    width: parent.width * 0.5
                  }
                  Body {
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    text: view.money(acct.modelData.balance, acct.modelData.currency) + " " + (acct.modelData.currency || "")
                    color: acct.modelData.balance < 0 ? view.expense : view.fg
                  }
                }
              }
              Note {
                visible: view.unconverted.length > 0
                width: parent.width
                wrapMode: Text.WordWrap
                text: "No rate on file for " + view.unconverted.join(", ")
                  + ": left out of cash on hand and net worth. Add one under Manage, Rates."
                color: view.app ? view.app.urgent : view.fg
                topPadding: Style.space(4)
              }
            }
          }
        }
      }
    }
  }
}
