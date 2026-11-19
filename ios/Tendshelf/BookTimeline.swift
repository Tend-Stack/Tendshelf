import Foundation
@preconcurrency import TendshelfShared

/// Where book time lands among a book's files (one `AVPlayerItem` per playable file). Pure: no AVFoundation, so it is tested
/// without a player. A file the server lacks (`playable` false) is skipped with the shared `skipSilence` rule, which is where
/// Android's silence lands too.
nonisolated struct BookTimeline {
    let files: [AudioFile]
    private let silent: [KotlinLongRange]

    init(files: [AudioFile]) {
        self.files = files
        silent = PositionKt.silentRanges(files: files)
    }

    /// File indexes that can be played, in order.
    var playable: [Int] { files.indices.filter { files[$0].playable } }

    /// Book time of [itemMs] milliseconds into file [fileIndex].
    func bookMs(fileIndex: Int, itemMs: Int64) -> Int64 {
        guard files.indices.contains(fileIndex) else { return 0 }
        return Position.shared.bookPosition(files: files, fileIndex: Int32(fileIndex), positionMs: itemMs)
    }

    /// The file and place for book time [bookMs], after the missing files are stepped over. Nil when that lies past the last
    /// playable file: the book is over.
    func target(bookMs: Int64) -> (fileIndex: Int, inFileMs: Int64)? {
        guard let last = playable.last else { return nil }
        var ms = max(0, bookMs)
        if let moved = PositionKt.skipSilence(ranges: silent, bookMs: ms) { ms = moved.int64Value }
        let end = files[last].offsetMs + files[last].durationMs
        if ms >= end { return nil }
        let at = Position.shared.locate(files: files, bookMs: ms)
        var index = Int(at.fileIndex)
        var inFile = at.positionMs
        if !files[index].playable {
            // Not reachable after the step-over, but a book with odd offsets must not play the wrong file.
            guard let after = playable.first(where: { $0 > index }) else { return nil }
            index = after
            inFile = 0
        }
        return (index, inFile)
    }

    /// The next playable file after [fileIndex].
    func next(after fileIndex: Int) -> Int? {
        playable.first(where: { $0 > fileIndex })
    }
}
