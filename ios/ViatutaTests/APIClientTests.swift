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
