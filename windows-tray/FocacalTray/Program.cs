using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.Windows.AppLifecycle;

// WinUI 3 unpackaged entry point.
// Must initialize Windows App SDK runtime before any WinUI code runs.

[STAThread]
static void Main(string[] args)
{
    WinRT.ComWrappersSupport.InitializeComWrappers();

    // Bootstrap the Windows App SDK runtime (required for unpackaged apps).
    Microsoft.Windows.ApplicationModel.DynamicDependency.Bootstrap.Initialize(0x00010005);

    Application.Start(p =>
    {
        var ctx = new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread());
        System.Threading.SynchronizationContext.SetSynchronizationContext(ctx);
        _ = new App();
    });
}
