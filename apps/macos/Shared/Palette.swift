import SwiftUI

/// Beacon's colour language. One decision, applied everywhere: state is the
/// only thing that carries colour. Everything else is greyscale, so a
/// coloured pixel anywhere on the screen means something about health and
/// nothing else does.
extension HealthState {
    var tint: Color {
        switch self {
        case .healthy: Color(red: 0.24, green: 0.66, blue: 0.42)
        case .warning: Color(red: 0.85, green: 0.62, blue: 0.16)
        case .down: Color(red: 0.83, green: 0.29, blue: 0.24)
        case .unknown: Color.secondary
        }
    }
}

/// A filled dot carrying a state's colour. Kept to one shape at one size so
/// a column of them reads as a single scale rather than a set of icons.
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
