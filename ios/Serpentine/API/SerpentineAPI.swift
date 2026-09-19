import Foundation

/// The app's only network peer (CLAUDE.md principle 1). Ephemeral session: no cookies, no disk
/// cache, so nothing about planned rides persists outside the app.
struct SerpentineAPI: Sendable {
    static let production = SerpentineAPI(base: URL(string: "https://serpentine.phfactor.net/v1/")!)

    let base: URL
    private let session: URLSession

    init(base: URL) {
        self.base = base
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 90 // loops fan out 16 routes; charging adds an NREL call
        session = URLSession(configuration: config)
    }

    struct Failure: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        return e
    }

    static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return d
    }

    func plan(_ request: PlanRequest) async throws -> PlanResult {
        var req = URLRequest(url: base.appending(path: "plan"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try Self.encoder().encode(request)
        let data = try await send(req)
        return try Self.decoder().decode(PlanResult.self, from: data)
    }

    /// Downloads the plan's GPX to a temporary file, for the share sheet.
    func downloadGPX(for plan: PlanResult) async throws -> URL {
        guard let url = URL(string: plan.gpxUrl, relativeTo: base) else {
            throw Failure(message: "bad GPX link")
        }
        let data = try await send(URLRequest(url: url))
        let file = FileManager.default.temporaryDirectory.appending(path: "serpentine-\(plan.id).gpx")
        try data.write(to: file, options: .atomic)
        return file
    }

    private func send(_ req: URLRequest) async throws -> Data {
        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw Failure(message: "no response") }
        guard (200..<300).contains(http.statusCode) else {
            // The API answers errors as {"error": "..."}; 422 means "can't route this", not a bug.
            let body = try? JSONDecoder().decode([String: String].self, from: data)
            throw Failure(message: body?["error"] ?? "server error \(http.statusCode)")
        }
        return data
    }
}
