import MapKit
import SwiftUI

struct ContentView: View {
    @State private var position: MapCameraPosition = .region(.austin)

    var body: some View {
        Map(position: $position)
            .ignoresSafeArea()
    }
}

extension MKCoordinateRegion {
    /// v1 coverage area is Austin, TX.
    static let austin = MKCoordinateRegion(
        center: CLLocationCoordinate2D(latitude: 30.2672, longitude: -97.7431),
        span: MKCoordinateSpan(latitudeDelta: 0.12, longitudeDelta: 0.12)
    )
}

#Preview {
    ContentView()
}
