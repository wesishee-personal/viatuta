import SwiftUI

struct RouteSummaryCard: View {
    let planner: RoutePlanner

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if planner.isPlanning {
                HStack { ProgressView(); Text("Finding the safest route…") }
            } else if let message = planner.errorMessage {
                Text(message).foregroundStyle(.red)
            } else if let route = planner.route {
                HStack(alignment: .firstTextBaseline) {
                    Text(String(format: "%.1f km", route.distanceM / 1000)).font(.title2.bold())
                    Text("· \(route.durationS / 60) min")
                    Spacer()
                    Text("Safety \(Int(route.safetyScore.rounded()))/100").font(.headline)
                }
                Text(detourText(route)).font(.footnote).foregroundStyle(.secondary)
                ForEach(route.warnings ?? [], id: \.self) { Text($0).font(.footnote) }
            } else if planner.origin == nil {
                Text("Tap the map to set your start.")
            } else {
                Text("Tap the map to set your destination.")
            }

            if planner.origin != nil {
                Button("Clear", role: .destructive, action: planner.clear).font(.footnote)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding()
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
        .padding()
    }

    private func detourText(_ route: PlanResponse) -> String {
        let extra = route.distanceM - route.baselineDistanceM
        if extra < 50 { return "About the same as the shortest route." }
        return String(format: "%.1f km longer than the shortest route, to stay on calmer streets.", extra / 1000)
    }
}
