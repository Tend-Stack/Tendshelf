package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class BridgeProtocolTest {
    @Test
    fun goodMessages() {
        val good = mapOf(
            "hello 1" to BridgeMessage.Hello,
            "play" to BridgeMessage.Play,
            "pause" to BridgeMessage.Pause,
            "start abc_DEF-123 0" to BridgeMessage.Start("abc_DEF-123", 0),
            "start abc -" to BridgeMessage.Start("abc", null),
            "start a 999999999999" to BridgeMessage.Start("a", 999999999999),
            "seek 0" to BridgeMessage.Seek(0),
            "seek 123456" to BridgeMessage.Seek(123456),
            "speed 0.50" to BridgeMessage.Speed(0.5),
            "speed 1.05" to BridgeMessage.Speed(1.05),
            "speed 3.00" to BridgeMessage.Speed(3.0),
            "back 0" to BridgeMessage.Back(false),
            "back 1" to BridgeMessage.Back(true),
            "theme #0a1b2c" to BridgeMessage.Theme("#0a1b2c"),
        )
        for ((raw, want) in good) assertEquals(want, parseBridge(raw), raw)
        assertEquals(BridgeMessage.Start("a".repeat(64), 5), parseBridge("start ${"a".repeat(64)} 5"))
    }

    @Test
    fun hostileMessagesAreDropped() {
        val bad = listOf(
            "", " ", "hello", "hello 2", "hello  1", " hello 1", "hello 1 ", "Hello 1", "play now", "pause 1",
            "start", "start a", "start a b c", "start ../x 0", "start a/b 0", "start a?b 0", "start a%2f 0", "start a#b 0",
            "start ${"a".repeat(65)} 0", "start  0", "start a 0 ", "start a 1000000000000", "start a -1", "start a +1", "start a 1e3",
            "start a 0x10", "start a 1.5", "seek", "seek -1", "seek 1000000000000", "seek 1 2", "seek ٣", "seek ١",
            "speed 0.45", "speed 3.05", "speed 3.50", "speed 1.07", "speed 1", "speed 1.5", "speed 01.50", "speed -1.00", "speed 1,50",
            "speed NaN", "speed 1.5e0", "back", "back 2", "back true", "theme", "theme #ABCDEF", "theme #abcde", "theme #abcdeg",
            "theme abcdef", "theme #abcdef0", "theme red", "frobnicate", "start\ta 0", "play\n", "play\u0000", "play\r\nhello 1",
            "héllo 1", "play\u007f", "‮play", "seek 5\u0000", "😀",
            "x".repeat(129), "seek " + "1".repeat(124),
        )
        for (raw in bad) assertNull(parseBridge(raw), "should drop: ${raw.take(40).map { if (it.code < 32 || it.code > 126) "\\u%04x".format(it.code) else it.toString() }.joinToString("")}")
        assertNull(parseBridge(null))
    }

    @Test
    fun exactlyAtTheLengthLimit() {
        // 128 chars of a valid shape parse; 129 do not. "start " (6) + 64 id + " " + 12 digits = 83, so pad with the grammar's own max.
        val max = "start ${"a".repeat(64)} 123456789012"
        assertTrue(max.length <= MAX_LENGTH)
        assertEquals(BridgeMessage.Start("a".repeat(64), 123456789012), parseBridge(max))
        assertNull(parseBridge("x".repeat(MAX_LENGTH + 1)))
        assertNull(parseBridge("play" + " ".repeat(MAX_LENGTH)))
    }

    @Test
    fun noMessageCanCarryAnythingButItsFields() {
        // Every accepted string is printable ASCII, and no accepted id or hex can hold a separator, quote or slash.
        for (raw in listOf("start a-b_c 1", "theme #00ff00", "speed 2.50", "seek 7", "back 1", "hello 1", "play", "pause")) {
            val m = parseBridge(raw)!!
            assertFalse(m.toString().any { it == '/' || it == '\\' || it == '?' || it == '\n' || it == '"' })
        }
    }

    @Test
    fun stateIsFixedFieldsOnly() {
        val s = PlayerSnapshot("b\"1\\", 1500, 900000, true, false, false, 1.5, 3, "stream")
        assertEquals(
            "{\"t\":\"state\",\"bookId\":\"b\\\"1\\\\\",\"positionMs\":1500,\"durationMs\":900000,\"playing\":true," +
                "\"buffering\":false,\"ended\":false,\"speed\":1.50,\"chapterIdx\":3}",
            encodeState(s),
        )
        // The error text of a snapshot never crosses.
        assertFalse(encodeState(s).contains("stream"))
        assertEquals(
            "{\"t\":\"state\",\"bookId\":\"\",\"positionMs\":0,\"durationMs\":0,\"playing\":false,\"buffering\":false," +
                "\"ended\":false,\"speed\":1.00,\"chapterIdx\":0}",
            encodeState(PlayerSnapshot("", -5, -1, false, false, false, Double.NaN, -1, "")),
        )
        assertTrue(encodeState(PlayerSnapshot("", 0, 0, false, false, false, 1e-9, 0, "")).contains("\"speed\":0.50"))
        assertTrue(encodeState(PlayerSnapshot("", 0, 0, false, false, false, Double.POSITIVE_INFINITY, 0, "")).contains("\"speed\":3.00"))
    }

    @Test
    fun errorAndBackAreFixed() {
        assertEquals("{\"t\":\"error\",\"code\":\"stream\"}", encodeError("stream"))
        assertEquals("{\"t\":\"error\",\"code\":\"book\"}", encodeError("book"))
        // Anything else, including text that could hold a secret, becomes the generic code.
        assertEquals("{\"t\":\"error\",\"code\":\"stream\"}", encodeError("Bearer abcdef12 at http://x"))
        assertEquals("{\"t\":\"back\"}", encodeBack())
    }

    @Test
    fun answerWordsAreStrict() {
        assertEquals(BridgeMessage.Answer(true), parseBridge("answer here"))
        assertEquals(BridgeMessage.Answer(false), parseBridge("answer there"))
        for (bad in listOf("answer", "answer ", "answer  here", "answer here now", "answer HERE", "answer yes", "answer 1", "Answer here", "answer\there", "answerhere")) {
            assertNull(parseBridge(bad), bad)
        }
    }

    @Test
    fun askAndRewoundAreFixedFields() {
        assertEquals(
            "{\"t\":\"ask\",\"bookId\":\"b1\",\"positionMs\":80000,\"deviceLabel\":\"Pixel \\\"7\\\" <b>\"}",
            encodeAsk(Ask("b1", 80000, "Pixel \"7\" <b>")),
        )
        // No question: an empty book id, which closes the toast.
        assertEquals("{\"t\":\"ask\",\"bookId\":\"\",\"positionMs\":0,\"deviceLabel\":\"\"}", encodeAsk(null))
        assertEquals("{\"t\":\"ask\",\"bookId\":\"b\",\"positionMs\":0,\"deviceLabel\":\"x\"}", encodeAsk(Ask("b", -9, "x")))
        assertEquals("{\"t\":\"rewound\",\"s\":5}", encodeRewound(5))
        assertEquals("{\"t\":\"rewound\",\"s\":0}", encodeRewound(-3))
        // `background` crosses as the generic stream code.
        assertEquals("{\"t\":\"error\",\"code\":\"stream\"}", encodeError("background"))
    }

    @Test
    fun bucketAllowsThirtyAndRefills() {
        val b = TokenBucket(30, 0)
        var ok = 0
        for (i in 0 until 1000) if (b.take(0)) ok++
        assertEquals(30, ok)
        assertFalse(b.take(10))
        assertTrue(b.take(40)) // 40 ms refills one
        var after = 0
        for (i in 0 until 100) if (b.take(2000)) after++
        assertEquals(30, after) // a long pause refills only to the burst size
    }
}
