using System.Text.Json.Serialization;

namespace FocacalTray;

public record FocalEvent(
    [property: JsonPropertyName("id")]       string Id,
    [property: JsonPropertyName("title")]    string Title,
    [property: JsonPropertyName("start")]    DateTime Start,
    [property: JsonPropertyName("end")]      DateTime End,
    [property: JsonPropertyName("kind")]     string Kind,
    [property: JsonPropertyName("is_locked")] bool IsLocked
)
{
    public bool IsActive    => Start <= DateTime.Now && End > DateTime.Now;
    public bool IsFuture    => Start > DateTime.Now;
    public TimeSpan Remaining => End - DateTime.Now;
    public TimeSpan Duration  => End - Start;

    public string SymbolGlyph => Kind switch
    {
        "focus" => "\uE945",   // Brain / focus icon (Segoe Fluent Icons)
        "lunch" => "\uEAC2",   // Restaurant
        "break" => "\uE780",   // Coffee
        _       => "\uE787",   // Calendar
    };
}

public record NowResponse(
    [property: JsonPropertyName("current")] FocalEvent? Current,
    [property: JsonPropertyName("next")]    FocalEvent? Next
);

public record TodayResponse(
    [property: JsonPropertyName("date")]   string Date,
    [property: JsonPropertyName("events")] List<FocalEvent> Events
);

public record HealthResponse(
    [property: JsonPropertyName("status")]      string Status,
    [property: JsonPropertyName("last_replan")] DateTime? LastReplan
);
