package app.tendshelf.android

import android.os.Handler
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import app.tendshelf.shared.Chapter
import app.tendshelf.shared.Node
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.Position
import app.tendshelf.shared.nextChapterStart
import app.tendshelf.shared.parseNode
import app.tendshelf.shared.previousChapterStart

/**
 * The player every client sees (D3). The wrapped ExoPlayer holds one window in book time, so next and
 * previous mean chapters, skip means the person's interval, and the title is the chapter. Nothing here
 * knows which client is asking: the notification, the web bar and a later native screen all talk to it.
 */
@UnstableApi
class BookPlayer(private val exo: ExoPlayer) : ForwardingPlayer(exo) {
    private val main = Handler(Looper.getMainLooper())
    private val mine = mutableListOf<Player.Listener>()
    private var chapters: List<Chapter> = emptyList()
    private var settings = PlayerSettings(1.0, 30, 30, true, false)
    private var title = ""
    private var author = ""
    private var art: ByteArray? = null
    private var shownChapter = -1

    /** Set when the wrapped player failed; the next play prepares again from the same place. */
    var failed = false

    /** A book was chosen in a client's tree (A8, D3): the engine starts it. Null until the service wires it. */
    var onBookChosen: ((String) -> Unit)? = null

    /** Called about twice a second while playing, and after a seek; A5 saves progress from it. */
    var onTick: (() -> Unit)? = null

    private val tick = object : Runnable {
        override fun run() {
            checkChapter()
            onTick?.invoke()
            if (exo.isPlaying) main.postDelayed(this, TICK_MS)
        }
    }

    init {
        exo.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) {
                main.removeCallbacks(tick)
                if (isPlaying) main.post(tick)
            }

            override fun onPositionDiscontinuity(
                oldPosition: Player.PositionInfo, newPosition: Player.PositionInfo, reason: Int,
            ) = checkChapter()

            override fun onPlayerError(error: PlaybackException) {
                failed = true
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_READY) failed = false
            }
        })
    }

    /** Debug builds only: who asked for a speed (the stack names the caller), so a surprise speed can be traced. */
    private fun traceSpeed(what: String, speed: Float) {
        if (!BuildConfig.DEBUG) return
        val who = Throwable().stackTrace.drop(2).take(6).joinToString(" < ") { "${it.className.substringAfterLast('.')}.${it.methodName}" }
        android.util.Log.i("Tendshelf", "speed request $what=$speed by $who")
    }

    override fun setPlaybackSpeed(speed: Float) {
        traceSpeed("setPlaybackSpeed", speed)
        super.setPlaybackSpeed(speed)
    }

    override fun setPlaybackParameters(playbackParameters: androidx.media3.common.PlaybackParameters) {
        traceSpeed("setPlaybackParameters", playbackParameters.speed)
        super.setPlaybackParameters(playbackParameters)
    }

    fun load(title: String, author: String, chapters: List<Chapter>, settings: PlayerSettings, art: ByteArray?) {
        this.title = title
        this.author = author
        this.chapters = chapters
        this.settings = settings
        this.art = art
        shownChapter = -1
        notifyMetadata()
    }

    fun clear() {
        chapters = emptyList()
        title = ""
        author = ""
        art = null
        shownChapter = -1
    }

    val chapterList: List<Chapter> get() = chapters

    /** The loaded book's name, and its picture's bytes: what the widget keeps (L9). */
    val bookTitle: String get() = title
    val artBytes: ByteArray? get() = art

    val chapterIdx: Int get() = Position.chapterAt(chapters, exo.currentPosition)?.idx ?: -1

    private fun checkChapter() {
        if (chapterIdx != shownChapter) notifyMetadata()
    }

    private fun notifyMetadata() {
        shownChapter = chapterIdx
        val md = mediaMetadata
        for (l in mine.toList()) l.onMediaMetadataChanged(md)
    }

    override fun addListener(listener: Player.Listener) {
        mine.add(listener)
        super.addListener(listener)
    }

    override fun removeListener(listener: Player.Listener) {
        mine.remove(listener)
        super.removeListener(listener)
    }

    override fun getMediaMetadata(): MediaMetadata {
        val chapter = Position.chapterAt(chapters, exo.currentPosition)
        val b = MediaMetadata.Builder()
            .setTitle(chapter?.title ?: title)
            .setAlbumTitle(title)
            .setArtist(author)
            .setIsPlayable(true)
        art?.let { b.setArtworkData(it, MediaMetadata.PICTURE_TYPE_FRONT_COVER) }
        return b.build()
    }

    private val chapterCommands = intArrayOf(
        Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_PREVIOUS,
        Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
        Player.COMMAND_SEEK_BACK, Player.COMMAND_SEEK_FORWARD, Player.COMMAND_GET_METADATA,
    )

    /** A one-window timeline drops next and previous; a book with chapters has both. */
    override fun getAvailableCommands(): Player.Commands {
        val base = super.getAvailableCommands()
        if (exo.mediaItemCount == 0) return base
        return base.buildUpon().addAll(*chapterCommands).build()
    }

    override fun isCommandAvailable(command: Int): Boolean = availableCommands.contains(command)

    override fun seekToNext() = nextChapter()
    override fun seekToNextMediaItem() = nextChapter()
    override fun seekToPrevious() = previousChapter()
    override fun seekToPreviousMediaItem() = previousChapter()

    private fun nextChapter() {
        val to = nextChapterStart(chapters, exo.currentPosition) ?: return // the last chapter: nothing
        exo.seekTo(to)
        checkChapter()
    }

    private fun previousChapter() {
        exo.seekTo(previousChapterStart(chapters, exo.currentPosition))
        checkChapter()
    }

    override fun getSeekBackIncrement(): Long = settings.skipBackS * 1000L
    override fun getSeekForwardIncrement(): Long = settings.skipForwardS * 1000L

    override fun seekBack() {
        exo.seekTo((exo.currentPosition - seekBackIncrement).coerceAtLeast(0))
        checkChapter()
    }

    override fun seekForward() {
        val end = exo.duration
        val to = exo.currentPosition + seekForwardIncrement
        exo.seekTo(if (end != C.TIME_UNSET) to.coerceAtMost(end) else to)
        checkChapter()
    }

    // A client picks a book, not a file (A8, D3): `book:<id>` goes to the engine, which builds the one window and
    // starts at the server's place. Anything else is ignored: the timeline is never edited from outside.
    override fun setMediaItem(mediaItem: MediaItem) = choose(listOf(mediaItem))
    override fun setMediaItem(mediaItem: MediaItem, startPositionMs: Long) = choose(listOf(mediaItem))
    override fun setMediaItem(mediaItem: MediaItem, resetPosition: Boolean) = choose(listOf(mediaItem))
    override fun setMediaItems(mediaItems: MutableList<MediaItem>) = choose(mediaItems)
    override fun setMediaItems(mediaItems: MutableList<MediaItem>, resetPosition: Boolean) = choose(mediaItems)
    override fun setMediaItems(mediaItems: MutableList<MediaItem>, startIndex: Int, startPositionMs: Long) = choose(mediaItems)

    private fun choose(items: List<MediaItem>) {
        val book = (items.firstOrNull()?.mediaId?.let(::parseNode) as? Node.Book)?.id ?: return
        onBookChosen?.invoke(book)
    }

    override fun play() {
        resumeIfFailed()
        super.play()
    }

    override fun setPlayWhenReady(playWhenReady: Boolean) {
        if (playWhenReady) resumeIfFailed()
        super.setPlayWhenReady(playWhenReady)
    }

    private fun resumeIfFailed() {
        if (failed && exo.mediaItemCount > 0) {
            failed = false
            exo.prepare() // same book, same place
        }
    }

    companion object {
        private const val TICK_MS = 500L
    }
}
