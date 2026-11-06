package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals

class BridgeOriginTest {
    private val good = Origin.parse("http://good.example:8095")!!

    @Test
    fun onlyTheExactOriginIsAccepted() {
        val yes = listOf("http://good.example:8095", "HTTP://GOOD.EXAMPLE:8095", "http://good.example:8095/")
        val no = listOf(
            "http://good.example", "http://good.example:8096", "http://good.example:80", "https://good.example:8095",
            "http://good.example.:8095", "http://evil.example:8095", "http://good.example.evil:8095", "http://evil@good.example:8095",
            "http://good.example@evil.example:8095", "http://good.example:8095@evil", "http://good.example:8095\\@evil",
            "null", "", " ", "file://", "about:blank", "data:text/html,x", "http://good.example:8095/x", "http://good.example:8095 evil",
            "http://[::1]:8095", "ws://good.example:8095", "http://good.example:08095x", "http://good.example:80950",
        )
        for (s in yes) assertEquals(true, good.sameSource(s), s)
        for (s in no) assertEquals(false, good.sameSource(s), s)
    }

    @Test
    fun defaultPortsAreNormalised() {
        val https = Origin.parse("https://good.example")!!
        assertEquals(true, https.sameSource("https://good.example:443"))
        assertEquals(false, https.sameSource("http://good.example"))
        assertEquals(false, https.sameSource("https://good.example:8443"))
    }
}
