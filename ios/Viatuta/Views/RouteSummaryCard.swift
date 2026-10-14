import SwiftUI

struct RouteSummaryCard: View {
    let planner: RoutePlanner
    let onSave: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if planner.isPlanning {
                HStack { ProgressView(); Text("Finding the safest route…") }
            } else if let message = planner.errorMessage {
                Text(message).foregroundStyle(.red)
            } else if let route = planner.route {
                stats(distanceM: route.distanceM, durationS: route.durationS, score: route.safetyScore)
                Text(detourText(route)).font(.footnote).foregroundStyle(.secondary)
                ForEach(route.warnings ?? [], id: \.self) { Text($0).font(.footnote) }
            } else if let saved = planner.savedRoute {
                Text(saved.name).font(.headline)
                stats(distanceM: saved.distanceM, durationS: saved.durationS, score: saved.safetyScore)
            } else if planner.origin == nil {
                Text("Tap the map to set your start.")
            } else {
                Text("Tap the map to set your destination.")
            }

            if let notice = planner.notice {
                Text(notice).font(.footnote).foregroundStyle(.green)
            }

            HStack {
                if planner.route != nil {
                    Button(action: onSave) {
                        if planner.isSaving { ProgressView() } else { Text("Save route") }
                    }
                    .buttonStyle(.bordered)
                    .disabled(planner.isSaving)
                }
                Spacer()
                if planner.origin != nil {
                    Button("Clear", role: .destructive, action: planner.clear).font(.footnote)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding()
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
        .padding()
    }

    private func stats(distanceM: Double, durationS: Int?, score: Double?) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(String(format: "%.1f km", distanceM / 1000)).font(.title2.bold())
            if let durationS { Text("· \(durationS / 60) min") }
            Spacer()
            if let score { Text("Safety \(Int(score.rounded()))/100").font(.headline) }
        }
    }

    private func detourText(_ route: PlanResponse) -> String {
        let extra = route.distanceM - route.baselineDistanceM
        if extra < 50 { return "About the same as the shortest route." }
        return String(format: "%.1f km longer than the shortest route, to stay on calmer streets.", extra / 1000)
    }
}
