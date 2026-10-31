import SwiftUI

// MARK: - Settings window  (Shottr-style: compact sections, no sidebar)

struct SettingsView: View {
    @EnvironmentObject var store: FocacalStore

    @State private var draftURL: String = ""
    @State private var draftPoll: Int   = 60
    @State private var draftTZ: String  = TimeZone.current.identifier

    @State private var showURLError = false

    var body: some View {
        Form {
            // --- Server ---
            Section {
                LabeledContent("Server URL") {
                    TextField("http://localhost:5233", text: $draftURL)
                        .textFieldStyle(.roundedBorder)
                        .frame(minWidth: 240)
                        .onSubmit { applyURL() }

                    if showURLError {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }

                LabeledContent("Poll interval") {
                    HStack {
                        Stepper("\(draftPoll)s", value: $draftPoll, in: 10...300, step: 10)
                        Text("seconds")
                            .foregroundStyle(.secondary)
                    }
                }
            } header: {
                Text("Connection")
                    .font(.headline)
                    .padding(.bottom, 2)
            }

            // --- Display ---
            Section {
                LabeledContent("Timezone") {
                    Picker("", selection: $draftTZ) {
                        ForEach(TimeZone.knownTimeZoneIdentifiers, id: \.self) { tz in
                            Text(tz).tag(tz)
                        }
                    }
                    .labelsHidden()
                    .frame(minWidth: 240)
                }
            } header: {
                Text("Display")
                    .font(.headline)
                    .padding(.bottom, 2)
            }

            // --- Status ---
            Section {
                LabeledContent("Focacal") {
                    HStack(spacing: 6) {
                        Circle()
                            .fill(store.isReachable ? Color.green : Color.red)
                            .frame(width: 8, height: 8)
                        Text(store.isReachable ? "Connected" : "Unreachable")
                            .foregroundStyle(.secondary)
                    }
                }
                if let err = store.lastError {
                    LabeledContent("Last error") {
                        Text(err)
                            .foregroundStyle(.red)
                            .font(.caption)
                    }
                }
                if let t = store.lastReplan {
                    LabeledContent("Last replan") {
                        Text(t, style: .relative)
                            .foregroundStyle(.secondary)
                    }
                }
            } header: {
                Text("Status")
                    .font(.headline)
                    .padding(.bottom, 2)
            }

            // --- Actions ---
            HStack {
                Spacer()
                Button("Apply") { apply() }
                    .keyboardShortcut(.return, modifiers: [])
                    .buttonStyle(.borderedProminent)
            }
            .padding(.top, 4)
        }
        .formStyle(.grouped)
        .padding(20)
        .frame(width: 440)
        .onAppear { loadDrafts() }
    }

    // MARK: -

    private func loadDrafts() {
        draftURL  = store.serverURL
        draftPoll = store.pollInterval
        draftTZ   = store.timezone
    }

    private func applyURL() {
        guard URL(string: draftURL) != nil else {
            showURLError = true
            return
        }
        showURLError = false
    }

    private func apply() {
        applyURL()
        guard !showURLError else { return }
        store.applyServerURL(draftURL)
        store.applyPollInterval(draftPoll)
        store.timezone = draftTZ
    }
}
