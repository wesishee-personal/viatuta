import CoreLocation
import Observation

/// Holds the origin/destination the rider has picked and the route planned
/// between them.
@MainActor
@Observable
final class RoutePlanner {
    private(set) var origin: CLLocationCoordinate2D?
    private(set) var destination: CLLocationCoordinate2D?
    private(set) var route: PlanResponse?
    private(set) var isPlanning = false
    private(set) var errorMessage: String?

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
        errorMessage = nil
        isPlanning = false
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
