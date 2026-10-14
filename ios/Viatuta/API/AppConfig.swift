import Foundation

enum AppConfig {
    /// The simulator shares the Mac's network, so localhost reaches the dev
    /// API. A physical iPhone needs the Mac's LAN address instead (or a
    /// deployed host) — change it here.
    static let apiBaseURL = URL(string: "http://localhost:8080")!
}
