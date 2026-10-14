import CoreLocation
import SwiftUI

/// Report a hazard at a point the rider has chosen on the map.
///
/// This is the detailed flow used from the planning screen: choose the kind,
/// optionally add a note. It sends no heading, so the server records the hazard
/// as affecting both directions. The navigation screen will use a quicker
/// one-tap flow built on the same `HazardService`.
struct ReportHazardSheet: View {
    let coordinate: CLLocationCoordinate2D
    /// Called with the outcome once the report has been accepted.
    let onReported: (HazardService.ReportOutcome) -> Void

    @Environment(HazardService.self) private var service
    @Environment(\.dismiss) private var dismiss

    @State private var chosen: String?
    @State private var notes = ""
    @State private var isSending = false
    @State private var errorMessage: String?

    private static let notesLimit = 500
    private let columns = Array(repeating: GridItem(.flexible(), spacing: 10), count: 3)

    private var chosenKind: HazardKindInfo? { chosen.flatMap(service.kind(named:)) }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    switch service.kindsState {
                    case .loading:
                        ProgressView("Loading hazard types…").frame(maxWidth: .infinity)
                    case .failed:
                        VStack(spacing: 8) {
                            Text("Couldn't load the hazard types.")
                            Button("Try again") { Task { await service.loadKinds() } }
                        }
                        .frame(maxWidth: .infinity)
                    case .ready:
                        Text("What is it?").font(.headline)
                        LazyVGrid(columns: columns, spacing: 10) {
                            ForEach(service.kinds) { kind in
                                KindButton(kind: kind, isSelected: chosen == kind.kind) {
                                    chosen = kind.kind
                                }
                            }
                        }
                    }
                }
                .padding()
            }
            // The hint, note field and button live in a fixed bar so they stay
            // in view however far the grid of kinds is scrolled.
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 10) {
                    if let kind = chosenKind { lifetimeHint(for: kind) }

                    if service.kindsState == .ready {
                        TextField("Add a note (optional)", text: $notes, axis: .vertical)
                            .lineLimit(1...3)
                            .textFieldStyle(.roundedBorder)
                            .onChange(of: notes) { _, new in
                                if new.count > Self.notesLimit { notes = String(new.prefix(Self.notesLimit)) }
                            }
                            .accessibilityIdentifier("hazardNotesField")
                    }

                    if let errorMessage {
                        Text(errorMessage).font(.footnote).foregroundStyle(.red)
                    }

                    Button(action: submit) {
                        HStack {
                            if isSending { ProgressView().tint(.white) }
                            Text(chosenKind.map { "Report \($0.label.lowercased())" } ?? "Choose a type")
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .disabled(chosen == nil || isSending)
                    .accessibilityIdentifier("submitHazardReport")
                }
                .padding()
                .background(.bar)
            }
            .navigationTitle("Report a hazard")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
        .task { if service.kindsState != .ready { await service.loadKinds() } }
    }

    private func lifetimeHint(for kind: HazardKindInfo) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Label("Stays on the map for about \(HazardFormat.span(seconds: kind.ttlS)), longer if other riders confirm it.",
                  systemImage: "clock")
            if kind.closure {
                Label("Routes will steer well clear of this road.", systemImage: "arrow.triangle.turn.up.right.diamond")
            }
        }
        .font(.footnote)
        .foregroundStyle(.secondary)
        .accessibilityIdentifier("hazardLifetimeHint")
    }

    private func submit() {
        guard let kind = chosen else { return }
        isSending = true
        errorMessage = nil
        Task {
            defer { isSending = false }
            do {
                let outcome = try await service.report(kind: kind, at: coordinate, notes: notes)
                onReported(outcome)
                dismiss()
            } catch {
                errorMessage = HazardFormat.userMessage(for: error)
            }
        }
    }
}

private struct KindButton: View {
    let kind: HazardKindInfo
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                Image(systemName: HazardIcon.symbol(for: kind.kind))
                    .font(.title3)
                    .frame(height: 24)
                Text(kind.label)
                    .font(.caption)
                    .multilineTextAlignment(.center)
                    .lineLimit(2, reservesSpace: true)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
            .padding(.horizontal, 4)
            .foregroundStyle(isSelected ? Color.white : (kind.closure ? HazardBadge.closureColor : Color.primary))
            .background(isSelected ? (kind.closure ? HazardBadge.closureColor : Color.accentColor)
                                   : Color(.secondarySystemBackground),
                        in: RoundedRectangle(cornerRadius: 12))
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("kind-\(kind.kind)")
        .accessibilityAddTraits(isSelected ? .isSelected : [])
    }
}
