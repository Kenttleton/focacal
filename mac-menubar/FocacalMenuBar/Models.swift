import Foundation

// MARK: - Calendar Event

struct FocalEvent: Identifiable, Decodable, Equatable {
    let id: String
    let title: String
    let start: Date
    let end: Date
    let kind: String      // focus | lunch | break | real
    let isLocked: Bool

    enum CodingKeys: String, CodingKey {
        case id, title, start, end, kind
        case isLocked = "is_locked"
    }

    var duration: TimeInterval { end.timeIntervalSince(start) }
    var remaining: TimeInterval { end.timeIntervalSinceNow }
    var isActive: Bool { start <= .now && end > .now }

    var isFocusType: Bool { kind == "focus" }
    var isReal: Bool { kind == "real" }

    var symbolName: String {
        switch kind {
        case "focus":  return "brain"
        case "lunch":  return "fork.knife"
        case "break":  return "cup.and.saucer"
        default:       return "calendar"
        }
    }
}

// MARK: - /now response

struct NowResponse: Decodable {
    let current: FocalEvent?
    let next: FocalEvent?
}

// MARK: - /today response

struct TodayResponse: Decodable {
    let date: String
    let events: [FocalEvent]
}

// MARK: - /health response

struct HealthResponse: Decodable {
    let status: String
    let lastReplan: Date?

    enum CodingKeys: String, CodingKey {
        case status
        case lastReplan = "last_replan"
    }
}

// MARK: - /replan response

struct ReplanResponse: Decodable {
    let status: String
}

// MARK: - Date decoding

extension JSONDecoder {
    static var focacal: JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }
}
