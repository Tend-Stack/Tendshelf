package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

class JxTest {
    @Test
    fun getAndOptCoerceLikeOrgJson() {
        val j = parseJsonObject("""{"s":"12","n":12,"d":1.5,"b":"TRUE","z":null,"t":true,"f":"x"}""")
        assertEquals(12L, j.getLong("s"))
        assertEquals("12", j.getString("n"))
        assertEquals(1, j.getInt("d"))
        assertEquals(true, j.getBoolean("b"))
        assertEquals(true, j.getBoolean("t"))
        // null reads as the text "null" (org.json), but as no number / no boolean
        assertEquals("null", j.optString("z"))
        assertEquals("null", j.getString("z"))
        assertEquals(7L, j.optLong("z", 7))
        assertEquals(false, j.optBoolean("z"))
        // missing: opt gives the fallback, get refuses
        assertEquals("", j.optString("none"))
        assertEquals("fb", j.optString("none", "fb"))
        assertFailsWith<JsonShapeException> { j.getString("none") }
        assertFailsWith<JsonShapeException> { j.getLong("f") }
        assertFailsWith<JsonShapeException> { j.getBoolean("n") }
        assertFailsWith<JsonShapeException> { j.getObject("s") }
        assertTrue(j.isNull("z") && j.isNull("none") && !j.isNull("s"))
    }

    @Test
    fun parseRefusesWhatIsNotAnObject() {
        for (bad in listOf("", "[]", "7", "\"x\"", "{", "<html>")) assertFailsWith<JsonShapeException>(bad) { parseJsonObject(bad) }
        assertEquals("v", parseJsonObject("{k:v}").getString("k")) // unquoted words, lenient like org.json's tokener
    }
}

class ApiJsonTest {
    private val paths = ApiPaths("https://t.example")

    @Test
    fun swapAnswer() {
        assertEquals(SwapAnswer("tok", "d1", "u1"), ApiJson.swapAnswer("""{"token":"tok","device":{"id":"d1","x":1},"user":{"id":"u1"},"extra":[1]}"""))
        assertEquals(SwapAnswer(null, "d1", "u1"), ApiJson.swapAnswer("""{"device":{"id":"d1"},"user":{"id":"u1"}}"""))
        assertEquals(SwapAnswer(null, "d1", "u1"), ApiJson.swapAnswer("""{"token":"","device":{"id":"d1"},"user":{"id":"u1"}}"""))
        assertEquals("null", ApiJson.swapAnswer("""{"token":null,"device":{"id":"d1"},"user":{"id":"u1"}}""")?.token) // org.json quirk, kept
        for (bad in listOf("", "[]", """{"device":{"id":"d"}}""", """{"device":"d","user":{"id":"u"}}""", """{"device":{},"user":{"id":"u"}}""", "nope")) {
            assertNull(ApiJson.swapAnswer(bad), bad)
        }
    }

    @Test
    fun meIdAndIdentityAndHealth() {
        assertEquals("u1", ApiJson.meId("""{"user":{"id":"u1"},"device":{}}"""))
        assertNull(ApiJson.meId("""{"user":{"id":""}}"""))
        assertNull(ApiJson.meId("""{"user":{}}"""))
        assertNull(ApiJson.meId("""{"user":5}"""))
        assertNull(ApiJson.meId("x"))
        assertEquals(Identity("tendshelf", "abc"), ApiJson.identity("""{"app":"tendshelf","id":"abc"}"""))
        assertEquals(Identity(null, null), ApiJson.identity("{}"))
        assertNull(ApiJson.identity("<html>"))
        assertEquals(ApiJson.HealthShape(true, false), ApiJson.healthOk("""{"status":"ok","version":"1"}"""))
        assertEquals(ApiJson.HealthShape(true, true), ApiJson.healthOk("""{"status":"ok","version":null,"app":"tendshelf","connect":{}}"""))
        assertEquals(ApiJson.HealthShape(false, false), ApiJson.healthOk("""{"status":"ok","version":"1","app":"other"}"""))
        assertEquals(ApiJson.HealthShape(false, false), ApiJson.healthOk("""{"status":"ok"}"""))
        assertEquals(ApiJson.HealthShape(false, false), ApiJson.healthOk("""{"status":1,"version":"1"}"""))
        assertNull(ApiJson.healthOk("[]"))
    }

    @Test
    fun settingsAndRewind() {
        val s = ApiJson.settings("""{"speed":1.25,"skipBackS":10,"skipForwardS":"45","autoRewind":true,"nextInSeries":false,"junk":{}}""")
        assertEquals(PlayerSettings(1.25, 10, 45, true, false, DEFAULT_REWIND, false), s)
        assertTrue(ApiJson.settings("""{"speed":1,"skipBackS":1,"skipForwardS":1,"autoRewind":true,"nextInSeries":true,"skipQuiet":true}""").skipQuiet)
        assertFailsWith<JsonShapeException> { ApiJson.settings("""{"speed":1,"skipBackS":1,"skipForwardS":1,"autoRewind":true}""") }
        assertFailsWith<JsonShapeException> { ApiJson.settings("""{"speed":"fast","skipBackS":1,"skipForwardS":1,"autoRewind":true,"nextInSeries":true}""") }
        val steps = ApiJson.rewindSteps(parseJsonObject("""{"rewindSteps":[{"fromMs":0,"seconds":2},{"fromMs":-5,"seconds":1},{"fromMs":9,"seconds":-1},7,{"fromMs":60000}]}"""))
        assertEquals(listOf(RewindStep(0, 2)), steps)
        assertEquals(DEFAULT_REWIND, ApiJson.rewindSteps(parseJsonObject("""{"rewindSteps":[]}""")))
        assertEquals(DEFAULT_REWIND, ApiJson.rewindSteps(parseJsonObject("""{"rewindSteps":"x"}""")))
        assertEquals(DEFAULT_REWIND, ApiJson.rewindSteps(parseJsonObject("{}")))
    }

    private val manifest = """{"book":{"id":"b1","title":"T","durationMs":9000,"cover":"x","authors":[{"name":"A"},{"name":"B"}]},
        "files":[{"id":"f1","idx":0,"offsetMs":0,"durationMs":9000,"mime":"audio/mp4","needsCompat":false}],
        "chapters":[{"idx":0,"startMs":0,"endMs":9000,"title":"One"}],
        "progress":{"positionMs":5,"finished":false,"clientUpdatedAt":6,"deviceId":"d","deviceLabel":"Pixel"},
        "speed":1.5,"permissions":{"download":true},"more":1}"""

    @Test
    fun manifestReadsAndDefaults() {
        val m = ApiJson.manifest(manifest, paths)
        assertEquals("b1", m.id)
        assertEquals("A, B", m.author)
        assertEquals(paths.coverUrl("b1", 512), m.coverUrl)
        assertEquals(AudioFile("f1", 0, 0, 9000, "audio/mp4", false, true, 0), m.files.single())
        assertEquals(Chapter(0, 0, 9000, "One"), m.chapters.single())
        assertEquals(RemoteProgress(5, false, 6, "d", "Pixel"), m.progress)
        assertEquals(1.5, m.speed)
        assertTrue(m.mayDownload)
        val bare = ApiJson.manifest("""{"book":{"id":"b","title":"t","durationMs":1,"cover":null},"files":[],"chapters":[]}""", paths)
        assertEquals("", bare.coverUrl)
        assertEquals("", bare.author)
        assertNull(bare.progress)
        assertEquals(1.0, bare.speed)
        assertEquals(false, bare.mayDownload)
        // a cover key that is missing is no cover (org.json isNull is true for both)
        assertEquals("", ApiJson.manifest("""{"book":{"id":"b","title":"t","durationMs":1},"files":[],"chapters":[]}""", paths).coverUrl)
        for (bad in listOf("", "[]", """{"files":[],"chapters":[]}""", """{"book":{"id":"b","title":"t","durationMs":1},"chapters":[]}""",
            """{"book":{"id":"b","title":"t"},"files":[],"chapters":[]}""",
            """{"book":{"id":"b","title":"t","durationMs":1,"authors":[{}]},"files":[],"chapters":[]}""".replace("{}", "5"),
            """{"book":{"id":"b","title":"t","durationMs":1},"files":[{"id":"f"}],"chapters":[]}""")) {
            assertFailsWith<JsonShapeException>(bad) { ApiJson.manifest(bad, paths) }
        }
        // an author without a name reads as the text "null" (org.json getString on null), kept
        assertEquals("null", ApiJson.manifest("""{"book":{"id":"b","title":"t","durationMs":1,"authors":[{"name":null}]},"files":[],"chapters":[]}""", paths).author)
    }

    @Test
    fun progressNextAndBatch() {
        assertEquals(RemoteProgress(1, true, 2, "", ""), ApiJson.progress("""{"progress":{"positionMs":1,"finished":true,"clientUpdatedAt":2}}"""))
        assertNull(ApiJson.progress("""{"progress":null}"""))
        assertNull(ApiJson.progress("{}"))
        assertFailsWith<JsonShapeException> { ApiJson.progress("""{"progress":{"positionMs":1}}""") }
        assertEquals("n1", ApiJson.nextBookId("""{"book":{"id":"n1"}}"""))
        assertFailsWith<JsonShapeException> { ApiJson.nextBookId("{}") }
        val r = ApiJson.batchResults(
            """{"results":[{"bookId":"a","applied":true,"progress":{"positionMs":1,"finished":false,"clientUpdatedAt":2}},
            {"bookId":"b","error":{"code":"nope"}},{"bookId":"c","error":{}}]}""",
        )
        assertEquals(listOf("a", "b", "c"), r.map { it.bookId })
        assertEquals(listOf(true, false, false), r.map { it.applied })
        assertEquals(listOf(null, "nope", "error"), r.map { it.errorCode })
        assertEquals(RemoteProgress(1, false, 2, "", ""), r[0].progress)
        assertFailsWith<JsonShapeException> { ApiJson.batchResults("{}") }
        assertFailsWith<JsonShapeException> { ApiJson.batchResults("""{"results":[5]}""") }
    }

    @Test
    fun listsAndShelves() {
        val card = """{"id":"b","title":"T","authors":[{"name":"A"},{"name":""},5,{"name":"C"}],"durationMs":"7","cover":{}}"""
        val expect = BookCard("b", "T", "A, C", 7, true)
        assertEquals(listOf(expect), ApiJson.itemBooks("""{"items":[{"book":$card},{"nobook":1},3]}"""))
        assertEquals(listOf(expect), ApiJson.searchBooks("""{"books":[$card,"x"]}"""))
        assertEquals(listOf(expect), ApiJson.shelfBooks("""{"books":{"items":[$card],"next":null},"shelf":{}}"""))
        assertEquals(emptyList(), ApiJson.searchBooks("{}"))
        assertEquals(emptyList(), ApiJson.shelfBooks("""{"books":[]}"""))
        assertEquals(BookCard("b", "T", "", 0, false), ApiJson.searchBooks("""{"books":[{"id":"b","title":"T","cover":null}]}""").single())
        assertFailsWith<JsonShapeException> { ApiJson.searchBooks("""{"books":[{"title":"T"}]}""") }
        assertEquals(
            listOf(ShelfCard("s1", "Mine", 3), ShelfCard("s2", "Theirs", 0)),
            ApiJson.shelves("""{"own":[{"id":"s1","name":"Mine","books":3}],"shared":[{"id":"s2","name":"Theirs"},"x"]}"""),
        )
        assertEquals(emptyList(), ApiJson.shelves("{}"))
        assertFailsWith<JsonShapeException> { ApiJson.shelves("""{"own":[{"id":"s"}]}""") }
    }

    @Test
    fun swapRequestIsTheSameJson() {
        assertEquals(
            """{"code":"c","verifier":"v\"q","name":"Pixel 7","kind":"mobile"}""",
            ApiJson.swapRequest("c", "v\"q", "Pixel 7"),
        )
    }
}
