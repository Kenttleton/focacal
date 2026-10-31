using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;

namespace FocacalTray;

public sealed partial class SettingsWindow : Window
{
    public SettingsWindow()
    {
        InitializeComponent();
        ConfigureWindow();
        LoadSettings();
        BindStatus();
    }

    private void ConfigureWindow()
    {
        Title = "Focacal Settings";
        AppWindow.Resize(new Windows.Graphics.SizeInt32(440, 380));

        if (AppWindow.Presenter is OverlappedPresenter p)
        {
            p.IsResizable   = false;
            p.IsMaximizable = false;
        }
    }

    private void LoadSettings()
    {
        var s = App.Store.Settings;
        ServerUrlBox.Text = s.ServerUrl;
        PollBox.Value     = s.PollInterval;
    }

    private void BindStatus()
    {
        UpdateStatus();
        App.Store.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName is nameof(FocacalStore.IsReachable) or nameof(FocacalStore.LastError))
                DispatcherQueue.TryEnqueue(UpdateStatus);
        };
    }

    private void UpdateStatus()
    {
        bool ok = App.Store.IsReachable;
        StatusDot.Fill = new SolidColorBrush(ok
            ? Windows.UI.Color.FromArgb(255, 16, 185, 129)
            : Windows.UI.Color.FromArgb(255, 220, 38, 38));
        StatusText.Text = ok ? "Connected" : "Unreachable";

        if (App.Store.LastError is { } err)
        {
            ErrorText.Text       = err;
            ErrorText.Visibility = Visibility.Visible;
        }
        else
        {
            ErrorText.Visibility = Visibility.Collapsed;
        }
    }

    private void ApplyButton_Click(object _, RoutedEventArgs e)
    {
        if (!Uri.TryCreate(ServerUrlBox.Text.Trim(), UriKind.Absolute, out _))
        {
            UrlError.Visibility = Visibility.Visible;
            return;
        }
        UrlError.Visibility = Visibility.Collapsed;

        var updated = new AppSettings
        {
            ServerUrl    = ServerUrlBox.Text.Trim().TrimEnd('/'),
            PollInterval = (int)PollBox.Value,
            Timezone     = App.Store.Settings.Timezone,
        };
        App.Store.ApplySettings(updated);
        Close();
    }

    private void CancelButton_Click(object _, RoutedEventArgs e) => Close();
}
