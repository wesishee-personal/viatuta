import MapKit
import SwiftUI

struct ContentView: View {
    @Environment(AuthStore.self) private var auth
    @State private var planner: RoutePlanner
    @State private var position: MapCameraPosition = .region(.austin)

    init(api: APIClient) {
        _planner = State(initialValue: RoutePlanner(api: api))
    }

    var body: some View {
        MapReader { proxy in
            Map(position: $position) {
                if let route = planner.route {
                    ForEach(route.drawableSegments) { segment in
                        MapPolyline(coordinates: segment.coordinates)
                            .stroke(Color.lts(segment.lts), style: StrokeStyle(lineWidth: 6, lineCap: .round, lineJoin: .round))
                    }
                }
                if let origin = planner.origin {
                    Marker("Start", coordinate: origin).tint(.green)
                }
                if let destination = planner.destination {
                    Marker("End", coordinate: destination).tint(.red)
                }
            }
            .onTapGesture { point in
                if let coordinate = proxy.convert(point, from: .local) {
                    planner.tap(coordinate)
                }
            }
        }
        .overlay(alignment: .top) {
            HStack {
                Spacer()
                Button("Sign out", action: auth.signOut).buttonStyle(.borderedProminent)
            }
            .padding()
        }
        .safeAreaInset(edge: .bottom) { RouteSummaryCard(planner: planner) }
        .onChange(of: planner.route == nil) { _, isNil in
            guard !isNil, let route = planner.route else { return }
            position = .rect(Self.fitRect(route.coordinates))
        }
    }

    /// The route's bounding box with breathing room, so it isn't flush with
    /// the screen edges or hidden behind the summary card.
    private static func fitRect(_ coordinates: [CLLocationCoordinate2D]) -> MKMapRect {
        let rect = MKPolyline(coordinates: coordinates, count: coordinates.count).boundingMapRect
        return rect.insetBy(dx: -rect.width * 0.3, dy: -rect.height * 0.4)
    }
}

extension MKCoordinateRegion {
    /// v1 coverage area is Austin, TX.
    static let austin = MKCoordinateRegion(
        center: CLLocationCoordinate2D(latitude: 30.2672, longitude: -97.7431),
        span: MKCoordinateSpan(latitudeDelta: 0.12, longitudeDelta: 0.12)
    )
}
