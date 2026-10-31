using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.Graphics;

namespace FocacalTray;

public sealed partial class FlyoutWindow : Window
{
    private const int Width  = 320;
    private const int Height = 480;

    public FlyoutWindow()
    {
        InitializeComponent();
        ConfigureWindow();
        BindStore();
        Render();
    }

    // --- Window setup ---

    private void ConfigureWindow()
    {
        var appWin = AppWindow;
        appWin.Resize(new SizeInt32(Width, Height));
        appWin.IsShownInSwitchers = false;

        // Remove title bar
        appWin.TitleBar.ExtendsContentIntoTitleBar = true;
        appWin.TitleBar.PreferredHeightOption = TitleBarHeightOption.Collapsed;

        // Always on top, no minimize/maximize/resize
        if (appWin.Presenter is OverlappedPresenter p)
        {
            p.IsResizable   = false;
            p.IsMaximizable = false;
            p.IsMinimizable = false;
            p.IsAlwaysOnTop = true;
        }

        // Dismiss when focus is lost
        Activated   += (_, e) => { if (e.WindowActivationState == WindowActivationState.Deactivated) Hide(); };
    }

    /// <summary>Positions the flyout just above the system tray (bottom-right of work area).</summary>
    public void PositionNearTray()
    {
        var area   = GetWorkArea();
        int margin = 12;
        int x      = area.Right  - Width  - margin;
        int y      = area.Bottom - Height - margin;
        AppWindow.Move(new PointInt32(x, y));
    }

    private static RectInt32 GetWorkArea()
    {
        // Use the display area of the primary monitor's work area
        var display = DisplayArea.GetFromWindowId(
            Microsoft.UI.Win32Interop.GetWindowIdFromWindow(
                WinRT.Interop.WindowNative.GetWindowHandle(
                    (Application.Current as App)!.GetType()  // unused, just to get a handle
                )),
            DisplayAreaFallback.Primary);
        return display.WorkArea;
    }

    private void Hide() => AppWindow.Hide();

    // --- Data binding ---

    private void BindStore()
    {
        App.Store.PropertyChanged += (_, e) =>
        {
            DispatcherQueue.TryEnqueue(() =>
            {
                switch (e.PropertyName)
                {
                    case nameof(FocacalStore.TodayEvents):
                    case nameof(FocacalStore.Current):
                        Render();
                        break;
                    case nameof(FocacalStore.IsReachable):
                        UpdateStatus();
                        break;
                    case nameof(FocacalStore.IsReplanning):
                        ReplanProgress.IsActive = App.Store.IsReplanning;
                        ReplanButton.IsEnabled  = !App.Store.IsReplanning;
                        break;
                    case nameof(FocacalStore.Tick):
                        UpdateCountdowns();
                        break;
                }
            });
        };
    }

    // --- Rendering ---

    private void Render()
    {
        var remaining = App.Store.TodayEvents.Where(e => e.End > DateTime.Now).ToList();

        EventList.Children.Clear();
        EmptyState.Visibility = Visibility.Collapsed;
        ErrorBar.IsOpen       = false;

        if (!App.Store.IsReachable)
        {
            ErrorBar.IsOpen = true;
            return;
        }

        if (remaining.Count == 0)
        {
            EmptyState.Visibility = Visibility.Visible;
            return;
        }

        foreach (var ev in remaining)
            EventList.Children.Add(BuildEventRow(ev));

        UpdateStatus();
    }

    private Grid BuildEventRow(FocalEvent ev)
    {
        var grid = new Grid { Padding = new Thickness(16, 10, 16, 10) };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });

        if (ev.IsActive)
            grid.Background = new SolidColorBrush(Windows.UI.Color.FromArgb(18, 0, 120, 212));

        // Dot
        var dot = new Ellipse
        {
            Width = 8, Height = 8,
            Margin = new Thickness(0, 0, 10, 0),
            VerticalAlignment = VerticalAlignment.Center,
            Fill = ev.IsActive
                ? new SolidColorBrush(Windows.UI.Color.FromArgb(255, 0, 120, 212))
                : new SolidColorBrush(Windows.UI.Color.FromArgb(80, 128, 128, 128)),
        };
        Grid.SetColumn(dot, 0);

        // Title + time range
        var titleBlock = new TextBlock
        {
            Text       = ev.Title,
            FontWeight = ev.IsActive ? Microsoft.UI.Text.FontWeights.SemiBold : Microsoft.UI.Text.FontWeights.Normal,
            TextTrimming = TextTrimming.CharacterEllipsis,
            MaxLines   = 1,
        };
        var timeBlock = new TextBlock
        {
            Text       = $"{ev.Start:h:mm tt} – {ev.End:h:mm tt}",
            Style      = (Style)Application.Current.Resources["CaptionTextBlockStyle"],
            Foreground = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"],
        };
        var titleStack = new StackPanel { Margin = new Thickness(0, 0, 8, 0) };
        titleStack.Children.Add(titleBlock);
        titleStack.Children.Add(timeBlock);
        Grid.SetColumn(titleStack, 1);

        // Remaining / duration
        var badge = new TextBlock
        {
            Text       = ev.IsActive ? FormatRemaining(ev.Remaining) : FormatDuration(ev.Duration),
            Style      = (Style)Application.Current.Resources["CaptionTextBlockStyle"],
            Foreground = (Brush)Application.Current.Resources["TextFillColorSecondaryBrush"],
            VerticalAlignment = VerticalAlignment.Center,
            Tag        = ev.Id,   // used by UpdateCountdowns
        };
        Grid.SetColumn(badge, 2);

        grid.Children.Add(dot);
        grid.Children.Add(titleStack);
        grid.Children.Add(badge);

        return grid;
    }

    private void UpdateCountdowns()
    {
        var active = App.Store.TodayEvents.FirstOrDefault(e => e.IsActive);
        foreach (var child in EventList.Children.OfType<Grid>())
        {
            var badge = child.Children.OfType<TextBlock>().FirstOrDefault(tb => tb.Tag is string);
            if (badge is null) continue;
            var id = (string)badge.Tag;
            var ev = App.Store.TodayEvents.FirstOrDefault(e => e.Id == id);
            if (ev is not null && ev.IsActive)
                badge.Text = FormatRemaining(ev.Remaining);
        }
    }

    private void UpdateStatus()
    {
        bool ok = App.Store.IsReachable;
        StatusDot.Fill = new SolidColorBrush(ok
            ? Windows.UI.Color.FromArgb(255, 16, 185, 129)   // green
            : Windows.UI.Color.FromArgb(255, 220, 38, 38));  // red
        StatusText.Text = ok ? "Connected" : "Unreachable";
    }

    // --- Button handlers ---

    private async void ReplanButton_Click(object _, RoutedEventArgs e)
    {
        await App.Store.Replan();
    }

    private void BrowserButton_Click(object _, RoutedEventArgs e)
    {
        var url = App.Store.Settings.ServerUrl.TrimEnd('/') + "/today";
        System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo(url) { UseShellExecute = true });
    }

    private void SettingsButton_Click(object _, RoutedEventArgs e)
    {
        Hide();
        var settings = new SettingsWindow();
        settings.Activate();
    }

    // --- Formatting ---

    private static string FormatRemaining(TimeSpan t)
    {
        t = t < TimeSpan.Zero ? TimeSpan.Zero : t;
        if (t.TotalSeconds < 60)  return $"{(int)t.TotalSeconds}s left";
        if (t.TotalMinutes < 60)  return $"{(int)t.TotalMinutes}m left";
        return $"{(int)t.TotalHours}h {t.Minutes}m left";
    }

    private static string FormatDuration(TimeSpan t)
    {
        if (t.TotalMinutes < 60) return $"{(int)t.TotalMinutes}m";
        return t.Minutes == 0 ? $"{(int)t.TotalHours}h" : $"{(int)t.TotalHours}h {t.Minutes}m";
    }
}
