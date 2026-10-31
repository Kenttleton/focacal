import SwiftUI

// MARK: - Compact label shown in the menu bar

struct MenuBarLabel: View {
    @EnvironmentObject var store: FocacalStore

    // Observe tick so the countdown updates every second
    private var _ = { _ = \.store.tick }

    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: iconName)
                .imageScale(.small)

            if let event = store.currentEvent {
                Text(truncated(event.title))
                    .lineLimit(1)
                Text("·")
                    .foregroundStyle(.secondary)
                Text(countdown(to: event.end))
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            } else if let next = store.nextEvent {
                Text(truncated(next.title))
                    .lineLimit(1)
                    .foregroundStyle(.secondary)
                Text("in \(countdown(to: next.start))")
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            } else {
                Text("focacal")
                    .foregroundStyle(.secondary)
            }

            if !store.isReachable {
                Image(systemName: "exclamationmark.triangle.fill")
                    .imageScale(.small)
                    .foregroundStyle(.yellow)
            }
        }
        .font(.system(size: 12, weight: .medium))
    }

    // MARK: Helpers

    private var iconName: String {
        guard store.isReachable else { return "calendar.badge.exclamationmark" }
        return store.currentEvent?.symbolName ?? "calendar"
    }

    private func truncated(_ title: String, max: Int = 22) -> String {
        title.count <= max ? title : String(title.prefix(max - 1)) + "…"
    }

    private func countdown(to date: Date) -> String {
        _ = store.tick  // subscribe to per-second updates
        let secs = max(0, Int(date.timeIntervalSinceNow))
        if secs < 60    { return "\(secs)s" }
        if secs < 3600  { return "\(secs / 60)m" }
        let h = secs / 3600
        let m = (secs % 3600) / 60
        return m == 0 ? "\(h)h" : "\(h)h \(m)m"
    }
}
