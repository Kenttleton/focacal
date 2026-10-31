# Focacal Windows Tray

A native Windows system tray app for Focacal built with WinUI 3 (Windows App SDK 1.5).

- **Tray icon** with tooltip showing current event and time remaining
- **Left-click** opens a floating panel with today's remaining events (dismisses on focus loss)
- **Right-click** opens a context menu: Open, Settings, Open in browser, Quit
- **Settings window** for server URL and poll interval

**Requires Windows 10 version 1809 (build 17763) or later, and .NET 8.**

---

## Prerequisites

- [Visual Studio 2022](https://visualstudio.microsoft.com/) (Community edition is free) with:
  - **Desktop development with C++** workload
  - **.NET desktop development** workload
  - **Windows App SDK** component (included in the above workloads)
- [.NET 8 SDK](https://dotnet.microsoft.com/download/dotnet/8)
- Focacal backend running (`focacal start` — defaults to port 5233)

Verify your setup from a Developer Command Prompt:

```
dotnet --version    # should be 8.x
```

---

## Add a tray icon

The project references `Assets\TrayIcon.ico`. You need to provide a 256×256 `.ico` file before building.

**Quick option — generate from the command line:**

```powershell
# Requires ImageMagick (winget install ImageMagick.ImageMagick)
magick -size 256x256 xc:none -fill "#0078D4" -draw "roundrectangle 24,24 232,232 32,32" `
       -resize 256x256 windows-tray\FocacalTray\Assets\TrayIcon.ico
```

**Manual option:**
1. Create or download any 256×256 icon
2. Save it as `windows-tray\FocacalTray\Assets\TrayIcon.ico`

The tray renders the icon at 16×16 px, so a simple geometric shape works well.

---

## Build and run

### Visual Studio

1. Open `windows-tray\FocacalTray\FocacalTray.csproj` (or `File → Open → Project/Solution`)
2. Select **Debug** or **Release** configuration, **x64** platform
3. Press **F5** (run with debugger) or **Ctrl+F5** (run without)

The app has no main window. After launch it appears in the system tray (you may need to expand the hidden icons chevron `^` in the taskbar corner).

### Command line

```powershell
cd windows-tray\FocacalTray
dotnet run --framework net8.0-windows10.0.19041.0
```

For a standalone executable:

```powershell
dotnet publish -c Release -r win-x64 --self-contained false -o publish\
```

The output in `publish\` can be copied anywhere and run directly.

---

## First-time setup

If focacal is running on a non-default address:

1. Right-click the tray icon → **Settings**
2. Update **Server URL** (e.g. `http://192.168.1.10:5233`)
3. Click **Apply**

Settings are saved to `%APPDATA%\Focacal\settings.json`.

---

## Tray behavior

| Action | Result |
|--------|--------|
| Left-click icon | Toggle the day-view flyout panel |
| Right-click icon | Context menu (Open, Settings, Browser, Quit) |
| Click outside flyout | Flyout dismisses |
| Tooltip (hover over icon) | Current event + time remaining |

The flyout positions itself above the tray area (bottom-right of the work area). It stays on top of other windows until dismissed.

---

## Launch at startup

1. Build a Release publish (see above) and copy the folder somewhere permanent, e.g. `C:\Tools\FocacalTray\`
2. Press **Win+R**, type `shell:startup`, press Enter
3. Create a shortcut to `FocacalTray.exe` in the Startup folder

---

## Troubleshooting

**Icon not visible in tray**
Windows may hide it in the overflow area. Click the `^` chevron next to the clock, then drag Focacal out to the visible tray.

**Build error: `TrayIcon.ico` not found**
Create the `Assets\` directory and add a `.ico` file as described in the [Add a tray icon](#add-a-tray-icon) section.

**`Bootstrap.Initialize` throws at startup**
The Windows App SDK runtime is not installed on this machine. Run the installer from the [Windows App SDK releases page](https://github.com/microsoft/WindowsAppSDK/releases) — look for the `WindowsAppRuntimeInstall-x64.exe` asset — or use the self-contained publish option:

```powershell
dotnet publish -c Release -r win-x64 --self-contained true -o publish\
```

**Flyout appears in wrong position**
The flyout always anchors to the bottom-right corner of the primary monitor's work area. Multi-monitor support with a taskbar on a secondary screen is not yet implemented.

**`An attempt was made to access a socket in a way forbidden by its access permissions`**
A firewall or security policy is blocking outbound HTTP to `localhost:5233`. Allow the app through Windows Defender Firewall, or change the server URL to `127.0.0.1:5233`.
