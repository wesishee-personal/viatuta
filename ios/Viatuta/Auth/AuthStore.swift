import Foundation
import Observation

/// App-wide authentication state.
@MainActor
@Observable
final class AuthStore {
    enum State: Equatable {
        case restoring
        case signedOut
        case signedIn(User)
    }

    private(set) var state: State = .restoring
    private(set) var isWorking = false
    var errorMessage: String?

    private let keychain: KeychainStore
    private(set) var api: APIClient

    init(baseURL: URL = AppConfig.apiBaseURL, keychain: KeychainStore = KeychainStore()) {
        self.keychain = keychain
        var api = APIClient(baseURL: baseURL)
        api.tokenProvider = { keychain.read() }
        self.api = api
    }

    /// On launch, validate any stored token against the server.
    func restore() async {
        guard keychain.read() != nil else {
            state = .signedOut
            return
        }
        do {
            state = .signedIn(try await api.me())
        } catch let error as APIError where error.isUnauthorized {
            // Expired or revoked: drop it.
            keychain.delete()
            state = .signedOut
        } catch {
            // Offline etc. — keep the token and let the user retry.
            state = .signedOut
            errorMessage = error.localizedDescription
        }
    }

    func login(email: String, password: String) async {
        await authenticate { try await self.api.login(email: email, password: password) }
    }

    func register(email: String, password: String, displayName: String) async {
        let name = displayName.trimmingCharacters(in: .whitespaces)
        await authenticate {
            try await self.api.register(email: email, password: password,
                                        displayName: name.isEmpty ? nil : name)
        }
    }

    func signOut() {
        keychain.delete()
        state = .signedOut
    }

    private func authenticate(_ call: () async throws -> AuthResponse) async {
        isWorking = true
        errorMessage = nil
        defer { isWorking = false }
        do {
            let response = try await call()
            try keychain.write(response.token)
            state = .signedIn(response.user)
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}
