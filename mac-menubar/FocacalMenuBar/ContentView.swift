import SwiftUI

// MARK: - Popover content (320 × 480 pt)

struct ContentView: View {
    @EnvironmentObject var store: FocacalStore
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(spacing: 0) {
            headerBar
            Divider()
            agendaList
            Divider()
            footerBar
        }
        .frame(width: 320)
        .task { await store.refreshAll() }
    }

    // MARK: Header

    private var headerBar: some View {
        HStack {
            Text("Today")
                .font(.headline)
            Spacer()
            Button {
                Task { await store.replan() }
            } label: {
                if store.isReplanning {
                    ProgressView()
                        .controlSize(.small)
                        .padding(.trailing, 2)
                } else {
                    Label("Replan", systemImage: "arrow.clockwise")
                        .labelStyle(.iconOnly)
                        .imageScale(.medium)
                }
            }
            .buttonStyle(.plain)
            .help("Re-run the AI scheduler")
            .disabled(store.isReplanning)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    // MARK: Agenda list

    private var agendaList: some View {
        let remaining = store.todayEvents.filter { $0.end > .now }
        return Group {
            if remaining.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: "checkmark.circle")
                        .font(.title2)
                        .foregroundStyle(.secondary)
                    Text("Nothing left today")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 24)
            } else {
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(remaining) { event in
                            EventRow(event: event)
                            if event.id != remaining.last?.id {
                                Divider().padding(.leading, 44)
                            }
                        }
                    }
                }
                .frame(maxHeight: 340)
            }
        }
    }

    // MARK: Footer

    private var footerBar: some View {
        HStack {
            if let err = store.lastError {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.yellow)
                Text(err)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            } else {
                Spacer()
            }
            Spacer()
            Button {
                if let url = URL(string: "\(store.serverURL)/today") {
                    NSWorkspace.shared.open(url)
                }
            } label: {
                Image(systemName: "safari")
            }
            .buttonStyle(.plain)
            .help("Open in browser")

            Button {
                openWindow(id: "settings")
            } label: {
                Image(systemName: "gearshape")
            }
            .buttonStyle(.plain)
            .help("Settings")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }
}

// MARK: - Single event row

private struct EventRow: View {
    let event: FocalEvent

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            // Dot indicator
            Circle()
                .fill(dotColor)
                .frame(width: 8, height: 8)
                .padding(.leading, 12)

            // Icon
            Image(systemName: event.symbolName)
                .frame(width: 20)
                .foregroundStyle(event.isActive ? .primary : .secondary)

            // Title + time range
            VStack(alignment: .leading, spacing: 1) {
                Text(event.title)
                    .font(.system(size: 13, weight: event.isActive ? .semibold : .regular))
                    .lineLimit(1)
                Text(timeRange)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            // Duration / remaining
            if event.isActive {
                Text(remainingLabel)
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                    .padding(.trailing, 12)
            } else {
                Text(durationLabel)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.trailing, 12)
            }
        }
        .padding(.vertical, 8)
        .background(event.isActive ? Color.accentColor.opacity(0.07) : Color.clear)
    }

    private var dotColor: Color {
        if event.isActive     { return .accentColor }
        if event.start > .now { return .secondary.opacity(0.4) }
        return .clear
    }

    private var timeRange: String {
        let f = DateFormatter()
        f.timeStyle = .short
        f.dateStyle = .none
        return "\(f.string(from: event.start)) – \(f.string(from: event.end))"
    }

    private var durationLabel: String {
        let m = Int(event.duration / 60)
        return m < 60 ? "\(m)m" : "\(m / 60)h \(m % 60 == 0 ? "" : "\(m % 60)m")"
    }

    private var remainingLabel: String {
        let s = max(0, Int(event.remaining))
        if s < 60   { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m left" }
        return "\(s / 3600)h \((s % 3600) / 60)m left"
    }
}
