import Foundation
@preconcurrency import TendshelfShared

/// The stored text of a downloaded book (decision 0048, I3d; the text half of `Offline.kt`): one Download (0019, point 1), a text
/// that waits for the web view's session (`signin`) and goes on at the next server page, a daily check, and the Read button.
extension Downloads {
    /// Whether the book's text is whole, so the Downloads page offers Read.
    func readable(_ id: String) -> Bool { store.text(id)?.state == TextResult.ready.rawValue }

    /// A book with text and no audio: one row, ready once its text is whole. It can be read, never played.
    func addTextOnly(_ id: String, _ conn: Connection, _ service: PlayerAPI, manifest: BookManifest, manifestJson: String) async {
        let got = await text.manifest(conn.origin, id)
        var known: TextManifest?
        switch got {
        case .ok(let m): known = m
        case .not(.signin): known = nil
        default: return refuse(Words.Key.downloadNothing)
        }
        try? paths.prepare()
        store.reopen()
        if let bytes = await service.cover(token: conn.token, bookId: id), let cover = paths.cover(id) { write(bytes, to: cover) }
        let row = OfflineBook(
            bookId: id, userId: userId() ?? "", title: known.flatMap { $0.title.isEmpty ? nil : $0.title } ?? manifest.title,
            author: known.flatMap { $0.author.isEmpty ? nil : $0.author } ?? manifest.author, durationMs: 0, sizeBytes: known?.bytes ?? 0,
            state: .ready, placeMs: 0, placeAt: 0, kind: OfflineKt.KIND_BOOK, showId: "", publishedAt: 0, played: false)
        store.put(row, manifestJson, at: now())
        // No session cookie yet: the row is kept and the text goes on at the next page load; anything else is a refusal.
        guard let known else {
            store.setText(id, state: TextResult.signin.rawValue, revision: "", edition: "")
            changed(id)
            return
        }
        if await fetchText(id, known: known) != .ready {
            store.remove(id: id)
            if let cover = paths.cover(id) { try? FileManager.default.removeItem(at: cover) }
            text.remove(id)
            refuse(Words.Key.downloadFailed)
        }
        changed(id)
    }

    /// Fetches a book's text and records how it went. [known] saves asking for the manifest twice.
    @discardableResult
    func fetchText(_ id: String, known: TextManifest? = nil) async -> TextResult {
        guard let conn = connection(), let had = store.text(id) else { return .failed }
        // A second request for the same book is the first one's business.
        guard !textBusy.contains(id) else { return .failed }
        textBusy.insert(id)
        defer { textBusy.remove(id) }
        store.setText(id, state: "fetching", revision: had.revision, edition: had.edition)
        changed(id)
        var result = TextResult.failed
        var revision = ""
        var edition = ""
        let tm: TextManifest?
        let answer: TextStore.ManifestAnswer
        if let known { answer = .ok(known) } else { answer = await text.manifest(conn.origin, id) }
        switch answer {
        case .ok(let m): tm = m
        case .code: tm = nil
        case .not(let r):
            store.setText(id, state: r.rawValue, revision: "", edition: "")
            changed(id)
            return r
        }
        if let tm {
            result = await text.fetch(conn.origin, tm, freeBytes: free(true))
            if result == .ready {
                revision = tm.revision
                edition = tm.editionId
            }
        } else {
            result = .none
        }
        // A book removed while its text came in leaves nothing behind.
        if store.get(id: id) == nil {
            text.remove(id)
            return result
        }
        store.setText(id, state: result.rawValue, revision: revision, edition: edition)
        changed(id)
        return result
    }

    /// A book whose text stopped at sign-in or failed is tried again (at the next start, and for sign-in at the next page).
    private func retryText(_ id: String) async {
        if isAuto(id), !unmetered() { return }
        guard let now = store.text(id)?.state else { return }
        if now == TextResult.signin.rawValue || now == TextResult.failed.rawValue || now == "fetching" { await fetchText(id) }
    }

    /// A server page loaded: the session exists, so a text that waited for it goes on. Cheap when nothing waits.
    func pageLoaded() async {
        for id in store.withText(TextResult.signin.rawValue) { await retryText(id) }
    }

    /// At the front: every text that stopped is tried again.
    func retryStoppedText() async {
        for state in [TextResult.signin.rawValue, TextResult.failed.rawValue, "fetching"] {
            for id in store.withText(state) { await retryText(id) }
        }
    }

    /// The text's own daily check (0019, point 10): other revision, fetch again with the shell last; 404, drop it; the rest changes nothing.
    func checkText(_ id: String) async {
        guard let conn = connection(), let had = store.text(id), had.state == TextResult.ready.rawValue else { return }
        switch await text.manifest(conn.origin, id) {
        case .ok(let m):
            if m.revision != had.revision { await fetchText(id, known: m) }
        case .code:
            text.remove(id)
            store.setText(id, state: TextResult.none.rawValue, revision: "", edition: "")
            // A text-only row has nothing left to offer.
            if store.files(id).isEmpty { await remove(id, byPerson: false) }
        case .not: break
        }
    }
}

/// The reading places the stored reader kept for the page (`rd`): the current person's, oldest first.
extension Downloads: ReadingControl {
    func readingItems() -> [ReadingItem] {
        guard let user = userId() else { return [] }
        return store.readingRows(userId: user, limit: Int(BridgeProtocolKt.MAX_READING_ITEMS), bytes: 256 * 1024)
    }

    func readingDone(_ seq: Int64) {
        guard let user = userId() else { return }
        store.deleteReading(userId: user, upTo: seq)
    }
}
