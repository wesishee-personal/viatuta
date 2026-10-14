import Foundation

struct User: Codable, Equatable {
    let id: String
    let email: String
    let displayName: String?
    let createdAt: Date
}

struct AuthResponse: Codable {
    let token: String
    let expiresAt: Date
    let user: User
}

struct LoginRequest: Encodable {
    let email: String
    let password: String
}

struct RegisterRequest: Encodable {
    let email: String
    let password: String
    let displayName: String?
}

/// The server's single error envelope. See `internal/httpapi/errors.go`.
struct APIErrorBody: Decodable {
    let code: String
    let message: String
    let details: [String: String]?
}
