import CoreLocation
import Foundation

struct LatLon: Codable, Equatable {
    let lat: Double
    let lon: Double

    init(_ coordinate: CLLocationCoordinate2D) {
        lat = coordinate.latitude
        lon = coordinate.longitude
    }
}

struct PlanRequest: Encodable {
    let waypoints: [LatLon]
    var profile: String?
}

struct GeoJSONLineString: Decodable {
    /// GeoJSON order: `[longitude, latitude]`.
    let coordinates: [[Double]]
}

/// A run of consecutive edges sharing a stress level, indexing into the
/// route geometry. `end` is inclusive. See `RouteSegment` in
/// `internal/httpapi/route_handler.go`.
struct RouteSegment: Decodable, Equatable {
    let start: Int
    let end: Int
    /// 1 (relaxed) to 4 (stressful); 0 means unclassified.
    let lts: Int
    let infra: String
    let lengthM: Double
}

struct PlanResponse: Decodable {
    let geometry: GeoJSONLineString
    let distanceM: Double
    let durationS: Int
    /// 0 (unbroken arterial) to 100 (fully protected).
    let safetyScore: Double
    let segments: [RouteSegment]
    let baselineDistanceM: Double
    let detourRatio: Double
    let warnings: [String]?

    var coordinates: [CLLocationCoordinate2D] {
        geometry.coordinates.compactMap { pair in
            pair.count >= 2 ? CLLocationCoordinate2D(latitude: pair[1], longitude: pair[0]) : nil
        }
    }

    /// The route split into drawable, individually-coloured polylines.
    /// Neighbouring segments share a junction coordinate, so they join up.
    var drawableSegments: [DrawableSegment] {
        let all = coordinates
        return segments.enumerated().compactMap { index, segment in
            guard segment.start >= 0, segment.end < all.count, segment.start < segment.end else {
                return nil
            }
            return DrawableSegment(id: index, lts: segment.lts,
                                   coordinates: Array(all[segment.start...segment.end]))
        }
    }
}

struct DrawableSegment: Identifiable {
    let id: Int
    let lts: Int
    let coordinates: [CLLocationCoordinate2D]
}
