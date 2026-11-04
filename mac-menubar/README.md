# Focacal Menu Bar

A native macOS menu bar app for Focacal. Shows the current event title and a live countdown in the menu bar. Click to see the rest of the day's agenda, trigger a replan, or open settings.

**Requires macOS 14.0 (Sonoma) or later.**

---

## Prerequisites

- Xcode 15 or later ([Mac App Store](https://apps.apple.com/us/app/xcode/id497799835))
- Focacal backend running locally (`focacal start` — defaults to port 5233)

---

## Build and run

1. Open the project in Xcode:

   ```bash
   open mac-menubar/FocacalMenuBar.xcodeproj
   ```

2. Select the **FocacalMenuBar** scheme and your Mac as the run destination (the device picker in the toolbar).

3. Set a Development Team so Xcode can sign the app:
   - Click the project in the navigator → select the **FocacalMenuBar** target → **Signing & Capabilities**
   - Choose your personal team (free Apple ID works for local use)

4. Press **⌘R** to build and run.

The app has no Dock icon. After launch it appears as a small calendar icon in the menu bar showing the current event and a countdown timer.

---

## First-time setup

If focacal is running on a non-default port or a remote host, update the server URL before or after launch:

1. Click the menu bar icon
2. Click the gear icon (bottom-right of the popover) to open Settings
3. Change **Server URL** to match your focacal instance (e.g. `http://192.168.1.10:5233`)
4. Click **Apply**

Settings are persisted in `UserDefaults` and survive restarts.

---

## Menu bar display

| State | What you see |
|-------|-------------|
| Active focus block | `⏱ Deep Work · 42m` |
| Active meeting | `📅 Team standup · 8m` |
| No current block, next one soon | `📅 Lunch in 23m` (dimmed) |
| Nothing for the rest of the day | `focacal` (dimmed) |
| Cannot reach the backend | Icon + yellow warning triangle |

The countdown updates every second. The full event list refreshes on the poll interval (default: 60 seconds).

---

## Popover

Click the menu bar item to open a 320 pt popover showing all remaining events today:

- **Active event** is highlighted and shows time remaining
- **Replan** button (↺) triggers the AI scheduler and refreshes the list
- **Open in browser** (safari icon) opens `<serverURL>/today` in your default browser
- **Settings** gear opens the settings window

---

## Settings

| Setting | Default | Description |
|---------|---------|-------------|
| Server URL | `http://localhost:5233` | Focacal REST server address |
| Poll interval | 60 s | How often the app fetches `/now` and `/today` |
| Timezone | System timezone | Used for display formatting |

---

## Launch at login

To have the app start automatically with macOS:

1. Build a Release archive: **Product → Archive**, then **Distribute App → Copy App**
2. Copy `FocacalMenuBar.app` to `/Applications`
3. Open **System Settings → General → Login Items**
4. Click **+** and select `FocacalMenuBar.app`

Alternatively, while running in debug from Xcode, add a Login Item via the **Signing & Capabilities** tab (add the **Background Modes** + **Login Items** capability).

---

## Troubleshooting

**Yellow warning triangle in menu bar**
The app cannot reach the focacal backend. Check that `focacal start` is running and the Server URL in Settings is correct.

**"Build input file cannot be found" error in Xcode**
Make sure you opened `FocacalMenuBar.xcodeproj` and not the `mac-menubar/` folder directly.

**Popover shows "Nothing left today" when events exist**
The backend may be returning events in a different timezone. Set the timezone in Settings to match `working_hours.timezone` in your `config/planner.yaml`.

**App not appearing in menu bar after build**
The app uses `LSUIElement = YES` so it has no Dock icon. Check the menu bar — it may be hidden if the bar is full. Try reducing other menu bar items or use Bartender/Ice to reveal hidden items.
