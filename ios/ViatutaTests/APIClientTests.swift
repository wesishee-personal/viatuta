import XCTest
@testable import Viatuta

/// Intercepts URLSession traffic so tests never touch the network.
final class StubProtocol: URLProtocol {
    nonisolated(unsafe) static var handler: ((URLRequest) -> (Int, Data))?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let (status, data) = Self.handler!(request)
        let response = HTTPURLResponse(url: request.url!, statusCode: status,
                                       httpVersion: nil, headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

final class APIClientTests: XCTestCase {
    private func makeClient() -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubProtocol.self]
        return APIClient(baseURL: URL(string: "http://test")!, session: URLSession(configuration: config))
    }

    func testLoginDecodesSnakeCaseResponse() async throws {
        StubProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/v1/auth/login")
            let json = """
            {"token":"abc","expires_at":"2026-10-14T12:00:00.5Z",
             "user":{"id":"u1","email":"a@b.co","display_name":"Ann","created_at":"2026-10-13T12:00:00Z"}}
            """
            return (200, Data(json.utf8))
        }
        let response = try await makeClient().login(email: "a@b.co", password: "pw")
        XCTAssertEqual(response.token, "abc")
        XCTAssertEqual(response.user.displayName, "Ann")
    }

    func testServerErrorEnvelopeSurfacesMessage() async {
        StubProtocol.handler = { _ in
            (401, Data(#"{"code":"unauthorized","message":"invalid email or password"}"#.utf8))
        }
        do {
            _ = try await makeClient().login(email: "a@b.co", password: "bad")
            XCTFail("expected error")
        } catch let error as APIError {
            XCTAssertTrue(error.isUnauthorized)
            XCTAssertEqual(error.errorDescription, "invalid email or password")
        } catch {
            XCTFail("wrong error \(error)")
        }
    }
}

final class SavedRouteAPITests: XCTestCase {
    private func makeClient(token: String? = "tok") -> APIClient {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubProtocol.self]
        var client = APIClient(baseURL: URL(string: "http://test")!, session: URLSession(configuration: config))
        client.tokenProvider = { token }
        return client
    }

    func testSaveRouteSendsEndpointsWithBearerToken() async throws {
        StubProtocol.handler = { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.url?.path, "/v1/routes")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer tok")
            let body = try! JSONSerialization.jsonObject(with: request.bodyData()) as! [String: Any]
            XCTAssertEqual(body["name"] as? String, "Commute")
            let points = body["waypoints"] as! [[String: Double]]
            XCTAssertEqual(points.count, 2)
            XCTAssertEqual(points[0]["lat"], 30.1)
            XCTAssertEqual(points[1]["lon"], -97.2)
            let json = """
            {"id":"r1","name":"Commute","waypoints":[],"distance_m":4200.5,"duration_s":1008,
             "safety_score":77.2,"profile":{},"created_at":"2026-10-14T01:02:03.123456Z"}
            """
            return (201, Data(json.utf8))
        }
        let saved = try await makeClient().saveRoute(
            name: "Commute",
            from: .init(latitude: 30.1, longitude: -97.1),
            to: .init(latitude: 30.2, longitude: -97.2))
        XCTAssertEqual(saved.id, "r1")
        XCTAssertEqual(saved.safetyScore, 77.2)
    }

    func testListDecodesRoutesWithMissingOptionalFields() async throws {
        StubProtocol.handler = { _ in
            let json = """
            {"count":1,"routes":[{"id":"r1","name":"A","waypoints":[],"distance_m":10,
              "profile":{},"created_at":"2026-10-14T01:02:03Z"}]}
            """
            return (200, Data(json.utf8))
        }
        let routes = try await makeClient().savedRoutes()
        XCTAssertEqual(routes.count, 1)
        XCTAssertNil(routes[0].durationS)
        XCTAssertNil(routes[0].safetyScore)
    }

    func testDetailDecodesGeometryAndWaypoints() async throws {
        StubProtocol.handler = { _ in
            let json = """
            {"id":"r1","name":"A","waypoints":[{"lat":30.1,"lon":-97.1},{"lat":30.2,"lon":-97.2}],
             "distance_m":10,"duration_s":5,"safety_score":50,"profile":{},"created_at":"2026-10-14T01:02:03Z",
             "geometry":{"type":"LineString","coordinates":[[-97.1,30.1],[-97.2,30.2]]}}
            """
            return (200, Data(json.utf8))
        }
        let detail = try await makeClient().savedRoute(id: "r1")
        XCTAssertEqual(detail.waypoints.last?.coordinate.longitude, -97.2)
        XCTAssertEqual(detail.coordinates.first?.latitude, 30.1)
    }

    func testDeleteAcceptsEmpty204() async throws {
        StubProtocol.handler = { request in
            XCTAssertEqual(request.httpMethod, "DELETE")
            XCTAssertEqual(request.url?.path, "/v1/routes/r1")
            return (204, Data())
        }
        try await makeClient().deleteSavedRoute(id: "r1")
    }

    func testDeleteOfMissingRouteThrowsNotFound() async {
        StubProtocol.handler = { _ in
            (404, Data(#"{"code":"not_found","message":"route not found"}"#.utf8))
        }
        do {
            try await makeClient().deleteSavedRoute(id: "nope")
            XCTFail("expected error")
        } catch let error as APIError {
            XCTAssertEqual(error.errorDescription, "route not found")
        } catch {
            XCTFail("wrong error \(error)")
        }
    }
}

private extension URLRequest {
    /// URLProtocol sees bodies as a stream, not `httpBody`.
    func bodyData() -> Data {
        if let httpBody { return httpBody }
        guard let stream = httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }
}
