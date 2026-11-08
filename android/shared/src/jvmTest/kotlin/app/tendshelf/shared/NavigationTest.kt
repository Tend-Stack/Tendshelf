package app.tendshelf.shared

import java.security.MessageDigest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class NavigationTest {
    private val good = Origin.parse("https://good.example")!!

    @Test
    fun pkceMatchesRfc7636AppendixB() {
        val octets = intArrayOf(116, 24, 223, 180, 151, 153, 224, 37, 79, 250, 96, 125, 216, 173, 187, 186, 22, 212, 37, 77, 105, 214, 191, 240, 91, 88, 5, 88, 83, 132, 141, 121)
        val verifier = Pkce.verifier(ByteArray(32) { octets[it].toByte() })
        assertEquals("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", verifier)
        val digest = MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII))
        assertEquals("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", Pkce.challenge(digest))
        assertEquals(43, verifier.length)
    }

    @Test
    fun originParse() {
        assertEquals(Origin("https", "good.example", null), Origin.parse("HTTPS://Good.Example:443/"))
        assertEquals(Origin("http", "10.0.2.2", 8097), Origin.parse("http://10.0.2.2:8097"))
        assertEquals("http://[::1]:8095", Origin.parse("http://[::1]:8095")!!.text)
        for (bad in listOf("good.example", "ftp://x", "https://", "https://a/b", "https://a?x=1", "https://u@a", "https://a:0", "https://a:99999", "https://a:", "https://a b", "https://a\\b", "https://a#f", "https://-a")) {
            assertNull(Origin.parse(bad), bad)
        }
    }

    @Test
    fun originCandidatesAndPrivacy() {
        assertEquals(listOf("https://tendshelf.home:8095", "http://tendshelf.home:8095"), Origin.candidates("tendshelf.home:8095").map { it.text })
        assertEquals(listOf("http://x"), Origin.candidates("http://x").map { it.text })
        val priv = listOf("10.1.2.3", "172.16.0.1", "172.31.9.9", "192.168.1.5", "127.0.0.1", "169.254.1.1", "100.64.0.1", "nas.local", "nas", "[::1]", "[fd12::1]", "[fe80::1]")
        for (h in priv) assertTrue(Origin.parse("http://$h")!!.privateAddress, h)
        for (h in listOf("8.8.8.8", "172.32.0.1", "100.128.0.1", "example.org", "[2001:db8::1]")) assertTrue(!Origin.parse("http://$h")!!.privateAddress, h)
    }

    @Test
    fun originUrls() {
        val o = Origin.parse("http://10.0.2.2:8097")!!
        assertEquals("http://10.0.2.2:8097/healthz", o.healthUrl())
        assertEquals("http://10.0.2.2:8097/api/v1/me", o.meUrl())
        assertEquals("http://10.0.2.2:8097/?shell=android&next=/", o.homeUrl())
        assertEquals("http://10.0.2.2:8097/?shell=android&next=%2Fapp%2Fconnect%3Fchallenge%3Dabc", o.connectUrl("abc"))
    }

    @Test
    fun decideTable() {
        val done = "https://good.example/app/connect/done?code=XYZ"
        val cases = listOf(
            Triple("same origin page", "https://good.example/books", Nav.Stay),
            Triple("done, pending", done, Nav.TakeCode("XYZ")),
            Triple("userinfo trick", "https://good@evil/", Nav.Block),
            Triple("scheme and port differ", "HTTP://GOOD.example:443", Nav.External),
            Triple("same host upper case", "HTTPS://GOOD.example/x", Nav.Stay),
            Triple("explicit default port", "https://good.example:443/x", Nav.Stay),
            Triple("trailing dot", "https://good.example./x", Nav.External),
            Triple("other port", "https://good.example:8443/x", Nav.External),
            Triple("backslashes", "https://good.example\\@evil/", Nav.Block),
            Triple("intent", "intent://x#Intent;end", Nav.Block),
            Triple("file", "file:///etc/hosts", Nav.Block),
            Triple("content", "content://x/y", Nav.Block),
            Triple("javascript", "javascript:alert(1)", Nav.Block),
            Triple("data", "data:text/html,hi", Nav.Block),
            Triple("junk", "not a url", Nav.Block),
            Triple("mailto", "mailto:a@b.c", Nav.External),
            Triple("other site", "https://example.org/", Nav.External),
            Triple("bundled start", "https://appassets.androidplatform.net/start", Nav.Stay),
            Triple("bundled connect", "https://appassets.androidplatform.net/connect?address=tendshelf.home%3A8095", Nav.Connect("tendshelf.home:8095")),
            Triple("bundled connect, no address", "https://appassets.androidplatform.net/connect", Nav.Connect("")),
            Triple("bundled over http", "http://appassets.androidplatform.net/start", Nav.External),
        )
        for ((name, url, want) in cases) assertEquals(want, decide(url, true, good, true, url.startsWith("https://$BUNDLED_HOST")), name)
    }

    @Test
    fun decideDoneNeedsEverything() {
        val done = "https://good.example/app/connect/done?code=XYZ"
        assertEquals(Nav.Block, decide(done, false, good, true, false), "subframe")
        assertEquals(Nav.Block, decide(done, true, good, false, false), "nothing pending")
        assertEquals(Nav.Block, decide("https://good.example/app/connect/done", true, good, true, false), "no code")
        assertEquals(Nav.Block, decide("https://good.example/app/connect/done?code=", true, good, true, false), "empty code")
        assertEquals(Nav.External, decide("https://evil.example/app/connect/done?code=XYZ", true, good, true, false), "other origin")
        assertEquals(Nav.External, decide(done, true, null, true, false), "no origin chosen")
        assertEquals(Nav.Stay, decide("https://good.example/app/connect/done/", true, good, true, false), "trailing slash is another page")
    }

    @Test
    fun decideSubframes() {
        assertEquals(Nav.Block, decide("https://example.org/", false, good, true, false))
        assertEquals(Nav.Stay, decide("https://good.example/x", false, good, true, false))
        assertNotNull(decide("mailto:a@b.c", false, good, false, false))
    }

    @Test
    fun bundledCommandsAreObeyedOnlyFromABundledMainFrame() {
        val b = "https://appassets.androidplatform.net"
        for (path in listOf("/connect?address=evil.example", "/remove?id=x", "/play?id=x", "/toggle", "/start", "/link?link=https%3A%2F%2Fevil.example%2Finvite%2Fx")) {
            assertEquals(Nav.Block, decide(b + path, true, good, false, false), "from a book page: $path")
            assertEquals(Nav.Block, decide(b + path, false, good, false, true), "subframe: $path")
        }
        assertEquals(Nav.RemoveBook("x"), decide("$b/remove?id=x", true, good, false, true))
        assertEquals(Nav.External, decide("https://example.org/", true, good, false, false), "an outside link opens the browser")
    }

    @Test
    fun linkIsObeyedOnlyFromABundledMainFrame() {
        val b = "https://appassets.androidplatform.net/link?link=https%3A%2F%2Fa.example%2Finvite%2FX"
        assertEquals(Nav.Link("https://a.example/invite/X"), decide(b, true, good, false, true))
        assertEquals(Nav.Block, decide(b, true, good, false, false), "a book or server page")
        assertEquals(Nav.Block, decide(b, false, good, false, true), "a subframe")
        assertEquals(Nav.Link(""), decide("https://appassets.androidplatform.net/link", true, null, false, true))
        // A server page linking to the app's own scheme never starts anything.
        assertEquals(Nav.Block, decide("tendshelf://open?link=https%3A%2F%2Fa.example%2Finvite%2FX", true, good, false, false))
    }

    @Test
    fun inviteAndPairUrls() {
        val o = Origin.parse("https://a.example:8443")!!
        assertEquals("https://a.example:8443/?shell=android&next=%2Finvite%2FTOK", o.inviteUrl("TOK"))
        assertEquals("https://a.example:8443/?shell=android&next=%2Fpair%2FTOK%3Fchallenge%3DCH-_%26name%3DPixel%2B7", o.pairUrl("TOK", "CH-_", "Pixel 7"))
    }
}
