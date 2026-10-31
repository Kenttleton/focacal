using H.NotifyIcon;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace FocacalTray;

public partial class App : Application
{
    private TaskbarIcon? _trayIcon;
    private FlyoutWindow? _flyout;
    private SettingsWindow? _settings;

    public static FocacalStore Store { get; private set; } = null!;

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        Store = new FocacalStore(DispatcherQueue.GetForCurrentThread());

        // Retrieve the tray icon declared in App.xaml resources
        _trayIcon = (TaskbarIcon)Resources["TrayIcon"];

        // Wire left-click to flyout
        _trayIcon.LeftClickCommand = new RelayCommand(ToggleFlyout);

        // Wire right-click menu items
        WireMenuItem("OpenFlyoutItem",  () => ShowFlyout());
        WireMenuItem("SettingsItem",    () => ShowSettings());
        WireMenuItem("OpenBrowserItem", () => OpenBrowser());
        WireMenuItem("QuitItem",        () => { Store.Dispose(); Exit(); });

        // Update tooltip on state changes
        Store.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName is nameof(FocacalStore.Current) or nameof(FocacalStore.Next) or nameof(FocacalStore.Tick))
                _trayIcon.ToolTipText = BuildTooltip();
        };
    }

    private void ToggleFlyout()
    {
        if (_flyout is { IsVisible: true })
            _flyout.Hide();
        else
            ShowFlyout();
    }

    private void ShowFlyout()
    {
        if (_flyout is null || !_flyout.IsLoaded)
            _flyout = new FlyoutWindow();

        _flyout.PositionNearTray();
        _flyout.Activate();
    }

    private void ShowSettings()
    {
        if (_settings is null || !_settings.IsLoaded)
            _settings = new SettingsWindow();
        _settings.Activate();
    }

    private static void OpenBrowser()
    {
        var url = Store.Settings.ServerUrl.TrimEnd('/') + "/today";
        System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo(url) { UseShellExecute = true });
    }

    private void WireMenuItem(string name, Action handler)
    {
        if (_trayIcon?.ContextFlyout is MenuFlyout menu)
        {
            foreach (var item in menu.Items)
            {
                if (item is MenuFlyoutItem fi && fi.Name == name)
                {
                    fi.Click += (_, _) => handler();
                    return;
                }
            }
        }
    }

    private string BuildTooltip()
    {
        if (Store.Current is { } cur)
        {
            var rem = FormatRemaining(cur.Remaining);
            return $"Focacal — {cur.Title} ({rem} left)";
        }
        if (Store.Next is { } next)
        {
            var rem = FormatRemaining(next.Start - DateTime.Now);
            return $"Focacal — {next.Title} in {rem}";
        }
        return "Focacal";
    }

    private static string FormatRemaining(TimeSpan t)
    {
        t = t < TimeSpan.Zero ? TimeSpan.Zero : t;
        if (t.TotalSeconds < 60)  return $"{(int)t.TotalSeconds}s";
        if (t.TotalMinutes < 60)  return $"{(int)t.TotalMinutes}m";
        return $"{(int)t.TotalHours}h {t.Minutes}m";
    }
}

// Minimal RelayCommand so we don't pull in a full MVVM library
internal sealed class RelayCommand(Action execute) : System.Windows.Input.ICommand
{
    public event EventHandler? CanExecuteChanged;
    public bool CanExecute(object? p) => true;
    public void Execute(object? p)    => execute();
}
