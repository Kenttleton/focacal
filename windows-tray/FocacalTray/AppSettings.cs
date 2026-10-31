using System.Text.Json;

namespace FocacalTray;

/// <summary>
/// Persists settings to %APPDATA%\Focacal\settings.json.
/// Works for both packaged and unpackaged apps.
/// </summary>
public class AppSettings
{
    private static readonly string _path = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
        "Focacal", "settings.json");

    public string ServerUrl    { get; set; } = "http://localhost:5233";
    public int    PollInterval { get; set; } = 60;   // seconds
    public string Timezone     { get; set; } = TimeZoneInfo.Local.Id;

    public static AppSettings Load()
    {
        try
        {
            if (File.Exists(_path))
            {
                var json = File.ReadAllText(_path);
                return JsonSerializer.Deserialize<AppSettings>(json) ?? new AppSettings();
            }
        }
        catch { /* fall through to defaults */ }
        return new AppSettings();
    }

    public void Save()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(_path)!);
        File.WriteAllText(_path, JsonSerializer.Serialize(this, new JsonSerializerOptions { WriteIndented = true }));
    }
}
