package app.tendshelf.shared

/**
 * The Downloads page's words, one per key of `strings.xml` (`downloads_*`, `offline_*`), given raw: a word with `%1$d`
 * or `%%` is filled by [fillWord]. Android's `getString(id)` and the iPhone's `Words` therefore give the same text.
 */
data class DownloadsWords(
    val title: String,
    val lead: String,
    val empty: String,
    val emptyHow: String,
    val active: String,
    val ready: String,
    val percent: String,
    val working: String,
    val waiting: String,
    val pausedRow: String,
    val failedRow: String,
    val staleRow: String,
    val play: String,
    val read: String,
    val pause: String,
    val remove: String,
    val library: String,
    val autoRow: String,
    val autoWaiting: String,
    val autoNoRoom: String,
    val podcasts: String,
    val podcastsLead: String,
    val podcastOnRow: String,
    val podcastOffRow: String,
    val turnOn: String,
    val turnOff: String,
    val offlineTitle: String,
    val offlineBody: String,
    val offlineRetry: String,
)

/** One downloaded book (or episode) with everything the page needs to know about it from the platform. */
data class DownloadRow(
    val book: OfflineBook,
    val view: DlView,
    val hasCover: Boolean,
    val readable: Boolean,
    val hasAudio: Boolean,
    val auto: Boolean,
)

/** A followed show that can be turned on for automatic episode downloads. */
data class AutoShowRow(val id: String, val title: String, val keepLatest: Int, val on: Boolean)

/**
 * Everything [downloadsPage] draws. [offline] adds the "can't reach your server" words and Try again for [offlineAddress];
 * [homeUrl] is the library link; [readBase] is the server's origin for a book's Read link (empty hides Read).
 */
data class DownloadsInput(
    val rows: List<DownloadRow>,
    val shows: List<AutoShowRow>,
    val playingId: String,
    val playing: Boolean,
    val unmetered: Boolean,
    val autoNoRoom: Boolean,
    val offline: Boolean,
    val offlineAddress: String,
    val homeUrl: String,
    val readBase: String,
)

/**
 * The one list of downloaded books as the page template's values (TITLE, LEAD, NOTE, LIST, LIBRARY, HOMEURL, REFRESH),
 * every value escaped and no script. While a book downloads the page reloads itself every 2 s.
 */
fun downloadsPage(input: DownloadsInput, w: DownloadsWords): Map<String, String> {
    val books = input.rows
    val active = books.filter {
        val s = it.book.state
        s == OfflineState.QUEUED || s == OfflineState.DOWNLOADING || s == OfflineState.PAUSED || s == OfflineState.FAILED
    }
    val ready = books.filter { it.book.state == OfflineState.READY || it.book.state == OfflineState.STALE }
    val list = StringBuilder()
    if (books.isEmpty()) {
        list.append("<p><strong>${htmlEscape(w.empty)}</strong><br>${htmlEscape(w.emptyHow)}</p>")
    }
    section(list, w.active, active, input, w)
    section(list, w.ready, ready, input, w)
    shows(list, input.shows, w)
    val noRoom = if (input.autoNoRoom) "<p class=\"start__note\" role=\"status\">${htmlEscape(w.autoNoRoom)}</p>" else ""
    val note = noRoom + if (!input.offline) "" else
        "<p class=\"start__note\" role=\"status\">${htmlEscape(w.offlineTitle)}. ${htmlEscape(w.offlineBody)}</p>" +
            "<form method=\"get\" action=\"/connect\"><input type=\"hidden\" name=\"address\" value=\"${htmlEscape(input.offlineAddress)}\">" +
            "<button type=\"submit\" class=\"ts-btn ts-btn--primary ts-btn--lg ts-btn--block\">${htmlEscape(w.offlineRetry)}</button></form>"
    return mapOf(
        "TITLE" to htmlEscape(w.title),
        "LEAD" to htmlEscape(w.lead),
        "NOTE" to note,
        "LIST" to list.toString(),
        "LIBRARY" to htmlEscape(w.library),
        "HOMEURL" to htmlEscape(input.homeUrl),
        "REFRESH" to if (active.any { it.book.state == OfflineState.DOWNLOADING || it.book.state == OfflineState.QUEUED }) "<meta http-equiv=\"refresh\" content=\"2\">" else "",
    )
}

/** "Podcasts on this phone": one row per followed show with its on/off link; the choice is kept on the phone only. */
private fun shows(out: StringBuilder, known: List<AutoShowRow>, w: DownloadsWords) {
    if (known.isEmpty()) return
    out.append("<h2>${htmlEscape(w.podcasts)}</h2>")
    out.append("<p>${htmlEscape(w.podcastsLead)}</p><ul class=\"dl__list\">")
    for (s in known) {
        if (!validId(s.id)) continue
        val meta = if (s.on) fillWord(w.podcastOnRow, s.keepLatest.toString()) else w.podcastOffRow
        val label = if (s.on) w.turnOff else w.turnOn
        out.append("<li class=\"ts-row dl__row\"><span class=\"ts-row__text\"><span class=\"ts-row__title\">${htmlEscape(s.title)}</span><span class=\"ts-row__meta\">${htmlEscape(meta)}</span></span>")
        out.append("<span class=\"dl__actions\"><a class=\"ts-btn ts-btn--${if (s.on) "quiet" else "secondary"}\" href=\"/autoshow?id=${htmlEscape(s.id)}&amp;on=${if (s.on) 0 else 1}\">${htmlEscape(label)}</a></span></li>")
    }
    out.append("</ul>")
}

private fun section(out: StringBuilder, heading: String, rows: List<DownloadRow>, input: DownloadsInput, w: DownloadsWords) {
    if (rows.isEmpty()) return
    out.append("<h2>${htmlEscape(heading)} · ${rows.size}</h2><ul class=\"dl__list\">")
    for (r in rows) {
        val b = r.book
        val id = htmlEscape(b.bookId)
        val here = b.bookId == input.playingId
        val canPlay = b.state == OfflineState.READY || b.state == OfflineState.STALE
        val meta = htmlEscape(listOf(b.author, stateWords(r, input, w)).filter { it.isNotEmpty() }.joinToString(" · "))
        // A bar only while the percent is known; unknown shows the plain words, so nothing animates here.
        val bar = r.view.takeIf { b.state == OfflineState.DOWNLOADING && it.known }?.let {
            "<progress class=\"dl__bar\" max=\"100\" value=\"${it.pct}\" aria-label=\"${htmlEscape(w.active)}\"></progress>"
        }.orEmpty()
        val cover = if (r.hasCover) "<img class=\"dl__cover\" alt=\"\" src=\"/covers/$id\">" else ""
        out.append("<li class=\"ts-row dl__row\">$cover")
        out.append("<span class=\"ts-row__text\"><span class=\"ts-row__title\">${htmlEscape(b.title)}</span><span class=\"ts-row__meta\">$meta</span>$bar</span>")
        out.append("<span class=\"dl__actions\">")
        // The two formats are two plain buttons: this page shows no place and no time, so neither one is "the resume button".
        if (r.readable && input.readBase.isNotEmpty()) {
            out.append("<a class=\"ts-btn ts-btn--secondary\" href=\"${htmlEscape(input.readBase)}/books/$id/read\">${htmlEscape(w.read)}</a>")
        }
        if (canPlay && r.hasAudio) {
            val href = if (here) "/toggle" else "/play?id=$id"
            val label = if (here && input.playing) w.pause else w.play
            out.append("<a class=\"ts-btn ts-btn--secondary\" href=\"$href\">${htmlEscape(label)}</a>")
        }
        out.append("<a class=\"ts-btn ts-btn--quiet\" href=\"/remove?id=$id\">${htmlEscape(w.remove)}</a>")
        out.append("</span></li>")
    }
    out.append("</ul>")
}

private fun stateWords(r: DownloadRow, input: DownloadsInput, w: DownloadsWords): String = when (r.book.state) {
    // A book downloaded on its own says so, and says when it waits for Wi-Fi (decision 0035).
    OfflineState.READY, OfflineState.QUEUED, OfflineState.PAUSED ->
        if (r.auto) autoWords(r, input, w) else plainWords(r, w)
    else -> plainWords(r, w)
}

private fun autoWords(r: DownloadRow, input: DownloadsInput, w: DownloadsWords): String = when {
    r.book.state == OfflineState.READY -> w.autoRow
    !input.unmetered -> w.autoWaiting
    else -> plainWords(r, w)
}

private fun plainWords(r: DownloadRow, w: DownloadsWords): String = when (r.book.state) {
    OfflineState.QUEUED -> w.waiting
    OfflineState.DOWNLOADING -> if (r.view.known) fillWord(w.percent, r.view.pct.toString()) else w.working
    OfflineState.PAUSED -> w.pausedRow
    OfflineState.FAILED -> w.failedRow
    OfflineState.STALE -> w.staleRow
    OfflineState.READY -> ""
}

/**
 * Fills a `strings.xml` word: `%N$s` and `%N$d` become argument N (an argument that is missing stays as written), then
 * `%%` becomes `%`. A word with no arguments is returned with only its `%%` turned into `%`.
 */
fun fillWord(template: String, vararg args: String): String {
    val out = StringBuilder()
    var i = 0
    while (i < template.length) {
        val c = template[i]
        if (c == '%' && i + 1 < template.length && template[i + 1] == '%') {
            out.append('%')
            i += 2
            continue
        }
        if (c == '%') {
            var j = i + 1
            while (j < template.length && template[j] in '0'..'9') j++
            if (j > i + 1 && j + 1 < template.length && template[j] == '$' && (template[j + 1] == 's' || template[j + 1] == 'd')) {
                val n = template.substring(i + 1, j).toInt()
                if (n in 1..args.size) {
                    out.append(args[n - 1])
                    i = j + 2
                    continue
                }
            }
        }
        out.append(c)
        i++
    }
    return out.toString()
}

/** HTML text and attribute escaping: `& < > " '`. */
fun htmlEscape(s: String): String = buildString {
    for (ch in s) when (ch) {
        '&' -> append("&amp;")
        '<' -> append("&lt;")
        '>' -> append("&gt;")
        '"' -> append("&quot;")
        '\'' -> append("&#39;")
        else -> append(ch)
    }
}
