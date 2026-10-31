using System.ComponentModel;
using System.Runtime.CompilerServices;
using Microsoft.UI.Dispatching;

namespace FocacalTray;

/// <summary>
/// Central state store. Polls the focacal backend and notifies the UI.
/// All property-change notifications are dispatched to the UI thread.
/// </summary>
public sealed class FocacalStore : INotifyPropertyChanged, IDisposable
{
    private readonly DispatcherQueue _ui;
    private FocacalApi _api;
    private readonly System.Timers.Timer _pollTimer;
    private readonly System.Timers.Timer _countdownTimer;

    public AppSettings Settings { get; private set; }

    // --- Observable state ---

    private FocalEvent? _current;
    public FocalEvent? Current { get => _current; private set => Set(ref _current, value); }

    private FocalEvent? _next;
    public FocalEvent? Next { get => _next; private set => Set(ref _next, value); }

    private List<FocalEvent> _todayEvents = [];
    public List<FocalEvent> TodayEvents { get => _todayEvents; private set => Set(ref _todayEvents, value); }

    private bool _isReachable = true;
    public bool IsReachable { get => _isReachable; private set => Set(ref _isReachable, value); }

    private bool _isReplanning;
    public bool IsReplanning { get => _isReplanning; private set => Set(ref _isReplanning, value); }

    private string? _lastError;
    public string? LastError { get => _lastError; private set => Set(ref _lastError, value); }

    // Ticks every second so countdown labels re-evaluate
    private DateTime _tick = DateTime.Now;
    public DateTime Tick { get => _tick; private set => Set(ref _tick, value); }

    public FocacalStore(DispatcherQueue ui)
    {
        _ui       = ui;
        Settings  = AppSettings.Load();
        _api      = new FocacalApi(Settings.ServerUrl);

        _pollTimer = new System.Timers.Timer(Settings.PollInterval * 1000);
        _pollTimer.Elapsed += async (_, _) => await RefreshAll();
        _pollTimer.AutoReset = true;
        _pollTimer.Start();

        _countdownTimer = new System.Timers.Timer(1000);
        _countdownTimer.Elapsed += (_, _) => Dispatch(() => Tick = DateTime.Now);
        _countdownTimer.AutoReset = true;
        _countdownTimer.Start();

        // Initial load
        _ = RefreshAll();
    }

    // --- Actions ---

    public async Task RefreshAll()
    {
        await Task.WhenAll(RefreshNow(), RefreshToday());
    }

    public async Task<bool> Replan()
    {
        Dispatch(() => IsReplanning = true);
        var ok = await _api.Replan();
        await RefreshAll();
        Dispatch(() => IsReplanning = false);
        return ok;
    }

    public void ApplySettings(AppSettings updated)
    {
        Settings = updated;
        Settings.Save();
        _api = new FocacalApi(updated.ServerUrl);
        _pollTimer.Interval = updated.PollInterval * 1000;
        _ = RefreshAll();
    }

    // --- Internal ---

    private async Task RefreshNow()
    {
        var r = await _api.GetNow();
        Dispatch(() =>
        {
            IsReachable = r is not null;
            LastError   = r is null ? "Cannot reach focacal — check server URL in Settings" : null;
            Current     = r?.Current;
            Next        = r?.Next;
        });
    }

    private async Task RefreshToday()
    {
        var r = await _api.GetToday();
        if (r is not null)
            Dispatch(() => TodayEvents = r.Events);
    }

    private void Dispatch(Action a) => _ui.TryEnqueue(a.Invoke);

    // --- INotifyPropertyChanged ---

    public event PropertyChangedEventHandler? PropertyChanged;

    private void Set<T>(ref T field, T value, [CallerMemberName] string? name = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value)) return;
        field = value;
        Dispatch(() => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name)));
    }

    public void Dispose()
    {
        _pollTimer.Dispose();
        _countdownTimer.Dispose();
    }
}
