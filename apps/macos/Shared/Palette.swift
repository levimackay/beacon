import SwiftUI

/// Beacon's visual language.
///
/// The widget is monochrome on purpose. Healthy infrastructure is drawn in
/// white on near-black and carries no colour at all, so the moment any
/// colour appears on the screen it means something is wrong. A palette that
/// tints everything by state spends its loudest signal on the case that
/// needs it least, and leaves nothing in reserve for the case that does.
enum Ink {
    static let background = Color(red: 0.04, green: 0.04, blue: 0.05)
    static let paper = Color(red: 0.96, green: 0.96, blue: 0.97)
    static let muted = Color(white: 0.62)
    static let faint = Color(white: 0.38)
    static let hairline = Color(white: 1.0).opacity(0.12)
    static let track = Color(white: 1.0).opacity(0.14)

    static let alarm = Color(red: 0.95, green: 0.33, blue: 0.28)
    static let caution = Color(red: 0.98, green: 0.76, blue: 0.28)
}

extension HealthState {
    /// Healthy and unknown draw in the monochrome palette. Only a real
    /// problem spends colour.
    var tint: Color {
        switch self {
        case .healthy: Ink.paper
        case .warning: Ink.caution
        case .down: Ink.alarm
        case .unknown: Ink.faint
        }
    }

    /// The word shown at display size. Short enough to stay on one line at
    /// the small widget's width.
    var display: String {
        switch self {
        case .healthy: "ALL\nCLEAR"
        case .warning: "CHECK\nTHIS"
        case .down: "SOMETHING\nIS DOWN"
        case .unknown: "NO\nSIGNAL"
        }
    }
}

/// A filled dot carrying a state's colour.
struct StateDot: View {
    var state: HealthState
    var size: CGFloat = 8

    var body: some View {
        Circle()
            .fill(state.tint)
            .frame(width: size, height: size)
            .accessibilityLabel(state.rawValue)
    }
}

/// Small caps monospace, used for labels that should read as instrumentation
/// rather than prose.
struct Micro: View {
    var text: String
    var color: Color = Ink.faint
    var size: CGFloat = 8.5

    init(_ text: String, color: Color = Ink.faint, size: CGFloat = 8.5) {
        self.text = text
        self.color = color
        self.size = size
    }

    var body: some View {
        Text(text.uppercased())
            .font(.system(size: size, weight: .semibold, design: .monospaced))
            .tracking(1.1)
            .foregroundStyle(color)
    }
}

struct Hairline: View {
    var body: some View {
        Rectangle().fill(Ink.hairline).frame(height: 1)
    }
}

/// A horizontal bar meter. The filled portion is drawn in the paper colour
/// until the value crosses into warning territory, at which point it is the
/// only coloured thing on the widget.
struct Meter: View {
    var label: String
    var value: Double
    var warnAt: Double = 85
    var alarmAt: Double = 95
    var showsValue: Bool = true

    private var fill: Color {
        if value >= alarmAt { return Ink.alarm }
        if value >= warnAt { return Ink.caution }
        return Ink.paper
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: 4) {
                Micro(label)
                Spacer(minLength: 2)
                if showsValue {
                    Text("\(Int(value.rounded()))")
                        .font(.system(size: 10, weight: .medium, design: .monospaced))
                        .foregroundStyle(Ink.muted)
                }
            }
            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    Capsule().fill(Ink.track)
                    Capsule()
                        .fill(fill)
                        .frame(width: max(2, geo.size.width * min(1, max(0, value / 100))))
                }
            }
            .frame(height: 3)
        }
    }
}

/// A row of ticks, one per monitored target, so the widget shows the shape
/// of the estate at a glance even when every entry is fine. It fills space
/// with information rather than padding.
struct TargetTicks: View {
    var states: [HealthState]
    var height: CGFloat = 14

    var body: some View {
        HStack(spacing: 3) {
            ForEach(Array(states.enumerated()), id: \.offset) { _, state in
                RoundedRectangle(cornerRadius: 1)
                    .fill(state == .healthy ? Ink.paper.opacity(0.85) : state.tint)
                    .frame(height: height)
            }
        }
    }
}
