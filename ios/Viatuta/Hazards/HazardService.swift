import CoreLocation
import MapKit
import Observation

/// Everything the app does with hazards, independent of any screen.
///
/// Planning and navigation both report and respond to hazards, so none of this
/// knows where a coordinate came from: the caller passes it in, with an
/// optional heading. One instance lives for the whole app and is shared
/// through the environment.
@MainActor
@Observable
final class HazardService {
    enum KindsState: Equatable {
        case loading
        case ready
        case failed
    }

    /// The result of reporting. `merged` means the report joined a hazard that
    /// was already on the map and counts as a confirmation of it.
    struct ReportOutcome: Equatable {
        let hazard: HazardReport
        let merged: Bool
    }

    /// The result of "it's gone". A hazard only comes down once enough riders
    /// agree, so `tookDown` says whether this response did it.
    struct ResolveOutcome: Equatable {
        let hazard: HazardReport
        let tookDown: Bool
    }

    private(set) var kindsState: KindsState = .loading
    private(set) var kinds: [HazardKindInfo] = []
    /// Active hazards in and around the last region the map showed.
    private(set) var hazards: [HazardReport] = []

    private let api: APIClient
    private var lastRegion: MKCoordinateRegion?

    init(api: APIClient) {
        self.api = api
    }

    func kind(named name: String) -> HazardKindInfo? {
        kinds.first { $0.kind == name }
    }

    func loadKinds() async {
        kindsState = .loading
        do {
            kinds = try await api.hazardKinds()
            kindsState = .ready
        } catch {
            kindsState = .failed
        }
    }

    /// Reloads the hazards around `region`. The box is grown a little so small
    /// pans do not leave the edge of the screen empty. A failure keeps what is
    /// already shown: hazards are an overlay, not worth an error on top of a
    /// perfectly good route.
    func refresh(in region: MKCoordinateRegion) async {
        lastRegion = region
        let box = Self.paddedBounds(of: region)
        do {
            hazards = try await api.hazards(
                minLon: box.minLon, minLat: box.minLat, maxLon: box.maxLon, maxLat: box.maxLat)
        } catch {
            // Keep the previous hazards.
        }
    }

    /// Reloads the last region, e.g. after the rider changed something.
    func refresh() async {
        if let lastRegion { await refresh(in: lastRegion) }
    }

    func report(kind: String, at coordinate: CLLocationCoordinate2D,
                headingDeg: Double? = nil, notes: String? = nil) async throws -> ReportOutcome {
        let trimmed = notes?.trimmingCharacters(in: .whitespacesAndNewlines)
        let submission = HazardSubmission(
            location: LatLon(coordinate), kind: kind,
            notes: (trimmed?.isEmpty ?? true) ? nil : trimmed,
            headingDeg: headingDeg)
        let result = try await api.reportHazard(submission)
        await refresh()
        return ReportOutcome(hazard: result.hazard, merged: result.merged)
    }

    func confirm(_ hazard: HazardReport) async throws -> HazardReport {
        let updated = try await api.confirmHazard(id: hazard.id)
        await refresh()
        return updated
    }

    func resolve(_ hazard: HazardReport, now: Date = .now) async throws -> ResolveOutcome {
        let updated = try await api.resolveHazard(id: hazard.id)
        await refresh()
        return ResolveOutcome(hazard: updated, tookDown: updated.expiresAt <= now)
    }

    /// The region's bounds, grown by a quarter on each side.
    static func paddedBounds(of region: MKCoordinateRegion, factor: Double = 0.25)
        -> (minLon: Double, minLat: Double, maxLon: Double, maxLat: Double) {
        let dLat = region.span.latitudeDelta * (0.5 + factor)
        let dLon = region.span.longitudeDelta * (0.5 + factor)
        return (region.center.longitude - dLon, region.center.latitude - dLat,
                region.center.longitude + dLon, region.center.latitude + dLat)
    }
}
