import Foundation
@preconcurrency import TendshelfShared

/// A request the player made that the server did not answer well: [status] is the HTTP status, or 0 for no answer, an
/// answer that was too large or one that was not the expected shape. [retryAfterS] is the server's wait (429 and 503).
nonisolated struct ApiError: Error, Equatable {
    let status: Int
    var retryAfterS = 0
}

/// The calls the player makes, port of what `Api.kt` gives the Android player. A fake stands in for it in the tests.
/// Every call carries the token as an `Authorization` header to an `/api/v1/` address of the pinned origin.
protocol PlayerAPI: AnyObject {
    func settings(token: String) async throws -> PlayerSettings
    func manifest(token: String, bookId: String) async throws -> BookManifest
    /// The manifest's own text, kept as the row's answer so the book starts with no network (and parsed with `manifest`).
    func manifestText(token: String, bookId: String) async throws -> String
    func episodeText(token: String, id: String) async throws -> String
    func chaptersText(token: String, path: String) async -> String?
    func cover(token: String, bookId: String) async -> Data?
    func episode(token: String, id: String) async throws -> EpisodeDoc
    func chapters(token: String, path: String, durationMs: Int64) async -> [Chapter]
    func podcastArt(token: String, artPath: String) async -> Data?
    func radioStation(token: String, id: String) async throws -> StationCard
    func radioArt(token: String, id: String, artPath: String) async -> Data?
    func radioNow(token: String, id: String) async -> String?
    func liveAddress(token: String, id: String, origin: Origin) async throws -> URL
    func progress(token: String, bookId: String) async throws -> RemoteProgress?
    func queueNext(token: String, ended: String) async throws -> String?
    /// The read-only peek at the next item after [ended] (decision 0035): nothing is taken off the queue. Nil: none.
    func queuePeek(token: String, ended: String) async throws -> String?
    /// `DELETE /api/v1/queue/{id}`: the status, for `queueDeleteDone` to judge; a failure to connect is an error.
    func queueRemove(token: String, bookId: String) async throws -> Int
    /// The followed shows (their automatic-download choice is `keepLatest`), and the newest episodes of one show.
    func podcastShows(token: String) async throws -> [ShowCard]
    func showEpisodes(token: String, showId: String) async throws -> [EpisodeCard]
    func putSpeed(token: String, bookId: String, speed: Double) async throws
    func batch(token: String, saves: [Save], deviceId: String) async throws -> [BatchItemResult]
}

/// The real thing over `ServerClient`. One instance per origin; redirects stay refused (Android follows one same-origin
/// 301/308 on GET; none is expected on these routes).
final class PlayerApi: PlayerAPI {
    let paths: ApiPaths
    private let client: ServerClient

    init(origin: Origin, client: ServerClient) {
        paths = ApiPaths(server: origin.text)
        self.client = client
    }

    // MARK: Books

    func settings(token: String) async throws -> PlayerSettings {
        let body = try await get(paths.settingsUrl, token, 64 * 1024)
        guard let s = PlayerParseKt.parseSettings(body: Self.text(body)) else { throw ApiError(status: 0) }
        return s
    }

    func manifest(token: String, bookId: String) async throws -> BookManifest {
        let text = try await manifestText(token: token, bookId: bookId)
        guard let m = PlayerParseKt.parseManifest(body: text, paths: paths) else { throw ApiError(status: 0) }
        return m
    }

    func manifestText(token: String, bookId: String) async throws -> String {
        guard BridgeProtocolKt.validId(s: bookId) else { throw ApiError(status: 404) }
        return Self.text(try await get(paths.bookUrl(bookId: bookId), token, 4 * 1024 * 1024))
    }

    func cover(token: String, bookId: String) async -> Data? {
        guard BridgeProtocolKt.validId(s: bookId) else { return nil }
        return try? await get(paths.coverUrl(bookId: bookId, width: 512), token, 2 * 1024 * 1024)
    }

    func progress(token: String, bookId: String) async throws -> RemoteProgress? {
        guard BridgeProtocolKt.validId(s: bookId) else { throw ApiError(status: 404) }
        let body = try await get(paths.progressUrl(bookId: bookId), token, 64 * 1024)
        guard let reply = PlayerParseKt.parseProgress(body: Self.text(body)) else { throw ApiError(status: 0) }
        return reply.progress
    }

    func putSpeed(token: String, bookId: String, speed: Double) async throws {
        guard BridgeProtocolKt.validId(s: bookId) else { throw ApiError(status: 404) }
        _ = try await send(paths.speedUrl(bookId: bookId), "PUT", "{\"speed\":\(speed)}", token, 64 * 1024)
    }

    func queueNext(token: String, ended: String) async throws -> String? {
        let body = try await send(paths.queueNextUrl, "POST", ApiPathsKt.queueNextBody(endedBookId: ended), token, 64 * 1024)
        if body.isEmpty { return nil }
        guard let id = PlayerParseKt.parseNextItemId(body: Self.text(body)) else { throw ApiError(status: 0) }
        return id
    }

    func queuePeek(token: String, ended: String) async throws -> String? {
        guard BridgeProtocolKt.validId(s: ended) else { throw ApiError(status: 404) }
        let body = try await get(paths.queuePeekUrl(ended: ended), token, 64 * 1024)
        if body.isEmpty { return nil }
        guard let id = PlayerParseKt.parseNextItemId(body: Self.text(body)) else { throw ApiError(status: 0) }
        return id
    }

    func queueRemove(token: String, bookId: String) async throws -> Int {
        guard BridgeProtocolKt.validId(s: bookId) else { throw ApiError(status: 404) }
        do {
            return try await client.callData(paths.queueEntryUrl(bookId: bookId), "DELETE", nil, token, 20, limit: 16 * 1024)?.status ?? 0
        } catch {
            throw ApiError(status: 0)
        }
    }

    // MARK: Podcasts

    func podcastShows(token: String) async throws -> [ShowCard] {
        let body = try await get(paths.showsUrl, token, 1024 * 1024)
        guard let list = PlayerParseKt.parseShowCards(body: Self.text(body)) else { throw ApiError(status: 0) }
        return list
    }

    func showEpisodes(token: String, showId: String) async throws -> [EpisodeCard] {
        guard BridgeProtocolKt.validId(s: showId) else { throw ApiError(status: 404) }
        let body = try await get(paths.showEpisodesUrl(showId: showId, limit: 50), token, 2 * 1024 * 1024)
        guard let list = PlayerParseKt.parseEpisodeCards(body: Self.text(body)) else { throw ApiError(status: 0) }
        return list
    }

    func episode(token: String, id: String) async throws -> EpisodeDoc {
        let text = try await episodeText(token: token, id: id)
        guard let doc = PlayerParseKt.parseEpisode(body: text) else { throw ApiError(status: 0) }
        return doc
    }

    func episodeText(token: String, id: String) async throws -> String {
        guard BridgeProtocolKt.validId(s: id) else { throw ApiError(status: 404) }
        return Self.text(try await get(paths.episodeUrl(episodeId: id), token, 256 * 1024))
    }

    func chaptersText(token: String, path: String) async -> String? {
        guard let url = paths.podcastUrl(path: path, width: 0), let body = try? await get(url, token, 512 * 1024) else { return nil }
        return Self.text(body)
    }

    func chapters(token: String, path: String, durationMs: Int64) async -> [Chapter] {
        guard let url = paths.podcastUrl(path: path, width: 0), let body = try? await get(url, token, 512 * 1024) else { return [] }
        return PlayerParseKt.parseEpisodeChapters(body: Self.text(body), durationMs: durationMs) ?? []
    }

    func podcastArt(token: String, artPath: String) async -> Data? {
        guard let url = paths.podcastUrl(path: artPath, width: 512) else { return nil }
        return try? await get(url, token, 2 * 1024 * 1024)
    }

    // MARK: Radio

    /// The station's card from the person's stations or the recent ones; a station that is in neither is a 404.
    func radioStation(token: String, id: String) async throws -> StationCard {
        guard BridgeProtocolKt.validId(s: id) else { throw ApiError(status: 404) }
        for url in [paths.radioStationsUrl, paths.radioRecentUrl] {
            let body = try await get(url, token, 1024 * 1024)
            guard let list = PlayerParseKt.parseStations(body: Self.text(body)) else { throw ApiError(status: 0) }
            if let card = list.first(where: { $0.id == id }) { return card }
        }
        throw ApiError(status: 404)
    }

    func radioArt(token: String, id: String, artPath: String) async -> Data? {
        guard BridgeProtocolKt.validId(s: id) else { return nil }
        let url = (artPath.isEmpty ? nil : paths.radioUrl(path: artPath)) ?? paths.radioArtUrl(stationId: id)
        return try? await get(url, token, 2 * 1024 * 1024)
    }

    func radioNow(token: String, id: String) async -> String? {
        guard BridgeProtocolKt.validId(s: id), let body = try? await get(paths.radioNowUrl(stationId: id), token, 16 * 1024) else { return nil }
        return PlayerParseKt.parseRadioNow(body: Self.text(body))
    }

    /// Asks for the one-hour live address of a station (decision 0049): a POST with the bearer, answered with a path that is
    /// checked and joined with the pinned origin. The address is a credential: it is never logged or kept.
    func liveAddress(token: String, id: String, origin: Origin) async throws -> URL {
        guard BridgeProtocolKt.validId(s: id) else { throw ApiError(status: 404) }
        let body = try await send(paths.liveAddressUrl(stationId: id), "POST", "{}", token, 4 * 1024)
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let path = object["path"] as? String,
              let url = Self.liveURL(origin: origin, path: path)
        else { throw ApiError(status: 0) }
        return url
    }

    /// The address only when [path] has the one shape the server answers: `/cast/<43 characters>/live`, nothing else.
    static func liveURL(origin: Origin, path: String) -> URL? {
        guard path.range(of: #"^/cast/[A-Za-z0-9_-]{43}/live\z"#, options: .regularExpression) != nil else { return nil }
        return URL(string: origin.text + path)
    }

    // MARK: Saves

    /// Port of `Api.kt:54-84`: books go to the book batch, episodes to the podcast batch (a server without it answers 404,
    /// so those rows come back `not_found` and are dropped), and the answers are put back in the order sent.
    func batch(token: String, saves: [Save], deviceId: String) async throws -> [BatchItemResult] {
        let split = PodcastKt.splitSaves(saves: saves)
        let books = split.first as? [Save] ?? []
        let episodes = split.second as? [Save] ?? []
        if episodes.isEmpty {
            return try await bookBatch(token, saves)
        }
        var failure: Error?
        var bookResults: [BatchItemResult]?
        if !books.isEmpty {
            do { bookResults = try await bookBatch(token, books) } catch { failure = error }
        }
        var episodeResults: [BatchItemResult]?
        do {
            episodeResults = try await episodeBatch(token, episodes, deviceId)
        } catch {
            failure = failure ?? error
        }
        let out = PodcastKt.assembleResults(saves: saves, books: books.isEmpty ? [] : bookResults, episodes: episodeResults)
        if out.isEmpty { throw failure ?? ApiError(status: 0) }
        return out
    }

    private func bookBatch(_ token: String, _ saves: [Save]) async throws -> [BatchItemResult] {
        let body = try await send(paths.batchUrl, "POST", ApiPathsKt.batchBody(saves: saves), token, 1024 * 1024)
        guard let results = PlayerParseKt.parseBookBatch(body: Self.text(body)) else { throw ApiError(status: 0) }
        return results
    }

    private func episodeBatch(_ token: String, _ saves: [Save], _ deviceId: String) async throws -> [BatchItemResult] {
        do {
            let body = try await send(paths.podcastBatchUrl, "POST", PodcastKt.podcastBatchBody(saves: saves, deviceId: deviceId), token, 1024 * 1024)
            guard let results = PlayerParseKt.parseEpisodeBatch(body: Self.text(body)) else { throw ApiError(status: 0) }
            return results
        } catch let e as ApiError where e.status == 404 {
            return saves.map { BatchItemResult(bookId: $0.bookId, applied: false, errorCode: "not_found", progress: nil) }
        }
    }

    // MARK: HTTP

    private static func text(_ data: Data) -> String { String(decoding: data, as: UTF8.self) }

    private func get(_ url: String, _ token: String, _ limit: Int) async throws -> Data {
        try await run(url, "GET", nil, token, limit)
    }

    private func send(_ url: String, _ method: String, _ json: String, _ token: String, _ limit: Int) async throws -> Data {
        try await run(url, method, json, token, limit)
    }

    private func run(_ url: String, _ method: String, _ json: String?, _ token: String, _ limit: Int) async throws -> Data {
        let reply: ServerClient.DataReply?
        do {
            reply = try await client.callData(url, method, json, token, 20, limit: limit)
        } catch {
            throw ApiError(status: 0)
        }
        guard let reply else { throw ApiError(status: 0) }
        if reply.status == 429 { throw ApiError(status: 429, retryAfterS: min(max(reply.retryAfterS ?? 30, 1), 300)) }
        guard (200..<300).contains(reply.status) else { throw ApiError(status: reply.status, retryAfterS: reply.retryAfterS ?? 0) }
        if reply.tooLarge { throw ApiError(status: 0) }
        return reply.body
    }
}
