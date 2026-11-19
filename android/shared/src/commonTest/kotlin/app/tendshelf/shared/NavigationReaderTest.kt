package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals

/** The iPhone's stored reader origin `tsread://app` (I3d): the policy for a page that already runs there. */
class NavigationReaderTest {
    private val origin = Origin.parse("http://10.0.0.5:8095")!!
    private val reader = BundledSite.IOS_READER

    private fun go(url: String, main: Boolean = true, o: Origin? = origin) =
        decide(url, main, o, false, false, bundled = BundledSite.IOS, reader = reader)

    @Test fun storedShellStays() {
        assertEquals(Nav.Stay, go("tsread://app/books/b1/read"))
        assertEquals(Nav.Stay, go("tsread://app/books/b1/read?sheet=contents"))
        assertEquals(Nav.Stay, go("tsread://app/books/b1/read?ch=2"))
    }

    @Test fun otherPagesGoToTheServer() {
        assertEquals(Nav.Online("http://10.0.0.5:8095/books/b1"), go("tsread://app/books/b1"))
        assertEquals(Nav.Online("http://10.0.0.5:8095/books/b1/read?sheet=search"), go("tsread://app/books/b1/read?sheet=search"))
        assertEquals(Nav.Online("http://10.0.0.5:8095/me/downloads?x=1"), go("tsread://app/me/downloads?x=1"))
    }

    @Test fun subFramesAndNoServerAreBlocked() {
        assertEquals(Nav.Block, go("tsread://app/books/b1", main = false))
        assertEquals(Nav.Block, go("tsread://app/books/b1/read", main = false))
        assertEquals(Nav.Block, go("tsread://app/books/b1/read", o = null))
    }

    @Test fun otherHostsPortsAndUserinfoAreNotTheReader() {
        assertEquals(Nav.External, go("https://tsread.example/books/b1/read"))
        assertEquals(Nav.Block, go("tsread://other/books/b1/read"))
        assertEquals(Nav.Block, go("tsread://app:80/books/b1/read"))
        assertEquals(Nav.Block, go("tsread://u@app/books/b1/read"))
    }

    @Test fun withoutAReaderTheSchemeIsBlocked() {
        assertEquals(Nav.Block, decide("tsread://app/books/b1/read", true, origin, false, false))
    }

    @Test fun bundledCommandsDoNotComeFromTheReader() {
        assertEquals(Nav.Block, decide("tsbundle://app/play?id=b1", true, origin, false, false, bundled = BundledSite.IOS, reader = reader))
    }
}
