package app.tendshelf.shared

import java.net.URLEncoder
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class HandoffTest {
    private val tok = "A".repeat(43)
    private val mixed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO-_"

    private fun enc(s: String) = URLEncoder.encode(s, "UTF-8")

    @Test
    fun accepted() {
        val inv = Handoff(Origin.parse("https://a.example")!!, Kind.Invite, tok)
        assertEquals(inv, parseHandoff("https://a.example/invite/$tok"))
        assertEquals(inv, parseHandoff("  Here you go: https://A.Example/invite/$tok. Welcome!"))
        assertEquals(inv, parseHandoff("(see https://a.example/invite/$tok)"))
        assertEquals(inv, parseHandoff("https://a.example/invite/$tok?x=1#frag"))
        assertEquals(inv, parseHandoff("https://a.example:443/invite/$tok,"))
        assertEquals(Handoff(Origin.parse("http://10.0.0.5:8095")!!, Kind.Pair, mixed), parseHandoff("http://10.0.0.5:8095/pair/$mixed"))
        assertEquals(inv, parseHandoff("tendshelf://open?link=" + enc("https://a.example/invite/$tok")))
        assertEquals(inv, parseHandoff("tendshelf://open?link=" + enc("https://a.example/invite/$tok?noapp=1")))
        assertEquals(Handoff(Origin.parse("http://[::1]:8095")!!, Kind.Pair, tok), parseHandoff("http://[::1]:8095/pair/$tok"))
    }

    @Test
    fun refused() {
        val refused = mapOf(
            "empty" to "",
            "no link" to "invitation please",
            "double wrapped" to "tendshelf://open?link=" + enc("tendshelf://open?link=" + enc("https://a.example/invite/$tok")),
            "wrapped non-http" to "tendshelf://open?link=" + enc("ftp://a.example/invite/$tok"),
            "wrong host for scheme" to "tendshelf://evil/?link=" + enc("https://a.example/invite/$tok"),
            "no link param" to "tendshelf://open?x=1",
            "userinfo" to "https://user@a.example/invite/$tok",
            "userinfo trick" to "https://a.example@evil.example/invite/$tok",
            "backslash" to "https://a.example\\@evil.example/invite/$tok",
            "non-ASCII host" to "https://bücher.example/invite/$tok",
            "ftp" to "ftp://a.example/invite/$tok",
            "wrong path" to "https://a.example/share/$tok",
            "extra segment" to "https://a.example/invite/$tok/x",
            "bare invite" to "https://a.example/invite/",
            "42 chars" to "https://a.example/invite/" + "A".repeat(42),
            "44 chars" to "https://a.example/invite/" + "A".repeat(44),
            "bad char" to "https://a.example/invite/" + "A".repeat(42) + "+",
            "too long" to "https://a.example/invite/$tok " + "x".repeat(2049),
            "path in link" to "https://a.example/x/invite/$tok",
        )
        for ((name, text) in refused) assertNull(parseHandoff(text), name)
    }
}
