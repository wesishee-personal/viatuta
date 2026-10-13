import XCTest
import MapKit
@testable import Viatuta

final class ViatutaTests: XCTestCase {
    func testAustinRegionIsCentredOnAustin() {
        XCTAssertEqual(MKCoordinateRegion.austin.center.latitude, 30.2672, accuracy: 0.001)
    }
}
