import XCTest
@testable import Viatuta

final class RouteModelsTests: XCTestCase {
    private let sample = """
    {"geometry":{"type":"LineString","coordinates":[[-97.70,30.20],[-97.71,30.21],[-97.72,30.22],[-97.73,30.23]]},
     "distance_m":3200.5,"duration_s":768,"cost":3500,"safety_score":81.4,
     "segments":[{"start":0,"end":1,"lts":1,"infra":"protected_track","length_m":1000},
                 {"start":1,"end":3,"lts":3,"infra":"none","length_m":2200.5},
                 {"start":2,"end":99,"lts":4,"infra":"none","length_m":5}],
     "baseline_distance_m":3000,"baseline_safety_score":40,"detour_ratio":1.07,
     "snapped":[],"edge_count":4,"compute_ms":12}
    """

    private func decode() throws -> PlanResponse {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(PlanResponse.self, from: Data(sample.utf8))
    }

    func testDecodesPlanResponse() throws {
        let route = try decode()
        XCTAssertEqual(route.distanceM, 3200.5)
        XCTAssertEqual(route.segments.count, 3)
        XCTAssertNil(route.warnings)
    }

    func testCoordinatesAreSwappedFromGeoJSONOrder() throws {
        let first = try XCTUnwrap(decode().coordinates.first)
        XCTAssertEqual(first.latitude, 30.20, accuracy: 1e-9)
        XCTAssertEqual(first.longitude, -97.70, accuracy: 1e-9)
    }

    func testDrawableSegmentsShareJunctionsAndSkipOutOfRange() throws {
        let drawable = try decode().drawableSegments
        XCTAssertEqual(drawable.count, 2, "the segment ending at index 99 is out of range")
        XCTAssertEqual(drawable[0].coordinates.count, 2)
        XCTAssertEqual(drawable[1].coordinates.count, 3)
        XCTAssertEqual(drawable[0].coordinates.last?.latitude, drawable[1].coordinates.first?.latitude)
    }
}
