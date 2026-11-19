import Foundation
import OSLog
import UIKit
import WidgetKit
@preconcurrency import TendshelfShared

/// Where the feed puts what it decided: the app group's files and the widget's reload. Injectable so tests need no app group.
struct WidgetWriter {
    var write: (WidgetSnapshot) -> Void
    var writeCover: (Data?) -> Void
    var clear: () -> Void
    var reload: () -> Void

    static let live = WidgetWriter(
        write: { WidgetStore.write($0) },
        writeCover: { WidgetStore.writeCover($0) },
        clear: { WidgetStore.clear() },
        reload: { WidgetCenter.shared.reloadTimelines(ofKind: "TendshelfNowPlaying") })
}

/// Writes what the home-screen widget draws (decision 0048, I4b; the widget's own side is `TendshelfWidget`). The app decides the
/// looks with the shared `widgetLook`, so the logic exists once; the extension only draws the file. Titles and times only.
@MainActor
final class WidgetFeed {
    /// The small and medium widget sizes of an iPhone 17, in points.
    static let smallSize = (width: 170, height: 170)
    static let mediumSize = (width: 364, height: 170)
    static let coverPixels: CGFloat = 360

    private let writer: WidgetWriter
    private var last: WidgetSnapshot?
    private var coverItem: String?
    private var coverMissing = false

    init(writer: WidgetWriter = .live) {
        self.writer = writer
    }

    /// The state for [input], written (and the widget reloaded) only when it differs from the last one written.
    /// [art] is the item's cover bytes; the cover file is rewritten when the item changes.
    func update(_ input: WidgetInput, art: Data?) {
        let small = WidgetKt.widgetLook(input: input, widthDp: Int32(Self.smallSize.width), heightDp: Int32(Self.smallSize.height))
        let medium = WidgetKt.widgetLook(input: input, widthDp: Int32(Self.mediumSize.width), heightDp: Int32(Self.mediumSize.height))
        let kind: String
        switch medium.kind {
        case .signedOut: kind = WidgetSnapshot.signedOutKind
        case .noBook: kind = WidgetSnapshot.noBookKind
        default: kind = WidgetSnapshot.bookKind
        }
        let isBook = kind == WidgetSnapshot.bookKind
        let line: String
        switch kind {
        case WidgetSnapshot.signedOutKind: line = words("widget_signed_out")
        case WidgetSnapshot.noBookKind: line = words("widget_no_book")
        default: line = ""
        }
        let snapshot = WidgetSnapshot(
            kind: kind, itemId: isBook ? input.bookId : "", title: medium.title, chapter: medium.chapter, playing: medium.playing,
            small: Self.out(small), medium: Self.out(medium), line: line,
            labels: WidgetLabels(
                play: words("widget_play"), pause: words("widget_pause"),
                back: wordsPlural("widget_back", Int(medium.skipBackS)), forward: wordsPlural("widget_forward", Int(medium.skipForwardS)),
                open: isBook ? words("widget_open_book", medium.title) : WidgetWords.label))
        let item = isBook ? input.bookId : ""
        // The cover is rewritten when the item changes, or when it was missing and the player has its picture now.
        var coverChanged = false
        if item != coverItem || (coverMissing && art != nil) {
            coverChanged = true
            let cover = isBook ? Self.scaledCover(art) : nil
            coverItem = item
            coverMissing = isBook && cover == nil
            writer.writeCover(cover)
        }
        guard snapshot != last || coverChanged else { return }
        last = snapshot
        writer.write(snapshot)
        writer.reload()
    }

    /// The player's current state as the widget's input; the show line, the live word and the chapter follow `NowLines`' rules.
    func update(_ p: AudioPlayer, signedIn: Bool) {
        let episode = PodcastKt.isEpisodeId(id: p.bookId)
        let chapter: String
        if p.live || episode {
            chapter = ""
        } else {
            let idx = p.chapterIdx
            chapter = p.chapters.indices.contains(idx) ? p.chapters[idx].title : ""
        }
        let input = WidgetInput(
            signedIn: signedIn, bookId: p.bookId, title: p.episodeTitle ?? p.title, chapter: chapter, playing: p.snapshot().playing,
            skipBackS: Int32(p.settings?.skipBackS ?? 30), skipForwardS: Int32(p.settings?.skipForwardS ?? 30),
            show: p.live ? p.liveWord : (episode ? p.title : ""), live: p.live)
        update(input, art: p.art)
    }

    /// Signed out: nothing of the person stays (the state and the cover), and the widget says so.
    func signedOut() {
        writer.clear()
        last = nil
        coverItem = nil
        update(WidgetInput(signedIn: false, bookId: "", title: "", chapter: "", playing: false, skipBackS: 30, skipForwardS: 30, show: "", live: false), art: nil)
    }

    /// Signed in with no book on the phone's widget yet: "Pick a book". A book already written stays, so a later Play can resume it.
    func ensureSignedIn() {
        if last == nil, let stored = WidgetStore.read(), stored.kind == WidgetSnapshot.bookKind {
            last = stored
            coverItem = stored.itemId
            return
        }
        if last == nil || last?.kind == WidgetSnapshot.signedOutKind {
            update(WidgetInput(signedIn: true, bookId: "", title: "", chapter: "", playing: false, skipBackS: 30, skipForwardS: 30, show: "", live: false), art: nil)
        }
    }

    private static func out(_ l: WidgetLook) -> WidgetLookOut {
        WidgetLookOut(showCover: l.showCover, showChapter: l.showChapter, showSkip: l.showSkip)
    }

    /// The cover scaled so its longer side is [coverPixels], as a JPEG; nil when there is none or it cannot be read.
    static func scaledCover(_ art: Data?) -> Data? {
        guard let art, let image = UIImage(data: art), image.size.width > 0, image.size.height > 0 else { return nil }
        let scale = min(1, coverPixels / max(image.size.width, image.size.height))
        let size = CGSize(width: (image.size.width * scale).rounded(), height: (image.size.height * scale).rounded())
        let format = UIGraphicsImageRendererFormat.default()
        format.scale = 1
        let scaled = UIGraphicsImageRenderer(size: size, format: format).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }
        return scaled.jpegData(compressionQuality: 0.8)
    }
}
