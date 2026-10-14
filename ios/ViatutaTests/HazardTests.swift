import MapKit
import XCTest
@testable import Viatuta

private let kindsJSON = """
{"kinds":[
 {"kind":"accident","label":"Accident","ttl_s":10800,"confirm_extension_s":3600,"max_life_s":28800,
  "merge_radius_m":50,"directional":true,"weight":0.9,"closure":false},
 {"kind":"road_closed","label":"Road closed","ttl_s":86400,"confirm_extension_s":43200,"max_life_s":604800,
  "merge_radius_m":50,"directional":false,"weight":1,"closure":true}]}
"""

private func hazardJSON(id: Int = 7, kind: String = "pothole", heading: Int? = 90,
                        expiresIn: TimeInterval = 3 * 3600) -> String {
    let iso = ISO8601DateFormatter()
    iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    let headingField = heading.map { #","heading_deg":\#($0)"# } ?? ""
    return """
    {"id":\(id),"location":{"lat":30.27,"lon":-97.74},"kind":"\(kind)"\(headingField),
     "confirmations":2,"created_at":"\(iso.string(from: Date()))",
     "expires_at":"\(iso.string(from: Date().addingTimeInterval(expiresIn)))"}
    """
}

private func makeClient() -> APIClient {
    let config = URLSessionConfiguration.ephemeral
    config.protocolClasses = [StubProtocol.self]
    var client = APIClient(baseURL: URL(string: "http://test")!, session: URLSession(configuration: config))
    client.tokenProvider = { "tok" }
    return client
}

final class HazardDecodingTests: XCTestCase {
    func testKindsDecodeSnakeCase() async throws {
        StubProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/v1/hazards/kinds")
            return (200, Data(kindsJSON.utf8))
        }
        let kinds = try await makeClient().hazardKinds()
        XCTAssertEqual(kinds.map(\.kind), ["accident", "road_closed"])
        XCTAssertEqual(kinds[0].ttlS, 10800)
        XCTAssertTrue(kinds[0].directional)
        XCTAssertTrue(kinds[1].closure)
        XCTAssertEqual(kinds[1].weight, 1)
    }

    func testListSendsBboxAsAQueryNotAnEscapedPath() async throws {
        StubProtocol.handler = { request in
            // appending(path:) would turn "?" into "%3F" and the server would
            // see a path with no query at all.
            XCTAssertEqual(request.url?.path, "/v1/hazards")
            XCTAssertEqual(request.url?.query, "bbox=-97.80000,30.20000,-97.70000,30.30000")
            return (200, Data(#"{"hazards":[\#(hazardJSON())],"count":1}"#.utf8))
        }
        let list = try await makeClient().hazards(minLon: -97.8, minLat: 30.2, maxLon: -97.7, maxLat: 30.3)
        XCTAssertEqual(list.count, 1)
        XCTAssertEqual(list[0].headingDeg, 90)
        XCTAssertEqual(list[0].coordinate.latitude, 30.27, accuracy: 1e-9)
    }

    func testHeadingIsAbsentWhenTheServerOmitsIt() async throws {
        StubProtocol.handler = { _ in (200, Data(#"{"hazards":[\#(hazardJSON(heading: nil))]}"#.utf8)) }
        let list = try await makeClient().hazards(minLon: 0, minLat: 0, maxLon: 1, maxLat: 1)
        XCTAssertNil(list[0].headingDeg)
    }

    func testReportMergedFlagFollowsTheStatus() async throws {
        let submission = HazardSubmission(location: LatLon(.init(latitude: 30.27, longitude: -97.74)), kind: "pothole")
        for (status, merged) in [(201, false), (200, true)] {
            StubProtocol.handler = { _ in (status, Data(hazardJSON().utf8)) }
            let result = try await makeClient().reportHazard(submission)
            XCTAssertEqual(result.merged, merged, "status \(status)")
        }
    }

    func testSubmissionBodyUsesServerFieldNamesAndOmitsEmptyOptionals() async throws {
        var bodies: [[String: Any]] = []
        StubProtocol.handler = { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer tok")
            bodies.append(try! JSONSerialization.jsonObject(with: request.bodyData()) as! [String: Any])
            return (201, Data(hazardJSON().utf8))
        }
        let client = makeClient()
        let point = LatLon(.init(latitude: 30.27, longitude: -97.74))

        _ = try await client.reportHazard(HazardSubmission(location: point, kind: "pothole"))
        XCTAssertEqual(Set(bodies[0].keys), ["location", "kind"],
                       "the server decodes strictly: no nulls and no unknown keys")

        _ = try await client.reportHazard(HazardSubmission(location: point, kind: "glass", notes: "across the lane", headingDeg: 271.5))
        XCTAssertEqual(Set(bodies[1].keys), ["location", "kind", "notes", "heading_deg"])
        XCTAssertEqual(bodies[1]["heading_deg"] as? Double, 271.5)
    }
}

@MainActor
final class HazardServiceTests: XCTestCase {
    private func region() -> MKCoordinateRegion {
        MKCoordinateRegion(center: .init(latitude: 30.27, longitude: -97.74),
                           span: .init(latitudeDelta: 0.1, longitudeDelta: 0.2))
    }

    func testKindsLoadSuccessAndFailure() async {
        let service = HazardService(api: makeClient())
        XCTAssertEqual(service.kindsState, .loading)

        StubProtocol.handler = { _ in (200, Data(kindsJSON.utf8)) }
        await service.loadKinds()
        XCTAssertEqual(service.kindsState, .ready)
        XCTAssertEqual(service.kind(named: "road_closed")?.label, "Road closed")
        XCTAssertNil(service.kind(named: "meteor"))

        StubProtocol.handler = { _ in (500, Data(#"{"code":"internal_error","message":"boom"}"#.utf8)) }
        await service.loadKinds()
        XCTAssertEqual(service.kindsState, .failed)
    }

    func testRefreshKeepsExistingHazardsWhenTheRequestFails() async {
        let service = HazardService(api: makeClient())
        StubProtocol.handler = { _ in (200, Data(#"{"hazards":[\#(hazardJSON())]}"#.utf8)) }
        await service.refresh(in: region())
        XCTAssertEqual(service.hazards.count, 1)

        StubProtocol.handler = { _ in (500, Data(#"{"code":"internal_error","message":"boom"}"#.utf8)) }
        await service.refresh(in: region())
        XCTAssertEqual(service.hazards.count, 1, "a failed refresh must not blank the map")
    }

    func testReportRefreshesTheMap() async throws {
        let service = HazardService(api: makeClient())
        StubProtocol.handler = { _ in (200, Data(#"{"hazards":[]}"#.utf8)) }
        await service.refresh(in: region())
        XCTAssertTrue(service.hazards.isEmpty)

        StubProtocol.handler = { request in
            if request.httpMethod == "POST" { return (201, Data(hazardJSON(id: 9).utf8)) }
            return (200, Data(#"{"hazards":[\#(hazardJSON(id: 9))]}"#.utf8))
        }
        let outcome = try await service.report(kind: "pothole", at: .init(latitude: 30.27, longitude: -97.74),
                                               notes: "   ")
        XCTAssertFalse(outcome.merged)
        XCTAssertEqual(service.hazards.map(\.id), [9], "the new hazard should be on the map straight away")
    }

    func testBlankNotesAreNotSent() async throws {
        var body: [String: Any] = [:]
        StubProtocol.handler = { request in
            if request.httpMethod == "POST" { body = try! JSONSerialization.jsonObject(with: request.bodyData()) as! [String: Any] }
            return (201, Data(hazardJSON().utf8))
        }
        _ = try await HazardService(api: makeClient())
            .report(kind: "glass", at: .init(latitude: 30.27, longitude: -97.74), notes: "  \n ")
        XCTAssertNil(body["notes"])
    }

    func testResolveSaysWhetherTheHazardCameDown() async throws {
        let service = HazardService(api: makeClient())
        let hazard = try JSONDecoder.test.decode(HazardReport.self, from: Data(hazardJSON().utf8))

        StubProtocol.handler = { request in
            request.httpMethod == "POST"
                ? (200, Data(hazardJSON(expiresIn: 3600).utf8))     // still active: one vote so far
                : (200, Data(#"{"hazards":[]}"#.utf8))
        }
        let stillUp = try await service.resolve(hazard)
        XCTAssertFalse(stillUp.tookDown)

        StubProtocol.handler = { request in
            request.httpMethod == "POST"
                ? (200, Data(hazardJSON(expiresIn: -5).utf8))       // expired: it came down
                : (200, Data(#"{"hazards":[]}"#.utf8))
        }
        let down = try await service.resolve(hazard)
        XCTAssertTrue(down.tookDown)
    }

    func testPaddedBoundsGrowTheRegionByAQuarterEachSide() {
        let box = HazardService.paddedBounds(of: region())
        XCTAssertEqual(box.minLat, 30.27 - 0.1 * 0.75, accuracy: 1e-9)
        XCTAssertEqual(box.maxLon, -97.74 + 0.2 * 0.75, accuracy: 1e-9)
    }
}

final class HazardFormatTests: XCTestCase {
    func testSpan() {
        XCTAssertEqual(HazardFormat.span(seconds: 30 * 60), "30 minutes")
        XCTAssertEqual(HazardFormat.span(seconds: 3600), "60 minutes")
        XCTAssertEqual(HazardFormat.span(seconds: 3 * 3600), "3 hours")
        XCTAssertEqual(HazardFormat.span(seconds: 24 * 3600), "24 hours")
        XCTAssertEqual(HazardFormat.span(seconds: 7 * 86_400), "7 days")
        XCTAssertEqual(HazardFormat.span(seconds: 180 * 86_400), "180 days")
    }

    // Lifetimes are measured in hours now. Rounding to whole days would make
    // a three-hour accident read "expiring now".
    func testExpiresInReadsInHours() {
        let now = Date()
        XCTAssertEqual(HazardFormat.expiresIn(now.addingTimeInterval(3 * 3600), now: now), "expires in 3 hours")
        XCTAssertEqual(HazardFormat.expiresIn(now.addingTimeInterval(40 * 60), now: now), "expires in 40 minutes")
        XCTAssertEqual(HazardFormat.expiresIn(now.addingTimeInterval(30), now: now), "expiring now")
        XCTAssertEqual(HazardFormat.expiresIn(now.addingTimeInterval(-60), now: now), "expiring now")
        XCTAssertEqual(HazardFormat.expiresIn(now.addingTimeInterval(30 * 3600), now: now), "expires tomorrow")
        XCTAssertEqual(HazardFormat.expiresIn(now.addingTimeInterval(5 * 86_400), now: now), "expires in 5 days")
    }

    func testReportedAgo() {
        let now = Date()
        XCTAssertEqual(HazardFormat.reportedAgo(now.addingTimeInterval(-10), now: now), "just now")
        XCTAssertEqual(HazardFormat.reportedAgo(now.addingTimeInterval(-5 * 60), now: now), "5 min ago")
        XCTAssertEqual(HazardFormat.reportedAgo(now.addingTimeInterval(-3 * 3600), now: now), "3 h ago")
        XCTAssertEqual(HazardFormat.reportedAgo(now.addingTimeInterval(-86_400), now: now), "yesterday")
    }

    func testCompassWrapsThroughNorth() {
        XCTAssertEqual(HazardFormat.compass(0), "north")
        XCTAssertEqual(HazardFormat.compass(90), "east")
        XCTAssertEqual(HazardFormat.compass(200), "south")
        XCTAssertEqual(HazardFormat.compass(271), "west")
        XCTAssertEqual(HazardFormat.compass(359), "north")
        XCTAssertEqual(HazardFormat.compass(-45), "north-west")
    }

    func testUserMessagesForServerErrors() {
        func err(_ status: Int, _ message: String) -> Error {
            APIError.server(status: status, body: APIErrorBody(code: "x", message: message, details: nil))
        }
        XCTAssertEqual(HazardFormat.userMessage(for: err(404, "no active hazard with that id")),
                       "That hazard has already expired or been taken down.")
        XCTAssertEqual(HazardFormat.userMessage(for: err(409, "you reported this hazard")),
                       "You reported this hazard.")
        XCTAssertEqual(HazardFormat.userMessage(for: err(429, "too many hazard reports; try again in 3 minutes")),
                       "Too many hazard reports; try again in 3 minutes.")
        XCTAssertEqual(HazardFormat.userMessage(for: err(401, "a bearer token is required")),
                       "Your session has expired. Sign in again.")
        XCTAssertEqual(HazardFormat.userMessage(for: URLError(.badServerResponse)),
                       "Couldn't send that. Try again in a moment.")
    }
}

final class HazardIconTests: XCTestCase {
    func testEveryMappedSymbolExists() {
        for (kind, name) in HazardIcon.symbols {
            XCTAssertNotNil(UIImage(systemName: name), "\(kind): SF Symbol \(name) does not exist")
        }
    }

    func testUnknownKindsGetTheFallbackIcon() {
        XCTAssertEqual(HazardIcon.symbol(for: "meteor_strike"), HazardIcon.fallback)
        XCTAssertNotNil(UIImage(systemName: HazardIcon.fallback))
    }

    // The server owns the kinds. If it gains one, the app should notice that
    // it has no icon for it rather than silently showing the generic one.
    func testEveryKindTheServerHasAtLeastTodayHasAnIcon() {
        let known = ["accident", "road_closed", "construction", "flooding", "poor_visibility",
                     "aggressive_traffic", "signal_out", "street_event", "dangerous_junction",
                     "pothole", "glass", "debris", "slippery_surface", "blocked_lane",
                     "fallen_tree", "loose_animal", "other"]
        for kind in known { XCTAssertNotNil(HazardIcon.symbols[kind], "\(kind) has no icon") }
    }
}

private extension JSONDecoder {
    static var test: JSONDecoder {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let string = try decoder.singleValueContainer().decode(String.self)
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            guard let date = f.date(from: string) else {
                throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: string))
            }
            return date
        }
        return d
    }
}
