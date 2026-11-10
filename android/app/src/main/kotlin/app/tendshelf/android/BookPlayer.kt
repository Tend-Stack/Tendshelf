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
import app.tendshelf.shared.PlayerSettings
import app.tendshelf.shared.Position
import app.tendshelf.shared.liveSubtitle
import app.tendshelf.shared.nextChapterStart
import app.tendshelf.shared.parseNode
import app.tendshelf.shared.playableId
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

    /** The episode's own title while an episode is loaded; null for a book. Its chapter shows beside it, never in its place. */
    private var episodeTitle: String? = null

    /**
     * A live station is loaded (R3): the title is its name, [liveNow] the song on air, and there is nothing to seek, skip or
     * save. Pause lets go of the stream and play joins it again at the live edge, so both go to [onLiveToggle] (the engine).
     */
    private var live = false
    private var liveNow = ""
    private var liveWord = ""
    val isLive: Boolean get() = live

    /** Main thread: a client asked a live station to play (true) or pause (false). */
    /** Main thread: a station replaced a book or episode, or the other way round. */
    var onLiveKindChanged: (() -> Unit)? = null

    var onLiveToggle: ((Boolean) -> Unit)? = null

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
        if (live) return // a station plays at its own speed
        traceSpeed("setPlaybackSpeed", speed)
        super.setPlaybackSpeed(speed)
    }

    override fun setPlaybackParameters(playbackParameters: androidx.media3.common.PlaybackParameters) {
        traceSpeed("setPlaybackParameters", playbackParameters.speed)
        super.setPlaybackParameters(playbackParameters)
    }

    /**
     * An episode was loaded (A1): the show is the album and the artist, the episode the title, so the lock screen never shows
     * a chapter name as the title or an old item's picture. [chapters] may be empty now and arrive later through [setChapters].
     */
    fun loadEpisode(episode: String, show: String, chapters: List<Chapter>, settings: PlayerSettings, art: ByteArray?) {
        load(show, "", chapters, settings, art, episode)
    }

    /** A station was loaded (R3). [liveWord] is the word for "live", shown where no song title is known yet. */
    fun loadStation(name: String, liveWord: String, art: ByteArray?) {
        load(name, "", emptyList(), settings, art)
        live = true
        liveNow = ""
        this.liveWord = liveWord
        notifyMetadata()
        onLiveKindChanged?.invoke()
    }

    /** The song or show on air, as the server last said; empty when it names none. */
    fun setLiveNow(text: String) {
        if (!live || text == liveNow) return
        liveNow = text
        notifyMetadata()
    }

    /** Chapters that arrived after the sound started (the server fetches them from the publisher on first use). */
    fun setChapters(list: List<Chapter>) {
        chapters = list
        notifyMetadata()
    }

    fun load(title: String, author: String, chapters: List<Chapter>, settings: PlayerSettings, art: ByteArray?, episode: String? = null) {
        this.episodeTitle = episode
        val wasLive = live
        live = false
        this.title = title
        this.author = author
        this.chapters = chapters
        this.settings = settings
        this.art = art
        shownChapter = -1
        notifyMetadata()
        if (wasLive) onLiveKindChanged?.invoke()
    }

    fun clear() {
        live = false
        liveNow = ""
        episodeTitle = null
        chapters = emptyList()
        title = ""
        author = ""
        art = null
        shownChapter = -1
    }

    val chapterList: List<Chapter> get() = chapters

    /** The loaded book's name, and its picture's bytes: what the widget keeps (L9). */
    val bookTitle: String get() = title

    /** The loaded episode's title (then [bookTitle] is the show), or null for a book. */
    val episodeName: String? get() = episodeTitle
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
        if (live) {
            // A radio station (R3): its name is the title and the song on air the line beneath, as a radio app shows it.
            val line = liveSubtitle(liveNow, liveWord)
            val b = MediaMetadata.Builder().setTitle(title).setSubtitle(line).setArtist(line).setAlbumTitle(title)
                .setIsPlayable(true).setMediaType(MediaMetadata.MEDIA_TYPE_RADIO_STATION)
            art?.let { b.setArtworkData(it, MediaMetadata.PICTURE_TYPE_FRONT_COVER) }
            return b.build()
        }
        val chapter = Position.chapterAt(chapters, exo.currentPosition)
        val episode = episodeTitle
        val b = MediaMetadata.Builder()
            .setTitle(episode ?: chapter?.title ?: title)
            .setAlbumTitle(title)
            .setArtist(if (episode != null) title else author)
            .setIsPlayable(true)
        if (episode != null && chapter != null) b.setSubtitle(chapter.title)
        art?.let { b.setArtworkData(it, MediaMetadata.PICTURE_TYPE_FRONT_COVER) }
        return b.build()
    }

    private val chapterCommands = intArrayOf(
        Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_PREVIOUS,
        Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
        Player.COMMAND_SEEK_BACK, Player.COMMAND_SEEK_FORWARD, Player.COMMAND_GET_METADATA,
    )

    /** What a live item cannot do: no seek bar, no skip, no chapters, no speed (R3). */
    private val liveBlocked = intArrayOf(
        Player.COMMAND_SEEK_TO_NEXT, Player.COMMAND_SEEK_TO_PREVIOUS,
        Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM, Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
        Player.COMMAND_SEEK_BACK, Player.COMMAND_SEEK_FORWARD, Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
        Player.COMMAND_SEEK_TO_MEDIA_ITEM, Player.COMMAND_SEEK_TO_DEFAULT_POSITION, Player.COMMAND_SET_SPEED_AND_PITCH,
    )

    /** A one-window timeline drops next and previous; a book with chapters has both. A live station has neither, nor a seek. */
    override fun getAvailableCommands(): Player.Commands {
        val base = super.getAvailableCommands()
        if (live) return base.buildUpon().removeAll(*liveBlocked).build()
        if (exo.mediaItemCount == 0) return base
        return base.buildUpon().addAll(*chapterCommands).build()
    }

    override fun isCommandAvailable(command: Int): Boolean = availableCommands.contains(command)

    // A live item has no length and no place (R3).
    override fun getDuration(): Long = if (live) C.TIME_UNSET else super.getDuration()
    override fun getCurrentPosition(): Long = if (live) 0 else super.getCurrentPosition()
    override fun isCurrentMediaItemLive(): Boolean = live || super.isCurrentMediaItemLive()
    override fun isCurrentMediaItemSeekable(): Boolean = !live && super.isCurrentMediaItemSeekable()
    override fun seekTo(positionMs: Long) { if (!live) super.seekTo(positionMs) }
    override fun seekTo(mediaItemIndex: Int, positionMs: Long) { if (!live) super.seekTo(mediaItemIndex, positionMs) }
    override fun seekToDefaultPosition() { if (!live) super.seekToDefaultPosition() }

    override fun seekToNext() = nextChapter()
    override fun seekToNextMediaItem() = nextChapter()
    override fun seekToPrevious() = previousChapter()
    override fun seekToPreviousMediaItem() = previousChapter()

    private fun nextChapter() {
        if (live) return
        val to = nextChapterStart(chapters, exo.currentPosition) ?: return // the last chapter: nothing
        exo.seekTo(to)
        checkChapter()
    }

    private fun previousChapter() {
        if (live) return
        exo.seekTo(previousChapterStart(chapters, exo.currentPosition))
        checkChapter()
    }

    override fun getSeekBackIncrement(): Long = settings.skipBackS * 1000L
    override fun getSeekForwardIncrement(): Long = settings.skipForwardS * 1000L

    override fun seekBack() {
        if (live) return
        exo.seekTo((exo.currentPosition - seekBackIncrement).coerceAtLeast(0))
        checkChapter()
    }

    override fun seekForward() {
        if (live) return
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
        val book = playableId(items.firstOrNull()?.mediaId?.let(::parseNode)) ?: return
        onBookChosen?.invoke(book)
    }

    override fun play() {
        if (live) {
            onLiveToggle?.invoke(true)
            return
        }
        resumeIfFailed()
        super.play()
    }

    override fun pause() {
        if (live) {
            onLiveToggle?.invoke(false)
            return
        }
        super.pause()
    }

    override fun setPlayWhenReady(playWhenReady: Boolean) {
        if (live) {
            onLiveToggle?.invoke(playWhenReady)
            return
        }
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
