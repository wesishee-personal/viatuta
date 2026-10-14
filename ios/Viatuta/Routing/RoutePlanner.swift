import CoreLocation
import Observation

/// Holds the origin/destination the rider has picked and the route planned
/// between them — or a previously saved route being viewed.
@MainActor
@Observable
final class RoutePlanner {
    private(set) var origin: CLLocationCoordinate2D?
    private(set) var destination: CLLocationCoordinate2D?
    private(set) var route: PlanResponse?
    private(set) var savedRoute: SavedRouteDetail?
    private(set) var isPlanning = false
    private(set) var isSaving = false
    private(set) var errorMessage: String?
    /// Brief confirmation after a save, shown on the summary card.
    private(set) var notice: String?

    private let api: APIClient
    private var planTask: Task<Void, Never>?

    init(api: APIClient) {
        self.api = api
    }

    /// First tap sets the origin, second the destination (and plans). A tap
    /// after that starts over with a new origin.
    func tap(_ coordinate: CLLocationCoordinate2D) {
        if origin == nil || destination != nil {
            clear()
            origin = coordinate
        } else {
            destination = coordinate
            plan()
        }
    }

    func clear() {
        planTask?.cancel()
        origin = nil
        destination = nil
        route = nil
        savedRoute = nil
        errorMessage = nil
        notice = nil
        isPlanning = false
    }

    /// Display a saved route in place of any current one.
    func show(_ detail: SavedRouteDetail) {
        clear()
        savedRoute = detail
        origin = detail.waypoints.first?.coordinate
        destination = detail.waypoints.last?.coordinate
    }

    /// Saves the planned route under `name`. The server recomputes it from
    /// the endpoints, so this stores what the server would plan, not what is
    /// on screen byte-for-byte.
    func save(name: String) async {
        guard route != nil, let origin, let destination else { return }
        isSaving = true
        errorMessage = nil
        notice = nil
        defer { isSaving = false }
        do {
            let saved = try await api.saveRoute(name: name, from: origin, to: destination)
            notice = "Saved “\(saved.name)”."
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func plan() {
        guard let origin, let destination else { return }
        planTask?.cancel()
        isPlanning = true
        errorMessage = nil
        planTask = Task {
            defer { if !Task.isCancelled { isPlanning = false } }
            do {
                let result = try await api.planRoute(from: origin, to: destination)
                guard !Task.isCancelled else { return }
                route = result
            } catch {
                guard !Task.isCancelled else { return }
                errorMessage = error.localizedDescription
            }
        }
    }
}
