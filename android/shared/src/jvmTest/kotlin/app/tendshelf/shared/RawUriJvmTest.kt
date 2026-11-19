package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals

class RawUriJvmTest {
    /** java.net.URI is the reference: on the JVM every row is exact, with no IOS_STRICTER leeway. */
    @Test
    fun everyRowIsExact() {
        val t = RawUriTest()
        val wrong = t.rows.mapNotNull { (text, want) ->
            val got = t.show(parseRawUri(text))
            if (got == want) null else "$text: wanted $want, got $got"
        }
        assertEquals(emptyList(), wrong)
    }
}
