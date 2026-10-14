import SwiftUI

struct SavedRoutesView: View {
    let api: APIClient
    /// Called with the full route once the rider picks one.
    let onOpen: (SavedRouteDetail) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var routes: [SavedRoute] = []
    @State private var isLoading = true
    @State private var errorMessage: String?

    var body: some View {
        NavigationStack {
            List {
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
                ForEach(routes) { route in
                    Button { open(route) } label: { row(route) }
                        .foregroundStyle(.primary)
                }
                .onDelete(perform: delete)
            }
            .overlay {
                if isLoading {
                    ProgressView()
                } else if routes.isEmpty && errorMessage == nil {
                    ContentUnavailableView("No saved routes", systemImage: "bicycle",
                                           description: Text("Plan a route and tap Save to keep it here."))
                }
            }
            .navigationTitle("Saved routes")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .task { await load() }
        }
    }

    private func row(_ route: SavedRoute) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(route.name).font(.headline)
            HStack {
                Text(String(format: "%.1f km", route.distanceM / 1000))
                if let score = route.safetyScore { Text("· Safety \(Int(score.rounded()))/100") }
                Spacer()
                Text(route.createdAt, style: .date)
            }
            .font(.footnote)
            .foregroundStyle(.secondary)
        }
    }

    private func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            routes = try await api.savedRoutes()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func open(_ route: SavedRoute) {
        Task {
            do {
                onOpen(try await api.savedRoute(id: route.id))
                dismiss()
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func delete(at offsets: IndexSet) {
        let doomed = offsets.map { routes[$0] }
        routes.remove(atOffsets: offsets)
        Task {
            for route in doomed {
                do {
                    try await api.deleteSavedRoute(id: route.id)
                } catch {
                    // Put it back: it wasn't actually deleted.
                    errorMessage = error.localizedDescription
                    await load()
                }
            }
        }
    }
}
