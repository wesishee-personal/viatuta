import SwiftUI

/// A hazard on the map: a coloured disc with the kind's icon.
///
/// Size carries corroboration and opacity carries severity, so a hazard several
/// riders have confirmed reads as more real than one dropped in passing, which
/// is how the routing penalty treats it. Road closures get their own colour:
/// they claim the road cannot be ridden at all.
struct HazardBadge: View {
    let hazard: HazardReport
    let kind: HazardKindInfo?

    static let hazardColor = Color(red: 0.76, green: 0.25, blue: 0.05)   // #c2410c, as on the web
    static let closureColor = Color(red: 0.60, green: 0.11, blue: 0.11)  // #991b1b

    private var size: CGFloat { 26 + CGFloat(min(hazard.confirmations, 4)) * 3 }

    var body: some View {
        let weight = kind?.weight ?? 0.3
        Image(systemName: HazardIcon.symbol(for: hazard.kind))
            .font(.system(size: size * 0.5, weight: .semibold))
            .foregroundStyle(.white)
            .frame(width: size, height: size)
            .background((kind?.closure == true ? Self.closureColor : Self.hazardColor)
                .opacity(0.6 + 0.4 * weight), in: Circle())
            .overlay(Circle().stroke(.white, lineWidth: 1.5))
            .shadow(color: .black.opacity(0.25), radius: 2, y: 1)
            .accessibilityElement()
            .accessibilityLabel(kind?.label ?? hazard.kind)
            .accessibilityIdentifier("hazard-\(hazard.id)")
    }
}
