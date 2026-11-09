package app.tendshelf.shared

import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * The only place server JSON is read, into plain data classes. It moved here from the Android app (it read with
 * org.json there) with the same acceptances, defaults and refusals; see [Jx.kt] for the rules and ApiJsonTest for the table.
 * Functions that return null on a bad shape say so; the others throw [JsonShapeException] as org.json threw.
 */
object ApiJson {
    /** `{token?,device:{id},user:{id}}` from POST /api/v1/auth/token. Null when it is not that shape. */
    fun swapAnswer(body: String): SwapAnswer? = try {
        val j = parseJsonObject(body)
        SwapAnswer(
            token = j.optString("token", "").ifEmpty { null },
            deviceId = j.getObject("device").getString("id"),
            userId = j.getObject("user").getString("id"),
        )
    } catch (_: Exception) {
        null
    }

    /** `{user:{id},device:{...}}` from GET /api/v1/me; the person's id is wanted, nothing else. */
    fun meId(body: String): String? = try {
        parseJsonObject(body).getObject("user").optString("id", "").ifEmpty { null }
    } catch (_: Exception) {
        null
    }

    fun healthOk(body: String): HealthShape? = try {
        val j = parseJsonObject(body)
        HealthShape(
            ok = j.isWord("status", "ok") && j.has("version") && (!j.has("app") || j.isWord("app", "tendshelf")),
            connect = j.has("connect"),
        )
    } catch (_: Exception) {
        null
    }

    /** True when the key holds the string [word] (a number or null never equals it, as before). */
    private fun JsonObject.isWord(name: String, word: String): Boolean =
        (this[name] as? JsonPrimitive)?.let { it.isString && it.content == word } ?: false

    /** The `app` word and the install `id` of a healthz answer; null when it is not JSON. */
    fun identity(body: String): Identity? = try {
        val j = parseJsonObject(body)
        Identity(j.optString("app").ifEmpty { null }, j.optString("id").ifEmpty { null })
    } catch (_: Exception) {
        null
    }

    fun remoteProgress(it: JsonObject): RemoteProgress = RemoteProgress(
        it.getLong("positionMs"), it.getBoolean("finished"), it.getLong("clientUpdatedAt"),
        it.optString("deviceId"), it.optString("deviceLabel"),
    )

    /** The server's rewind table from the settings answer; the built-in one when it has none. */
    fun rewindSteps(settings: JsonObject): List<RewindStep> {
        val arr = settings.optArray("rewindSteps") ?: return DEFAULT_REWIND
        val steps = (0 until arr.size).mapNotNull {
            val o = arr.optObject(it) ?: return@mapNotNull null
            RewindStep(o.optLong("fromMs", -1), o.optInt("seconds", -1)).takeIf { s -> s.fromMs >= 0 && s.seconds >= 0 }
        }
        return steps.ifEmpty { DEFAULT_REWIND }
    }

    /** `GET /api/v1/settings`. */
    fun settings(body: String): PlayerSettings {
        val j = parseJsonObject(body)
        return PlayerSettings(
            j.getDouble("speed"), j.getInt("skipBackS"), j.getInt("skipForwardS"),
            j.getBoolean("autoRewind"), j.getBoolean("nextInSeries"), rewindSteps(j),
            skipQuiet = j.optBoolean("skipQuiet", false), // older servers lack it
            autoDownload = j.optBoolean("autoDownload", false), // older servers lack it
        )
    }

    /** `GET /api/v1/books/{id}`; [paths] words the cover address. */
    fun manifest(text: String, paths: ApiPaths): BookManifest {
        val json = parseJsonObject(text)
        val book = json.getObject("book")
        val files = json.getArray("files").let { arr ->
            (0 until arr.size).map {
                val f = arr.getObject(it)
                AudioFile(
                    f.getString("id"), f.getInt("idx"), f.getLong("offsetMs"), f.getLong("durationMs"),
                    f.getString("mime"), f.getBoolean("needsCompat"), f.optBoolean("playable", true), f.optLong("size", 0),
                )
            }
        }
        val chapters = json.getArray("chapters").let { arr ->
            (0 until arr.size).map {
                val c = arr.getObject(it)
                Chapter(c.getInt("idx"), c.getLong("startMs"), c.getLong("endMs"), c.getString("title"))
            }
        }
        val authors = book.optArray("authors")?.let { arr ->
            (0 until arr.size).joinToString(", ") { arr.getObject(it).getString("name") }
        }.orEmpty()
        val progress = json.optObject("progress")?.let(::remoteProgress)
        // The id in the answer is the live one when the asked-for id was merged into another book.
        val id = book.getString("id")
        return BookManifest(
            id, book.getString("title"), book.getLong("durationMs"), files, chapters,
            speed = json.optDouble("speed", 1.0),
            coverUrl = if (book.isNull("cover")) "" else paths.coverUrl(id, 512),
            author = authors,
            progress = progress,
            mayDownload = json.optObject("permissions")?.optBoolean("download", false) ?: false,
        )
    }

    /** `{"progress":{...}|null}` from GET /api/v1/progress/{id}: the stored place, or null when there is none. */
    fun progress(body: String): RemoteProgress? = parseJsonObject(body).optObject("progress")?.let(::remoteProgress)

    /** `{"book":{"id"}}` from the queue-next answer. */
    fun nextBookId(body: String): String = parseJsonObject(body).getObject("book").getString("id")

    /** `{"results":[{bookId,applied,progress,error:{code}}]}` from POST /api/v1/progress/batch. */
    fun batchResults(body: String): List<BatchItemResult> {
        val arr = parseJsonObject(body).getArray("results")
        return (0 until arr.size).map {
            val o = arr.getObject(it)
            BatchItemResult(
                o.getString("bookId"), o.optBoolean("applied"),
                o.optObject("error")?.optString("code", "error"),
                o.optObject("progress")?.let(::remoteProgress),
            )
        }
    }

    /** One `apiBookCard` from the server: id, title, authors, duration and whether it has a cover. */
    fun bookCard(o: JsonObject): BookCard {
        val authors = o.optArray("authors")?.let { arr ->
            (0 until arr.size).mapNotNull { arr.optObject(it)?.optString("name")?.ifEmpty { null } }.joinToString(", ")
        }.orEmpty()
        return BookCard(o.getString("id"), o.getString("title"), authors, o.optLong("durationMs", 0), !o.isNull("cover"))
    }

    private fun cards(arr: JsonArray?, pick: (JsonObject) -> JsonObject?): List<BookCard> =
        if (arr == null) emptyList() else (0 until arr.size).mapNotNull { i -> arr.optObject(i)?.let(pick)?.let(::bookCard) }

    /** `{"items":[{"book":{...}}]}` from /continue and /queue. */
    fun itemBooks(body: String): List<BookCard> = cards(parseJsonObject(body).optArray("items")) { it.optObject("book") }

    /** `{"books":[...]}` from /search. */
    fun searchBooks(body: String): List<BookCard> = cards(parseJsonObject(body).optArray("books")) { it }

    /** `{"books":{"items":[...],"next":...},"shelf":{...}}` from /shelves/{id}. */
    fun shelfBooks(body: String): List<BookCard> = cards(parseJsonObject(body).optObject("books")?.optArray("items")) { it }

    /** `{"own":[...],"shared":[...]}` from /shelves: the person's own first. */
    fun shelves(body: String): List<ShelfCard> {
        val j = parseJsonObject(body)
        return listOf("own", "shared").flatMap { key ->
            val arr = j.optArray(key) ?: return@flatMap emptyList()
            (0 until arr.size).mapNotNull { arr.optObject(it) }.map { ShelfCard(it.getString("id"), it.getString("name"), it.optInt("books", 0)) }
        }
    }

    data class HealthShape(val ok: Boolean, val connect: Boolean)

    fun swapRequest(code: String, verifier: String, name: String): String =
        "{\"code\":${jsonString(code)},\"verifier\":${jsonString(verifier)},\"name\":${jsonString(name)},\"kind\":\"mobile\"}"
}
