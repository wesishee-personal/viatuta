import Foundation
import Security

/// Stores a single secret string in the iOS Keychain.
struct KeychainStore {
    var service = "app.viatuta.Viatuta"
    var account = "auth-token"

    private var baseQuery: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    func read() -> String? {
        var query = baseQuery
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    struct WriteError: LocalizedError {
        let status: OSStatus
        var errorDescription: String? {
            let detail = SecCopyErrorMessageString(status, nil) as String? ?? "unknown error"
            return "Couldn't save your sign-in securely (\(detail), code \(status))."
        }
    }

    func write(_ value: String) throws {
        delete()
        var query = baseQuery
        query[kSecValueData as String] = Data(value.utf8)
        // Readable after first unlock, never migrated to another device.
        query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(query as CFDictionary, nil)
        guard status == errSecSuccess else { throw WriteError(status: status) }
    }

    func delete() {
        SecItemDelete(baseQuery as CFDictionary)
    }
}
