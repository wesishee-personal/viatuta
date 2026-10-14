import SwiftUI

extension Color {
    /// Colours for traffic-stress levels, matching `LTS_COLORS` in the web app.
    static func lts(_ level: Int) -> Color {
        switch level {
        case 1: Color(red: 0x1a / 255, green: 0x8a / 255, blue: 0x4a / 255)
        case 2: Color(red: 0x8c / 255, green: 0xb9 / 255, blue: 0x3c / 255)
        case 3: Color(red: 0xe8 / 255, green: 0xa3 / 255, blue: 0x3d / 255)
        case 4: Color(red: 0xd2 / 255, green: 0x40 / 255, blue: 0x2f / 255)
        default: .gray
        }
    }
}
