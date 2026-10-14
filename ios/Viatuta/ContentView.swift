import MapKit
import SwiftUI

struct ContentView: View {
    @Environment(AuthStore.self) private var auth
    @State private var planner: RoutePlanner
    @State private var position: MapCameraPosition = .region(.austin)
    @State private var showingSaved = false
    @State private var showingSaveAlert = false
    @State private var routeName = ""

    init(api: APIClient) {
        _planner = State(initialValue: RoutePlanner(api: api))
    }

    var body: some View {
        MapReader { proxy in
            Map(position: $position) {
                if let route = planner.route {
                    ForEach(route.drawableSegments) { segment in
                        MapPolyline(coordinates: segment.coordinates)
                            .stroke(Color.lts(segment.lts), style: Self.routeStyle)
                    }
                }
                if let saved = planner.savedRoute {
                    // Saved routes carry no per-segment stress data, so draw
                    // them in a single colour.
                    MapPolyline(coordinates: saved.coordinates)
                        .stroke(.blue, style: Self.routeStyle)
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
                Button("Saved", systemImage: "bookmark") { showingSaved = true }
                    .buttonStyle(.borderedProminent)
                Spacer()
                Button("Sign out", action: auth.signOut).buttonStyle(.borderedProminent)
            }
            .padding()
        }
        .safeAreaInset(edge: .bottom) {
            RouteSummaryCard(planner: planner) {
                routeName = ""
                showingSaveAlert = true
            }
        }
        .alert("Save route", isPresented: $showingSaveAlert) {
            TextField("Name", text: $routeName)
            Button("Save") {
                let name = routeName
                Task { await planner.save(name: name) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Give this route a name, like “Commute”.")
        }
        .sheet(isPresented: $showingSaved) {
            SavedRoutesView(api: auth.api) { planner.show($0) }
        }
        .onChange(of: planner.route == nil) { _, isNil in
            if !isNil, let route = planner.route { fit(route.coordinates) }
        }
        .onChange(of: planner.savedRoute?.id) { _, id in
            if id != nil, let saved = planner.savedRoute { fit(saved.coordinates) }
        }
    }

    private static let routeStyle = StrokeStyle(lineWidth: 6, lineCap: .round, lineJoin: .round)

    /// Frames the route with breathing room, so it isn't flush with the
    /// screen edges or hidden behind the summary card.
    private func fit(_ coordinates: [CLLocationCoordinate2D]) {
        guard coordinates.count > 1 else { return }
        let rect = MKPolyline(coordinates: coordinates, count: coordinates.count).boundingMapRect
        position = .rect(rect.insetBy(dx: -rect.width * 0.3, dy: -rect.height * 0.4))
    }
}

extension MKCoordinateRegion {
    /// v1 coverage area is Austin, TX.
    static let austin = MKCoordinateRegion(
        center: CLLocationCoordinate2D(latitude: 30.2672, longitude: -97.7431),
        span: MKCoordinateSpan(latitudeDelta: 0.12, longitudeDelta: 0.12)
    )
}
