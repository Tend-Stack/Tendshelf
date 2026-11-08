package app.tendshelf.shared

import java.net.URLDecoder
import java.net.URLEncoder
import kotlin.random.Random
import kotlin.test.Test
import kotlin.test.assertEquals

/** The common form-urlencoding must match java.net.URLEncoder/URLDecoder exactly, since the app used those before. */
class FormUrlJavaTest {
    private val table = listOf(
        "", "plain", "a b", "+", "%", "&", "=", "/", "?", "#", "a+b c%d&e=f/g?h#i", "*-._",
        "éèüñ", "中文日本語", "😀 🎧", "line\nbreak\ttab", "\u0000\u007f\u0080",
        "https://example.com:8443/pair/abc?x=1&y=é",
        // Lone surrogates: URLEncoder writes '?' (%3F) for them, and so does formUrlEncode.
        "\uD83D", "\uDE00", "a\uD83Db", "\uDE00\uD83D",
    )

    private fun javaEncode(s: String) = URLEncoder.encode(s, "UTF-8")

    private fun javaDecode(s: String): String? = try { URLDecoder.decode(s, "UTF-8") } catch (_: IllegalArgumentException) { null }

    private fun ourDecode(s: String): String? = try { formUrlDecode(s) } catch (_: IllegalArgumentException) { null }

    @Test fun encodeMatchesTable() {
        for (s in table) assertEquals(javaEncode(s), formUrlEncode(s), "encode of ${s.map { it.code }}")
    }

    @Test fun decodeMatchesTable() {
        for (s in table) {
            val e = javaEncode(s)
            assertEquals(javaDecode(e), formUrlDecode(e))
        }
        for (s in listOf("a+b", "%41%2b%2B", "%e4%b8%ad", "%C3", "%FF%FE", "%ED%A0%80", "x%F0%9F%98", "%C3%28")) {
            assertEquals(javaDecode(s), formUrlDecode(s), s)
        }
    }

    @Test fun malformedEscapesAgree() {
        for (s in listOf("%", "%4", "%zz", "a%g1", "x%", "%%41")) {
            assertEquals(javaDecode(s), ourDecode(s), "decode of $s")
        }
    }

    // The one known difference: Integer.parseInt inside URLDecoder accepts a sign, so "%+1" and "%-1" decode to a
    // byte there. The common decoder wants two hex digits and throws. A link with such an escape is not one the
    // server or the app ever writes (URLEncoder emits only hex digits), so being stricter changes nothing real.
    @Test fun signedEscapesAreRefusedHere() {
        for (s in listOf("%+1", "%-1", "%1-")) {
            assertEquals(null, ourDecode(s), s)
        }
    }

    @Test fun seededSweep() {
        val random = Random(20261108)
        val pool = "abcXYZ019 +%&=/?#.-*_~é中😀😀\u0000\n"
        repeat(5000) {
            val s = buildString { repeat(random.nextInt(0, 24)) { append(pool[random.nextInt(pool.length)]) } }
            assertEquals(javaEncode(s), formUrlEncode(s), "encode of ${s.map { it.code }}")
        }
        // Arbitrary escape text, including bad escapes: both must agree on throwing and on the result.
        val hex = "0123456789abcdefABCDEFgG%"
        repeat(5000) {
            val s = buildString { repeat(random.nextInt(0, 12)) { append(hex[random.nextInt(hex.length)]) } }
            assertEquals(javaDecode(s), ourDecode(s), "decode of $s")
        }
    }
}
