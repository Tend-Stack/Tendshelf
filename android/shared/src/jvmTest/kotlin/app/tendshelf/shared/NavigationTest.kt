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
        assertEquals("http://10.0.2.2:8097/?shell=android&next=%2Fradio", o.radioUrl())
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
        for (path in listOf("/connect?address=evil.example", "/remove?id=x", "/play?id=x", "/autoshow?id=ps_1&on=1", "/toggle", "/start", "/link?link=https%3A%2F%2Fevil.example%2Finvite%2Fx")) {
            assertEquals(Nav.Block, decide(b + path, true, good, false, false), "from a book page: $path")
            assertEquals(Nav.Block, decide(b + path, false, good, false, true), "subframe: $path")
        }
        assertEquals(Nav.RemoveBook("x"), decide("$b/remove?id=x", true, good, false, true))
        assertEquals(Nav.External, decide("https://example.org/", true, good, false, false), "an outside link opens the browser")
    }

    @Test
    fun theDownloadsPageSwitchesAShowOnAndOff() {
        val b = "https://appassets.androidplatform.net"
        assertEquals(Nav.AutoShow("ps_1", true), decide("$b/autoshow?id=ps_1&on=1", true, good, false, true))
        assertEquals(Nav.AutoShow("ps_1", false), decide("$b/autoshow?id=ps_1&on=0", true, good, false, true))
        assertEquals(Nav.Block, decide("$b/autoshow?id=ps_1&on=2", true, good, false, true))
        assertEquals(Nav.Block, decide("$b/autoshow?id=a%2Fb&on=1", true, good, false, true))
        assertEquals(Nav.PlayBook("pe_abc"), decide("$b/play?id=pe_abc", true, good, false, true), "an episode plays like a book")
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

    @Test
    fun signInProviderOrigins() {
        val idp = setOf(Origin.parse("https://auth.example.com")!!)
        fun d(url: String, main: Boolean = true) = decide(url, main, good, false, false, idp)
        assertEquals(Nav.Stay, d("https://auth.example.com/authorize?x=1"), "provider origin stays")
        assertEquals(Nav.Stay, d("https://AUTH.example.com:443/x"), "case and default port")
        assertEquals(Nav.Block, d("https://auth.example.com/frame", false), "provider sub-frame")
        assertEquals(Nav.External, d("https://other.example.com/"), "unknown origin")
        assertEquals(Nav.External, d("https://auth.example.com:8443/"), "other port")
        assertEquals(Nav.Block, d("https://auth.example.com@evil.example/"), "userinfo trick")
        assertEquals(Nav.Block, d("https://evil.example@auth.example.com/"), "userinfo on provider host")
        assertEquals(Nav.External, d("http://auth.example.com/"), "other scheme")
        assertEquals(Nav.Block, decide("https://$BUNDLED_HOST/connect", true, good, false, false, idp), "bundled rules first")
        assertEquals(Nav.External, decide("https://auth.example.com/", true, good, false, false), "empty set by default")
    }

    @Test
    fun signInOriginsParse() {
        val https = Origin.parse("https://tend.example")!!
        val http = Origin.parse("http://10.0.0.5:8095")!!
        fun p(body: String, s: Origin = https) = parseSignInOrigins(body, s).map { it.text }
        assertEquals(listOf("https://auth.example.com"), p("""{"origins":["https://auth.example.com"]}"""))
        assertEquals(emptyList(), p("""{"origins":[]}"""))
        assertEquals(emptyList(), p("not json"))
        assertEquals(emptyList(), p("""{"origins":"https://a.example"}"""))
        assertEquals(emptyList(), p("""{"origins":["http://auth.example.com","https://u@a.example","https://a.example/path",5,null]}"""))
        assertEquals(listOf("http://10.0.0.5:9091"), p("""{"origins":["http://10.0.0.5:9091","http://other.example"]}""", http))
        assertEquals(4, p("""{"origins":["https://a.example","https://b.example","https://c.example","https://d.example","https://e.example"]}""").size)
        assertEquals(listOf("https://a.example"), p("""{"origins":["https://a.example","https://A.example/"]}"""))
    }
}
