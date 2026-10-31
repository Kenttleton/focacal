import Foundation
import SwiftUI

// MARK: - App-wide state

@MainActor
final class FocacalStore: ObservableObject {

    // Menu bar display
    @Published var currentEvent: FocalEvent?
    @Published var nextEvent: FocalEvent?

    // Popover
    @Published var todayEvents: [FocalEvent] = []

    // Status
    @Published var isReachable: Bool = true
    @Published var isReplanning: Bool = false
    @Published var lastError: String?
    @Published var lastReplan: Date?

    // Settings (persisted via UserDefaults)
    @AppStorage("serverURL")    var serverURL: String    = "http://localhost:5233"
    @AppStorage("pollInterval") var pollInterval: Int    = 60   // seconds
    @AppStorage("timezone")     var timezone: String     = TimeZone.current.identifier

    private var api: FocacalAPI
    private var pollTask: Task<Void, Never>?
    private var countdownTask: Task<Void, Never>?

    // Triggers a SwiftUI update every second for the countdown label
    @Published var tick: Date = .now

    init() {
        api = FocacalAPI(baseURL: UserDefaults.standard.string(forKey: "serverURL") ?? "http://localhost:5233")
        startPolling()
        startCountdown()
    }

    // MARK: - Public actions

    func replan() async {
        isReplanning = true
        defer { isReplanning = false }
        do {
            _ = try await api.replan()
            await refreshNow()
            await refreshToday()
        } catch {
            lastError = error.localizedDescription
        }
    }

    func refreshAll() async {
        await refreshNow()
        await refreshToday()
    }

    // MARK: - Settings change

    func applyServerURL(_ url: String) {
        serverURL = url
        api = FocacalAPI(baseURL: url)
        restartPolling()
    }

    func applyPollInterval(_ seconds: Int) {
        pollInterval = seconds
        restartPolling()
    }

    // MARK: - Private

    private func startPolling() {
        pollTask = Task { [weak self] in
            guard let self else { return }
            while !Task.isCancelled {
                await self.refreshNow()
                await self.refreshToday()
                try? await Task.sleep(nanoseconds: UInt64(self.pollInterval) * 1_000_000_000)
            }
        }
    }

    private func restartPolling() {
        pollTask?.cancel()
        startPolling()
    }

    private func startCountdown() {
        countdownTask = Task { [weak self] in
            while !Task.isCancelled {
                await MainActor.run { self?.tick = .now }
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
    }

    private func refreshNow() async {
        do {
            let r = try await api.now()
            currentEvent = r.current
            nextEvent    = r.next
            isReachable  = true
            lastError    = nil
        } catch {
            isReachable = false
            lastError   = error.localizedDescription
        }
    }

    private func refreshToday() async {
        do {
            let r = try await api.today()
            todayEvents = r.events
        } catch {
            // non-fatal; reachability tracked by refreshNow
        }
    }
}
