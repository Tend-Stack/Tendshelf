import AppIntents
import SwiftUI
import WidgetKit

@main
struct TendshelfWidgetBundle: WidgetBundle {
    var body: some Widget {
        NowPlayingWidget()
    }
}

/// One entry: what the app last wrote, or nothing. The extension makes no request and holds no token.
struct NowPlayingEntry: TimelineEntry {
    let date: Date
    let snapshot: WidgetSnapshot
    let cover: UIImage?

    static let preview = NowPlayingEntry(
        date: .now,
        snapshot: WidgetSnapshot(
            kind: WidgetSnapshot.bookKind, itemId: "preview", title: WidgetWords.previewTitle, chapter: WidgetWords.previewChapter,
            playing: false, small: WidgetLookOut(showCover: true, showChapter: false, showSkip: false),
            medium: WidgetLookOut(showCover: true, showChapter: true, showSkip: true), line: "",
            labels: WidgetLabels(
                play: WidgetWords.play, pause: WidgetWords.pause, back: WidgetWords.back, forward: WidgetWords.forward,
                open: WidgetWords.previewTitle)),
        cover: nil)

    static func signedOut() -> NowPlayingEntry {
        NowPlayingEntry(
            date: .now,
            snapshot: WidgetSnapshot(
                kind: WidgetSnapshot.signedOutKind, itemId: "", title: "", chapter: "", playing: false,
                small: WidgetLookOut(showCover: false, showChapter: false, showSkip: false),
                medium: WidgetLookOut(showCover: false, showChapter: false, showSkip: false), line: WidgetWords.signedOut,
                labels: WidgetLabels(play: "", pause: "", back: "", forward: "", open: WidgetWords.label)),
            cover: nil)
    }
}

struct NowPlayingProvider: TimelineProvider {
    func placeholder(in context: Context) -> NowPlayingEntry { .preview }

    func getSnapshot(in context: Context, completion: @escaping (NowPlayingEntry) -> Void) {
        completion(context.isPreview ? .preview : current())
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<NowPlayingEntry>) -> Void) {
        completion(Timeline(entries: [current()], policy: .never))
    }

    private func current() -> NowPlayingEntry {
        guard let snapshot = WidgetStore.read() else { return .signedOut() }
        let cover = WidgetStore.coverURL().flatMap { try? Data(contentsOf: $0) }.flatMap { UIImage(data: $0) }
        return NowPlayingEntry(date: .now, snapshot: snapshot, cover: cover)
    }
}

struct NowPlayingWidget: Widget {
    static let kind = "TendshelfNowPlaying"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: Self.kind, provider: NowPlayingProvider()) { entry in
            NowPlayingView(entry: entry)
                .containerBackground(.background, for: .widget)
        }
        .configurationDisplayName(WidgetWords.label)
        .description(WidgetWords.description)
        .supportedFamilies([.systemSmall, .systemMedium])
    }
}

struct NowPlayingView: View {
    @Environment(\.widgetFamily) private var family
    let entry: NowPlayingEntry

    private var snapshot: WidgetSnapshot { entry.snapshot }
    private var look: WidgetLookOut { family == .systemSmall ? snapshot.small : snapshot.medium }

    var body: some View {
        if snapshot.kind == WidgetSnapshot.bookKind {
            book
        } else {
            Button(intent: OpenItemIntent(itemId: "")) {
                Text(snapshot.line)
                    .font(.subheadline)
                    .multilineTextAlignment(.leading)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
            .buttonStyle(.plain)
        }
    }

    private var book: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button(intent: OpenItemIntent(itemId: snapshot.itemId)) {
                HStack(alignment: .top, spacing: 10) {
                    if look.showCover { cover }
                    VStack(alignment: .leading, spacing: 2) {
                        Text(snapshot.title).font(.headline).lineLimit(2)
                        if look.showChapter, !snapshot.chapter.isEmpty {
                            Text(snapshot.chapter).font(.subheadline).foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(maxHeight: .infinity, alignment: .top)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(snapshot.labels.open)
            controls
        }
    }

    @ViewBuilder private var cover: some View {
        let side: CGFloat = family == .systemSmall ? 56 : 72
        if let image = entry.cover {
            Image(uiImage: image).resizable().scaledToFill().frame(width: side, height: side).clipShape(RoundedRectangle(cornerRadius: 8))
        } else {
            RoundedRectangle(cornerRadius: 8).fill(.fill.tertiary).frame(width: side, height: side)
        }
    }

    private var controls: some View {
        HStack(spacing: 8) {
            if look.showSkip {
                Button(intent: SkipIntent(forward: false)) { glyph("gobackward") }
                    .accessibilityLabel(snapshot.labels.back)
            }
            Button(intent: TogglePlayIntent()) { glyph(snapshot.playing ? "pause.fill" : "play.fill") }
                .accessibilityLabel(snapshot.playing ? snapshot.labels.pause : snapshot.labels.play)
            if look.showSkip {
                Button(intent: SkipIntent(forward: true)) { glyph("goforward") }
                    .accessibilityLabel(snapshot.labels.forward)
            }
        }
        .buttonStyle(.plain)
        .frame(maxWidth: .infinity, alignment: .center)
    }

    private func glyph(_ name: String) -> some View {
        Image(systemName: name)
            .font(.title3)
            .frame(minWidth: 44, minHeight: 44)
            .contentShape(Rectangle())
    }
}
