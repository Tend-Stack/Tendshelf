package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Strings in these fixtures are what the app wrote with org.json before the move; they must still load. */
class StoredJsonTest {
    // org.json: whole doubles without ".0", "/" escaped, keys in insertion order.
    private val oldSettings = """{"speed":1.25,"skipBackS":15,"skipForwardS":30,"autoRewind":true,"nextInSeries":false,"skipQuiet":true}"""
    private val oldSettingsWhole = """{"speed":1,"skipBackS":30,"skipForwardS":30,"autoRewind":true,"nextInSeries":false,"skipQuiet":false}"""
    private val oldSettingsBeforeSkipQuiet = """{"speed":2,"skipBackS":10,"skipForwardS":20,"autoRewind":false,"nextInSeries":true}"""

    @Test
    fun keptSettingsLoadAndKeepTheirWrittenShape() {
        assertEquals(PlayerSettings(1.25, 15, 30, true, false, DEFAULT_REWIND, true), decodeKeptSettings(oldSettings))
        assertEquals(PlayerSettings(1.0, 30, 30, true, false), decodeKeptSettings(oldSettingsWhole))
        assertEquals(PlayerSettings(2.0, 10, 20, false, true), decodeKeptSettings(oldSettingsBeforeSkipQuiet))
        // writing gives byte-for-byte what org.json wrote
        assertEquals(oldSettings, encodeKeptSettings(decodeKeptSettings(oldSettings)))
        assertEquals(oldSettingsWhole, encodeKeptSettings(decodeKeptSettings(oldSettingsWhole)))
        // the rewind table is not kept; a table in the argument is ignored
        assertEquals(oldSettings, encodeKeptSettings(PlayerSettings(1.25, 15, 30, true, false, listOf(RewindStep(0, 1)), true)))
    }

    @Test
    fun keptSettingsFallBackToTheBuiltInOnes() {
        val builtIn = PlayerSettings(1.0, 30, 30, true, false)
        for (bad in listOf(null, "", "{", "[]", "{}", """{"speed":1}""", """{"speed":"x","skipBackS":1,"skipForwardS":1,"autoRewind":true,"nextInSeries":true}""")) {
            assertEquals(builtIn, decodeKeptSettings(bad), bad ?: "null")
        }
    }

    @Test
    fun textManifest() {
        val m = TextManifest.parse(
            """{"bookId":"b","editionId":"e","revision":"r1","title":"T","author":"A","bytes":123,"parts":["/p1","/p2"],"images":["/i"],"assets":[],"x":1}""",
        )
        assertEquals(listOf("b", "e", "r1", "T", "A", 123L), listOf(m.bookId, m.editionId, m.revision, m.title, m.author, m.bytes))
        assertEquals(listOf("/p1", "/p2"), m.parts)
        assertEquals(listOf("/i"), m.images)
        assertEquals(emptyList(), m.assets)
        val bare = TextManifest.parse("""{"bookId":"b","revision":"r"}""")
        assertEquals(listOf("", "", "", 0L), listOf(bare.editionId, bare.title, bare.author, bare.bytes))
        assertEquals(emptyList(), bare.parts)
        for (bad in listOf("", "[]", """{"revision":"r"}""", """{"bookId":"b"}""")) {
            assertFailsWith<JsonShapeException>(bad) { TextManifest.parse(bad) }
        }
    }

    // As org.json wrote index.json: "\/" escapes, entries in insertion order.
    private val oldIndex = """{"\/books\/b1\/read":{"file":"a1.html","type":"text\/html; charset=utf-8","csp":"default-src 'self'"},""" +
        """"\/books\/b1\/images\/x":{"file":"a2.bin","type":"image\/png","csp":""},""" +
        """"\/static\/app.css":{"file":"a3.bin","type":"text\/css","csp":""}}"""

    @Test
    fun textIndexLoadsWhatTheOldAppWrote() {
        val i = TextIndex.parse(oldIndex)
        assertEquals(listOf("/books/b1/read", "/books/b1/images/x", "/static/app.css"), i.keys)
        val e = i["/books/b1/read"]!!
        assertEquals(listOf("a1.html", "text/html; charset=utf-8", "default-src 'self'"), listOf(e.file, e.type, e.csp))
        assertEquals("", i["/static/app.css"]!!.csp)
        assertNull(i["/nope"])
        assertTrue("/static/app.css" in i)
        // and round-trips through the new writer
        val again = TextIndex.parse(i.toJson())
        assertEquals(i.keys, again.keys)
        assertEquals(e.type, again["/books/b1/read"]!!.type)
    }

    @Test
    fun textIndexIsForgivingAndEditable() {
        assertEquals(emptyList(), TextIndex.parse("").keys)
        assertEquals(emptyList(), TextIndex.parse("[1]").keys)
        assertEquals(emptyList(), TextIndex.parse("{").keys)
        val odd = TextIndex.parse("""{"/a":5,"/b":{},"/c":{"file":"f","csp":null}}""")
        assertEquals(listOf("/b", "/c"), odd.keys)
        assertEquals(listOf("", "application/octet-stream", ""), listOf(odd["/b"]!!.file, odd["/b"]!!.type, odd["/b"]!!.csp))
        assertEquals("null", odd["/c"]!!.csp) // org.json read a null as the text "null"; the page never sets a csp of null
        val idx = TextIndex()
        idx["/books/b/read"] = TextIndexEntry("f1", "text/html", "")
        idx["/books/b/images/i"] = TextIndexEntry("f2", "image/png", "")
        idx["/books/b/p1"] = TextIndexEntry("f3", "text/html", "x")
        assertEquals("""{"/books/b/read":{"file":"f1","type":"text/html","csp":""},"/books/b/images/i":{"file":"f2","type":"image/png","csp":""},"/books/b/p1":{"file":"f3","type":"text/html","csp":"x"}}""", idx.toJson())
        val fresh = TextIndex()
        fresh["/books/b/p2"] = TextIndexEntry("f4", "text/html", "")
        fresh["/books/b/read"] = TextIndexEntry("f5", "text/html", "")
        val merged = idx.mergedWith(fresh)
        // old shell and parts stay (images and anything not under /books/ go), fresh ones replace, order: old then new
        assertEquals(listOf("/books/b/read", "/books/b/p1", "/books/b/p2"), merged.keys)
        assertEquals("f5", merged["/books/b/read"]!!.file)
        idx.remove("/books/b/p1")
        assertEquals(2, idx.keys.size)
        assertEquals("""{}""", TextIndex().toJson())
        // quotes and slashes survive a round trip
        val q = TextIndex()
        q["/books/\"x\"/\\"] = TextIndexEntry("f\"", "t\\", "c\n")
        assertEquals("c\n", TextIndex.parse(q.toJson())["/books/\"x\"/\\"]!!.csp)
    }
}
