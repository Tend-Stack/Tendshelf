package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class OfflineRoutesTest {
    private val http = Origin.parse("http://10.0.2.2:8105")!!
    private val https = Origin.parse("https://books.example")!!
    private val base = "http://10.0.2.2:8105"

    @Test fun shellAndItsContents() {
        assertEquals(OfflineRoute.Shell("b1", "/books/b1/read"), offlineRoute("$base/books/b1/read", http))
        assertEquals(OfflineRoute.Shell("b1", "/books/b1/read?sheet=contents"), offlineRoute("$base/books/b1/read?sheet=contents", http))
        assertEquals(OfflineRoute.Shell("b1", "/books/b1/read?sheet=contents"), offlineRoute("$base/books/b1/read?ch=2&sheet=contents", http))
    }

    @Test fun chapterQueryMapsToTheShell() {
        assertEquals(OfflineRoute.Shell("b1", "/books/b1/read"), offlineRoute("$base/books/b1/read?ch=2", http))
        assertEquals(OfflineRoute.Shell("b1", "/books/b1/read"), offlineRoute("$base/books/b1/read?ch=2&part=3&edition=e1", http))
    }

    @Test fun otherSheetsAreNotStored() {
        for (s in listOf("search", "marks", "reading", "settings", "other")) assertNull(offlineRoute("$base/books/b1/read?sheet=$s", http), s)
    }

    @Test fun partKeepsQueryOrderAndEdition() {
        assertEquals(
            OfflineRoute.Part("b1", "/hx/books/b1/read/chapters/3?edition=e9&part=2"),
            offlineRoute("$base/hx/books/b1/read/chapters/3?edition=e9&part=2", http),
        )
        assertEquals(
            OfflineRoute.Part("b1", "/hx/books/b1/read/chapters/3?part=2&edition=e9"),
            offlineRoute("$base/hx/books/b1/read/chapters/3?part=2&edition=e9", http),
        )
        assertNull(offlineRoute("$base/hx/books/b1/read/chapters/x?edition=e9", http))
        assertNull(offlineRoute("$base/hx/books/b1/read/chapters/", http))
    }

    @Test fun imagesAndStatic() {
        assertEquals(OfflineRoute.Image("b1", "/books/b1/read/images/blk_1?edition=e1"), offlineRoute("$base/books/b1/read/images/blk_1?edition=e1", http))
        assertEquals(OfflineRoute.Static("/static/css/a.1234.css"), offlineRoute("$base/static/css/a.1234.css", http))
        assertNull(offlineRoute("$base/static/../etc/passwd", http))
        assertNull(offlineRoute("$base/static/", http))
        assertNull(offlineRoute("$base/static/%2e%2e/x", http))
    }

    @Test fun encodedIdsAreRefused() {
        assertNull(offlineRoute("$base/books/b%2F1/read", http))
        assertNull(offlineRoute("$base/books/b.1/read", http))
        assertNull(offlineRoute("$base/hx/books/b%201/read/chapters/1", http))
    }

    @Test fun otherOriginsAreNeverAnswered() {
        assertNull(offlineRoute("http://10.0.2.2:8099/books/b1/read", http))
        assertNull(offlineRoute("http://10.0.2.2/books/b1/read", http))
        assertNull(offlineRoute("http://other.example:8105/books/b1/read", http))
        assertNull(offlineRoute("https://10.0.2.2:8105/books/b1/read", http))
        assertNull(offlineRoute("http://books.example/books/b1/read", https))
        assertNull(offlineRoute("http://user@10.0.2.2:8105/books/b1/read", http))
        assertNull(offlineRoute("https://appassets.androidplatform.net/books/b1/read", http))
    }

    @Test fun downloadLinksAndOtherPathsAreNotAnswered() {
        assertNull(offlineRoute("$base/books/b1/read?download=1", http))
        assertNull(offlineRoute("$base/books/b1", http))
        assertNull(offlineRoute("$base/offline/books/b1/read", http))
        assertNull(offlineRoute("$base/api/v1/books/b1", http))
        assertNull(offlineRoute("$base/login", http))
        assertNull(offlineRoute("not a url", http))
    }

    @Test fun httpsOriginMatchesItsDefaultPort() {
        assertEquals(OfflineRoute.Shell("b1", "/books/b1/read"), offlineRoute("https://books.example:443/books/b1/read", https))
    }

    @Test fun storeKeyIsTheAddressAsSent() {
        assertEquals("/a?b=2&a=1", storeKey("/a?b=2&a=1"))
    }
}
