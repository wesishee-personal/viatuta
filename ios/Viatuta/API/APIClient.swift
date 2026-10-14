import CoreLocation
import Foundation

enum APIError: LocalizedError {
    case server(status: Int, body: APIErrorBody)
    case unexpectedStatus(Int)
    case transport(Error)

    var errorDescription: String? {
        switch self {
        case .server(_, let body):
            // Prefer the field-level reason (e.g. weak password) when present.
            return body.details?.values.first ?? body.message
        case .unexpectedStatus(let status):
            return "Unexpected response from server (\(status))."
        case .transport:
            return "Couldn't reach the server. Check your connection."
        }
    }

    var isUnauthorized: Bool {
        if case .server(let status, _) = self { return status == 401 }
        return false
    }
}

/// Thin JSON client for the Viatuta API.
struct APIClient {
    var baseURL: URL
    var session: URLSession = .shared
    /// Supplies the bearer token for authenticated calls.
    var tokenProvider: () -> String? = { nil }

    private static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        return e
    }()

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let string = try decoder.singleValueContainer().decode(String.self)
            // Go emits RFC 3339, with fractional seconds only when non-zero.
            let withFraction = ISO8601DateFormatter()
            withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            let plain = ISO8601DateFormatter()
            if let date = withFraction.date(from: string) ?? plain.date(from: string) {
                return date
            }
            throw DecodingError.dataCorrupted(.init(
                codingPath: decoder.codingPath, debugDescription: "Bad date: \(string)"))
        }
        return d
    }()

    func send<Response: Decodable>(
        _ method: String,
        _ path: String,
        body: (some Encodable)? = Optional<String>.none,
        authenticated: Bool = false
    ) async throws -> Response {
        let (data, _) = try await perform(method, path, body: body, authenticated: authenticated)
        return try Self.decoder.decode(Response.self, from: data)
    }

    /// Like `send`, but also returns the HTTP status, for the few endpoints
    /// where 200 and 201 mean different things.
    func sendWithStatus<Response: Decodable>(
        _ method: String,
        _ path: String,
        body: (some Encodable)? = Optional<String>.none,
        authenticated: Bool = false
    ) async throws -> (response: Response, status: Int) {
        let (data, status) = try await perform(method, path, body: body, authenticated: authenticated)
        return (try Self.decoder.decode(Response.self, from: data), status)
    }

    /// For endpoints that answer 204 No Content.
    func sendNoContent(_ method: String, _ path: String, authenticated: Bool = false) async throws {
        _ = try await perform(method, path, body: Optional<String>.none, authenticated: authenticated)
    }

    /// `path` may carry a query ("v1/hazards?bbox=..."). `appending(path:)`
    /// would percent-encode the "?", so the two are joined separately.
    private func url(for path: String) -> URL {
        let parts = path.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
        let base = baseURL.appending(path: String(parts[0]))
        guard parts.count == 2,
              var components = URLComponents(url: base, resolvingAgainstBaseURL: false) else { return base }
        components.percentEncodedQuery = String(parts[1])
        return components.url ?? base
    }

    private func perform(
        _ method: String,
        _ path: String,
        body: (some Encodable)?,
        authenticated: Bool
    ) async throws -> (Data, Int) {
        var request = URLRequest(url: url(for: path))
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try Self.encoder.encode(body)
        }
        if authenticated, let token = tokenProvider() {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw APIError.transport(error)
        }
        guard let http = response as? HTTPURLResponse else {
            throw APIError.unexpectedStatus(-1)
        }
        guard (200..<300).contains(http.statusCode) else {
            if let body = try? Self.decoder.decode(APIErrorBody.self, from: data) {
                throw APIError.server(status: http.statusCode, body: body)
            }
            throw APIError.unexpectedStatus(http.statusCode)
        }
        return (data, http.statusCode)
    }

    // MARK: Auth

    func login(email: String, password: String) async throws -> AuthResponse {
        try await send("POST", "v1/auth/login", body: LoginRequest(email: email, password: password))
    }

    func register(email: String, password: String, displayName: String?) async throws -> AuthResponse {
        try await send("POST", "v1/auth/register",
                       body: RegisterRequest(email: email, password: password, displayName: displayName))
    }

    func me() async throws -> User {
        try await send("GET", "v1/auth/me", authenticated: true)
    }

    // MARK: Routing

    func planRoute(from origin: CLLocationCoordinate2D, to destination: CLLocationCoordinate2D) async throws -> PlanResponse {
        try await send("POST", "v1/route/plan",
                       body: PlanRequest(waypoints: [LatLon(origin), LatLon(destination)]))
    }

    // MARK: Saved routes

    func saveRoute(name: String, from origin: CLLocationCoordinate2D, to destination: CLLocationCoordinate2D) async throws -> SavedRoute {
        try await send("POST", "v1/routes",
                       body: SaveRouteRequest(name: name, waypoints: [LatLon(origin), LatLon(destination)]),
                       authenticated: true)
    }

    func savedRoutes() async throws -> [SavedRoute] {
        let response: SavedRoutesResponse = try await send("GET", "v1/routes", authenticated: true)
        return response.routes
    }

    func savedRoute(id: String) async throws -> SavedRouteDetail {
        try await send("GET", "v1/routes/\(id)", authenticated: true)
    }

    func deleteSavedRoute(id: String) async throws {
        try await sendNoContent("DELETE", "v1/routes/\(id)", authenticated: true)
    }

    // MARK: Hazards

    func hazardKinds() async throws -> [HazardKindInfo] {
        let response: HazardKindsResponse = try await send("GET", "v1/hazards/kinds")
        return response.kinds
    }

    /// Active hazards inside a bounding box. Public: no sign-in needed.
    func hazards(minLon: Double, minLat: Double, maxLon: Double, maxLat: Double) async throws -> [HazardReport] {
        let bbox = [minLon, minLat, maxLon, maxLat].map { String(format: "%.5f", $0) }.joined(separator: ",")
        let response: HazardListResponse = try await send("GET", "v1/hazards?bbox=\(bbox)")
        return response.hazards
    }

    /// `merged` is true when the server folded the report into a hazard
    /// already on the map (HTTP 200) instead of creating one (201).
    func reportHazard(_ submission: HazardSubmission) async throws -> (hazard: HazardReport, merged: Bool) {
        let result: (response: HazardReport, status: Int) =
            try await sendWithStatus("POST", "v1/hazards", body: submission, authenticated: true)
        return (result.response, result.status == 200)
    }

    func confirmHazard(id: Int) async throws -> HazardReport {
        try await send("POST", "v1/hazards/\(id)/confirm", authenticated: true)
    }

    func resolveHazard(id: Int) async throws -> HazardReport {
        try await send("POST", "v1/hazards/\(id)/resolve", authenticated: true)
    }
}
