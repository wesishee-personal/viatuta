import XCTest
@testable import Viatuta

final class KeychainStoreTests: XCTestCase {
    func testWriteReadDeleteRoundTrip() throws {
        let store = KeychainStore(service: "app.viatuta.tests", account: UUID().uuidString)
        defer { store.delete() }

        XCTAssertNil(store.read())
        try store.write("secret-1")
        XCTAssertEqual(store.read(), "secret-1")
        try store.write("secret-2")
        XCTAssertEqual(store.read(), "secret-2", "writing again replaces the value")
        store.delete()
        XCTAssertNil(store.read())
    }
}
