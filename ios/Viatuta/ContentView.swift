import MapKit
import SwiftUI

/// A spot the rider has dropped to report a hazard at.
struct HazardPin: Identifiable {
    let id = UUID()
    let coordinate: CLLocationCoordinate2D
}

/// The one sheet the map screen can have up at a time.
enum ActiveSheet: Identifiable {
    case saved
    case reportHazard(HazardPin)
    case hazard(HazardReport)

    var id: String {
        switch self {
        case .saved: "saved"
        case .reportHazard(let pin): "report-\(pin.id)"
        case .hazard(let hazard): "hazard-\(hazard.id)"
        }
    }
}

struct ContentView: View {
    @Environment(AuthStore.self) private var auth
    @Environment(HazardService.self) private var hazardService
    @State private var planner: RoutePlanner
    @State private var position: MapCameraPosition = .region(.austin)
    @State private var sheet: ActiveSheet?
    @State private var showingSaveAlert = false
    @State private var routeName = ""

    /// While true, the next tap on the map places a hazard instead of a route
    /// waypoint.
    @State private var isPlacingHazard = false
    @State private var hazardPin: HazardPin?
    @State private var toast: String?

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

                ForEach(hazardService.hazards) { hazard in
                    Annotation("", coordinate: hazard.coordinate, anchor: .center) {
                        HazardBadge(hazard: hazard, kind: hazardService.kind(named: hazard.kind))
                            .onTapGesture {
                                // Tapping an existing hazard while reporting is
                                // most likely "this one is already there": show it.
                                isPlacingHazard = false
                                sheet = .hazard(hazard)
                            }
                    }
                }
                if let pin = hazardPin {
                    Marker("Hazard", systemImage: "exclamationmark.triangle.fill", coordinate: pin.coordinate)
                        .tint(HazardBadge.hazardColor)
                }
            }
            .onTapGesture { point in
                guard let coordinate = proxy.convert(point, from: .local) else { return }
                if isPlacingHazard {
                    isPlacingHazard = false
                    let pin = HazardPin(coordinate: coordinate)
                    hazardPin = pin
                    sheet = .reportHazard(pin)
                } else {
                    planner.tap(coordinate)
                }
            }
            .onMapCameraChange(frequency: .onEnd) { context in
                Task { await hazardService.refresh(in: context.region) }
            }
        }
        .overlay(alignment: .top) { topBar }
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
        .sheet(item: $sheet, onDismiss: { hazardPin = nil }) { active in
            switch active {
            case .saved:
                SavedRoutesView(api: auth.api) { planner.show($0) }
            case .reportHazard(let pin):
                ReportHazardSheet(coordinate: pin.coordinate) { outcome in
                    show(outcome.merged
                         ? "Someone already reported this here, so yours counted as a confirmation."
                         : "Reported. Routing already knows.")
                }
            case .hazard(let hazard):
                HazardDetailSheet(hazard: hazard)
            }
        }
        .task {
            await hazardService.loadKinds()
            // The camera-change callback covers later pans and zooms; this
            // covers the first screen, before the rider has touched the map.
            await hazardService.refresh(in: .austin)
        }
        .onChange(of: planner.route == nil) { _, isNil in
            if !isNil, let route = planner.route { fit(route.coordinates) }
        }
        .onChange(of: planner.savedRoute?.id) { _, id in
            if id != nil, let saved = planner.savedRoute { fit(saved.coordinates) }
        }
    }

    // MARK: Top bar

    private var topBar: some View {
        VStack(spacing: 8) {
            HStack {
                Button("Saved", systemImage: "bookmark") { sheet = .saved }
                    .buttonStyle(.borderedProminent)
                Button("Report", systemImage: "exclamationmark.triangle") {
                    isPlacingHazard.toggle()
                }
                .buttonStyle(.borderedProminent)
                .tint(isPlacingHazard ? .gray : HazardBadge.hazardColor)
                .accessibilityIdentifier("reportHazardButton")
                Spacer()
                Button("Sign out", action: auth.signOut).buttonStyle(.borderedProminent)
            }

            if isPlacingHazard {
                banner("Tap the map where the hazard is", id: "hazardPlacementBanner")
            } else if let toast {
                banner(toast, id: "hazardToast")
            }
        }
        .padding()
    }

    private func banner(_ text: String, id: String) -> some View {
        Text(text)
            .font(.footnote.weight(.medium))
            .multilineTextAlignment(.center)
            .padding(.horizontal, 14)
            .padding(.vertical, 8)
            .background(.regularMaterial, in: Capsule())
            .accessibilityIdentifier(id)
    }

    /// Shows a short message over the map, then clears it.
    private func show(_ message: String) {
        toast = message
        Task {
            try? await Task.sleep(for: .seconds(6))
            if toast == message { toast = nil }
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
