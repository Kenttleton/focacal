import Foundation

// MARK: - API client

final class FocacalAPI {
    var baseURL: String

    init(baseURL: String = "http://localhost:5233") {
        self.baseURL = baseURL
    }

    // MARK: Endpoints

    func now() async throws -> NowResponse {
        try await get("/now")
    }

    func today() async throws -> TodayResponse {
        try await get("/today")
    }

    func health() async throws -> HealthResponse {
        try await get("/health")
    }

    func replan() async throws -> ReplanResponse {
        try await post("/replan")
    }

    // MARK: Helpers

    private func get<T: Decodable>(_ path: String) async throws -> T {
        let url = try makeURL(path)
        let (data, response) = try await URLSession.shared.data(from: url)
        try validate(response, data: data)
        return try JSONDecoder.focacal.decode(T.self, from: data)
    }

    private func post<T: Decodable>(_ path: String) async throws -> T {
        let url = try makeURL(path)
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = Data("{}".utf8)
        let (data, response) = try await URLSession.shared.data(for: req)
        try validate(response, data: data)
        return try JSONDecoder.focacal.decode(T.self, from: data)
    }

    private func makeURL(_ path: String) throws -> URL {
        let raw = baseURL.hasSuffix("/") ? String(baseURL.dropLast()) : baseURL
        guard let url = URL(string: raw + path) else {
            throw APIError.invalidURL(raw + path)
        }
        return url
    }

    private func validate(_ response: URLResponse, data: Data) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200...299).contains(http.statusCode) else {
            let body = String(data: data, encoding: .utf8) ?? ""
            throw APIError.httpError(http.statusCode, body)
        }
    }
}

// MARK: - Errors

enum APIError: LocalizedError {
    case invalidURL(String)
    case httpError(Int, String)

    var errorDescription: String? {
        switch self {
        case .invalidURL(let s):       return "Invalid URL: \(s)"
        case .httpError(let c, let b): return "HTTP \(c): \(b)"
        }
    }
}
