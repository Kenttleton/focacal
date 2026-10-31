using System.Net.Http.Json;
using System.Text.Json;

namespace FocacalTray;

public class FocacalApi(string baseUrl)
{
    private readonly HttpClient _http = new() { BaseAddress = new Uri(NormalizeUrl(baseUrl)) };

    private static readonly JsonSerializerOptions _json = new()
    {
        PropertyNameCaseInsensitive = true,
    };

    public string BaseUrl
    {
        get => _http.BaseAddress!.ToString().TrimEnd('/');
        set
        {
            _http.Dispose();
            // Reassign by replacing the field — called from FocacalStore when URL changes
        }
    }

    public Task<NowResponse?>   GetNow()    => Get<NowResponse>("/now");
    public Task<TodayResponse?> GetToday()  => Get<TodayResponse>("/today");
    public Task<HealthResponse?> GetHealth() => Get<HealthResponse>("/health");

    public async Task<bool> Replan()
    {
        try
        {
            var resp = await _http.PostAsync("/replan", new StringContent("{}", System.Text.Encoding.UTF8, "application/json"));
            return resp.IsSuccessStatusCode;
        }
        catch { return false; }
    }

    private async Task<T?> Get<T>(string path)
    {
        try
        {
            return await _http.GetFromJsonAsync<T>(path, _json);
        }
        catch { return default; }
    }

    private static string NormalizeUrl(string url)
    {
        url = url.TrimEnd('/');
        return url.EndsWith("/") ? url : url + "/";
    }
}
