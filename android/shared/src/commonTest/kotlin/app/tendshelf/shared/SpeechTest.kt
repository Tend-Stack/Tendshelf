package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Read aloud (decision 0032): the three speech verbs, the `say` text rules, the two events and the voice filter. */
class SpeechTest {
    private fun say(text: String, id: String = "en-us-x-sfg-local") = BridgeMessage.Say(1, 1.0, id, text)

    @Test
    fun voicesAndStop() {
        assertEquals(BridgeMessage.TtsVoices, parseBridge("tts voices"))
        assertEquals(BridgeMessage.TtsStop, parseBridge("tts stop"))
        for (bad in listOf("tts", "tts foo", "tts voices x", "tts  voices", "TTS voices", "tts voices ", "tts stop 1")) {
            assertNull(parseBridge(bad), bad)
        }
    }

    @Test
    fun sayAccepts() {
        assertEquals(say("Hello, world."), parseBridge("say 1 1.00 en-us-x-sfg-local Hello, world."))
        val unicode = "Ça va? “Oui.” 日本語"
        assertEquals(say(unicode), parseBridge("say 1 1.00 en-us-x-sfg-local $unicode"))
        val emoji = "Hi 😀 there"
        assertEquals(say(emoji), parseBridge("say 1 1.00 en-us-x-sfg-local $emoji"))
        assertEquals(BridgeMessage.Say(999999999, 2.0, "v1", "x  y"), parseBridge("say 999999999 2.00 v1 x  y"))
        val longest = "a".repeat(4000)
        assertEquals(say(longest, "v1"), parseBridge("say 1 1.00 v1 $longest"))
    }

    @Test
    fun sayRefuses() {
        val bad = listOf(
            "say 1 1.00 v1 ${"a".repeat(4001)}",
            "say 1 1.00 v1 line\nbreak",
            "say 1 1.00 v1 tab\there",
            "say 1 1.00 v1 next\u0085line",
            "say 1 1.00 v1 del\u007f",
            "say 1 1.00 v1 lone\uD800",
            "say 1 1.00 v1 lone\uDC00 low",
            "say 1 1.00 v1",
            "say 1 1.00 v1 ",
            "say  1 1.00 v1 hi",
            "say 1  1.00 v1 hi",
            "say 1 1.00  v1 hi",
            "say 0 1.00 v1 hi",
            "say 01 1.00 v1 hi",
            "say 1234567890 1.00 v1 hi",
            "say x 1.00 v1 hi",
            "say 1 1.03 v1 hi",
            "say 1 0.45 v1 hi",
            "say 1 3.05 v1 hi",
            "say 1 1.00 - hi",
            "say 1 1.00 a/b hi",
            "SAY 1 1.00 v1 hi",
            "say",
            "say ",
        )
        for (raw in bad) assertNull(parseBridge(raw), raw.take(40))
        // Only `say` may carry anything but ASCII.
        assertNull(parseBridge("play é"))
        assertNull(parseBridge("tts voicés"))
    }

    @Test
    fun events() {
        assertEquals("{\"t\":\"tts\",\"n\":7,\"ev\":\"start\"}", encodeTts(7, "start"))
        assertEquals("{\"t\":\"tts\",\"n\":7,\"ev\":\"end\"}", encodeTts(7, "end"))
        assertEquals("{\"t\":\"tts\",\"n\":7,\"ev\":\"stopped\"}", encodeTts(7, "stopped"))
        assertEquals("{\"t\":\"tts\",\"n\":7,\"ev\":\"error\"}", encodeTts(7, "boom\"}"))
        assertEquals("{\"t\":\"tts\",\"n\":0,\"ev\":\"end\"}", encodeTts(-5, "end"))
        assertEquals("{\"t\":\"tts\",\"n\":999999999,\"ev\":\"end\"}", encodeTts(Int.MAX_VALUE, "end"))
    }

    @Test
    fun voiceList() {
        val a = VoiceInfo("en-us-x-sfg-local", "en-US", network = false, notInstalled = false, isDefault = true)
        val b = VoiceInfo("de-de-x-nfh-local", "de-DE", network = false, notInstalled = false, isDefault = false)
        assertEquals("{\"t\":\"voices\",\"list\":[]}", encodeVoices(emptyList()))
        assertEquals(
            "{\"t\":\"voices\",\"list\":[{\"id\":\"en-us-x-sfg-local\",\"lang\":\"en-US\",\"def\":true},{\"id\":\"de-de-x-nfh-local\",\"lang\":\"de-DE\",\"def\":false}]}",
            encodeVoices(listOf(a, b)),
        )
        // Bad ids and languages are dropped, never escaped through.
        val badId = a.copy(id = "a b\"")
        val badLang = a.copy(lang = "e")
        val dotted = a.copy(lang = "en.US")
        assertEquals("{\"t\":\"voices\",\"list\":[]}", encodeVoices(listOf(badId, badLang, dotted)))
        assertTrue(encodeVoices(List(300) { a.copy(id = "v$it") }).split("\"id\"").size - 1 == MAX_VOICES)
    }

    @Test
    fun usable() {
        fun v(id: String, lang: String = "en-US", net: Boolean = false, missing: Boolean = false) =
            VoiceInfo(id, lang, net, missing, false)
        val out = usableVoices(
            listOf(v("b"), v("net", net = true), v("gone", missing = true), v("a"), v("de", lang = "de-DE"), v("bad id"), v("x", lang = "e")),
            "b",
        )
        assertEquals(listOf("de", "a", "b"), out.map { it.id })
        assertEquals(listOf(false, false, true), out.map { it.isDefault })
        assertEquals(MAX_VOICES, usableVoices(List(500) { v("v$it") }, null).size)
        // A default that was filtered out flags nothing.
        assertTrue(usableVoices(listOf(v("net", net = true), v("a")), "net").none { it.isDefault })
    }
}
