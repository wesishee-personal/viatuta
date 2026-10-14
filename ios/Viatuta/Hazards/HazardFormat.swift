import Foundation
import UIKit

/// How hazards are described to a rider. Pure functions, so they are easy to
/// test and shared by every screen that shows a hazard.
enum HazardFormat {
    /// A span of time for a rider: "30 minutes", "3 hours", "7 days". Hazard
    /// lifetimes run from an hour to months, so one unit does not fit them all.
    static func span(seconds: Int) -> String {
        let minutes = Int((Double(seconds) / 60).rounded())
        if minutes < 90 { return minutes == 1 ? "1 minute" : "\(minutes) minutes" }
        let hours = Int((Double(minutes) / 60).rounded())
        if hours < 48 { return hours == 1 ? "1 hour" : "\(hours) hours" }
        let days = Int((Double(hours) / 24).rounded())
        return days == 1 ? "1 day" : "\(days) days"
    }

    /// "expires in 2 hours". A hazard about to lapse is weaker evidence, and
    /// lifetimes can be a few hours, so this must not round to whole days.
    static func expiresIn(_ date: Date, now: Date = .now) -> String {
        let seconds = date.timeIntervalSince(now)
        if seconds < 60 { return "expiring now" }
        if seconds >= 86_400 && seconds < 2 * 86_400 { return "expires tomorrow" }
        return "expires in \(span(seconds: Int(seconds)))"
    }

    /// "3 min ago", "2 h ago", "yesterday".
    static func reportedAgo(_ date: Date, now: Date = .now) -> String {
        let minutes = Int((now.timeIntervalSince(date) / 60).rounded())
        if minutes < 1 { return "just now" }
        if minutes < 60 { return "\(minutes) min ago" }
        let hours = Int((Double(minutes) / 60).rounded())
        if hours < 24 { return "\(hours) h ago" }
        let days = Int((Double(hours) / 24).rounded())
        return days == 1 ? "yesterday" : "\(days) days ago"
    }

    private static let directions = [
        "north", "north-east", "east", "south-east",
        "south", "south-west", "west", "north-west",
    ]

    /// A compass heading in degrees as a direction word: 90 is "east".
    static func compass(_ degrees: Int) -> String {
        let index = Int((Double(((degrees % 360) + 360) % 360) / 45).rounded()) % 8
        return directions[index]
    }

    /// What to tell the rider when confirming, resolving or reporting fails.
    static func userMessage(for error: Error) -> String {
        guard let api = error as? APIError else { return "Couldn't send that. Try again in a moment." }
        if case .server(let status, let body) = api {
            switch status {
            case 401:
                return "Your session has expired. Sign in again."
            case 404:
                return "That hazard has already expired or been taken down."
            case 409, 429:
                // These carry a message written for the rider, e.g.
                // "you have already responded to this hazard".
                let message = body.message
                return message.prefix(1).uppercased() + message.dropFirst() + "."
            default:
                break
            }
        }
        return api.errorDescription ?? "Couldn't send that. Try again in a moment."
    }
}

/// SF Symbols for hazard kinds. The server owns the list of kinds, so a kind
/// this table has not heard of gets a generic warning icon instead of nothing.
enum HazardIcon {
    static let fallback = "exclamationmark.triangle.fill"

    static let symbols: [String: String] = [
        "accident": "exclamationmark.triangle.fill",
        "road_closed": "nosign",
        "construction": "hammer.fill",
        "flooding": "drop.fill",
        "poor_visibility": "sun.haze.fill",
        "aggressive_traffic": "car.fill",
        "signal_out": "bolt.slash.fill",
        "street_event": "party.popper.fill",
        "dangerous_junction": "exclamationmark.octagon.fill",
        "pothole": "circle.bottomhalf.filled",
        "glass": "sparkles",
        "debris": "trash.fill",
        "slippery_surface": "figure.fall",
        "blocked_lane": "bicycle",
        "fallen_tree": "tree.fill",
        "loose_animal": "dog.fill",
        "other": "questionmark.circle.fill",
    ]

    static func symbol(for kind: String) -> String {
        guard let name = symbols[kind], UIImage(systemName: name) != nil else { return fallback }
        return name
    }
}
