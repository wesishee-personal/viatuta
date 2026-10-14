import CoreLocation
import Foundation

/// One entry of `GET /v1/hazards/kinds`. The server owns the list of kinds and
/// each one's lifetime, so the app reads it instead of hard-coding it and a
/// new kind needs no app release.
struct HazardKindInfo: Decodable, Identifiable, Equatable {
    let kind: String
    let label: String
    /// How long a fresh report lives, in seconds.
    let ttlS: Int
    let confirmExtensionS: Int
    let maxLifeS: Int
    let mergeRadiusM: Double
    /// Whether a report's heading limits it to one direction of travel.
    let directional: Bool
    /// Routing influence, 0 to 1; used to draw serious hazards more boldly.
    let weight: Double
    /// The road cannot be ridden, which routing treats far more steeply.
    let closure: Bool

    var id: String { kind }
}

struct HazardKindsResponse: Decodable {
    let kinds: [HazardKindInfo]
}

struct HazardReport: Decodable, Identifiable, Equatable {
    let id: Int
    let location: LatLon
    let kind: String
    let notes: String?
    /// Direction of travel the hazard affects, degrees clockwise from north.
    /// Nil means both directions.
    let headingDeg: Int?
    let confirmations: Int
    let createdAt: Date
    let expiresAt: Date

    var coordinate: CLLocationCoordinate2D { location.coordinate }
}

struct HazardListResponse: Decodable {
    let hazards: [HazardReport]
}

/// The body of `POST /v1/hazards`. The server decodes strictly, so this holds
/// exactly the fields it accepts and nothing else.
struct HazardSubmission: Encodable {
    let location: LatLon
    let kind: String
    var notes: String?
    /// Direction of travel when reported during navigation. Planning-screen
    /// reports leave it nil, which the server reads as "both directions".
    var headingDeg: Double?
}
