package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The reference is java.net.URI (the Android answers, run by jvmTest with every row as written). The iOS parser may be
 * stricter and answer null for a text listed in [IOS_STRICTER]; any other row must match on iOS too.
 * A row is `scheme|host|port|userinfo|path|query`, with `~` for null; the expected null means the text is refused.
 */
class RawUriTest {
    internal fun show(u: RawUri?): String? =
        u?.let { listOf(it.scheme, it.host, it.port, it.rawUserInfo, it.rawPath, it.rawQuery).joinToString("|") { f -> f?.toString() ?: "~" } }

    internal val rows: List<Pair<String, String?>> = listOf(
        "https://good.example" to "https|good.example|-1|~||~",
        "https://good.example/" to "https|good.example|-1|~|/|~",
        "http://good.example:8095/x" to "http|good.example|8095|~|/x|~",
        "HTTPS://Good.Example/Path" to "HTTPS|Good.Example|-1|~|/Path|~",
        "https://good.example:443/x" to "https|good.example|443|~|/x|~",
        "https://good.example:/x" to "https|good.example|-1|~|/x|~",
        "https://good.example:0/x" to "https|good.example|0|~|/x|~",
        "https://good.example:65535/" to "https|good.example|65535|~|/|~",
        "https://good.example:65536/" to "https|good.example|65536|~|/|~",
        "https://good.example:99999999999/" to "https|~|-1|~|/|~",
        "https://good.example:-1/" to "https|~|-1|~|/|~",
        "https://good.example:ab/" to "https|~|-1|~|/|~",
        "http://10.0.2.2:8097/" to "http|10.0.2.2|8097|~|/|~",
        "http://[::1]:8095/" to "http|[::1]|8095|~|/|~",
        "http://[fd12::1]/x" to "http|[fd12::1]|-1|~|/x|~",
        "http://[::1/" to null,
        "https://u@good.example/" to "https|good.example|-1|u|/|~",
        "https://u:p@good.example/" to "https|good.example|-1|u:p|/|~",
        "https://good@evil/" to "https|evil|-1|good|/|~",
        "https://good.example@evil.example/" to "https|evil.example|-1|good.example|/|~",
        "https://good.example/a%20b" to "https|good.example|-1|~|/a%20b|~",
        "https://good.example/a%2Fb?x=%2f" to "https|good.example|-1|~|/a%2Fb|x=%2f",
        "https://good.example/a%zz" to null,
        "https://good.example/a%2" to null,
        "https://good.example/a%" to null,
        "https://good.example/?q=1&r=2" to "https|good.example|-1|~|/|q=1&r=2",
        "https://good.example?x=1" to "https|good.example|-1|~||x=1",
        "https://good.example/p?" to "https|good.example|-1|~|/p|",
        "https://good.example/p#frag" to "https|good.example|-1|~|/p|~",
        "https://good.example/p?q=1#frag" to "https|good.example|-1|~|/p|q=1",
        "https://a_b.example/" to "https|~|-1|~|/|~",
        "https://a b.example/" to null,
        "https://good.example/a b" to null,
        "https://good.example\\@evil/" to null,
        "https:///path" to "https|~|-1|~|/path|~",
        "https://" to null,
        "https:" to null,
        "mailto:a@b.c" to "mailto|~|-1|~|~|~",
        "tendshelf://open?link=https%3A%2F%2Fa.example%2Finvite%2FX" to "tendshelf|open|-1|~||link=https%3A%2F%2Fa.example%2Finvite%2FX",
        "tsbundle://app/connect?address=x%3A1" to "tsbundle|app|-1|~|/connect|address=x%3A1",
        "tsbundle://app" to "tsbundle|app|-1|~||~",
        "tsbundle://app:1/connect" to "tsbundle|app|1|~|/connect|~",
        "tsbundle://u@app/connect" to "tsbundle|app|-1|u|/connect|~",
        "javascript:alert(1)" to "javascript|~|-1|~|~|~",
        "data:text/html,hi" to "data|~|-1|~|~|~",
        "intent://x#Intent;end" to "intent|x|-1|~||~",
        "file:///etc/hosts" to "file|~|-1|~|/etc/hosts|~",
        "not a url" to null,
        "" to "~|~|-1|~||~",
        "/relative/path" to "~|~|-1|~|/relative/path|~",
        "https://good.example./x" to "https|good.example.|-1|~|/x|~",
        "https://bücher.example/" to "https|~|-1|~|/|~",
        "https://good.example/ü" to "https|good.example|-1|~|/ü|~",
    )

    /** Texts on which the iOS parser may answer null where java.net.URI answers something (a stricter host, port or form). */
    private val IOS_STRICTER = setOf(
        "https://good.example:65536/",
        "https://good.example:99999999999/",
        "https://good.example:-1/",
        "https://good.example:ab/",
        "https://a_b.example/",
        "https://bücher.example/",
        "https://good.example/ü",
        "mailto:a@b.c",
        "javascript:alert(1)",
        "data:text/html,hi",
        "/relative/path",
        "https://good.example:/x",
    )

    @Test
    fun rowsMatchTheReference() {
        assertTrue(rows.size >= 40, "at least 40 rows")
        val wrong = rows.mapNotNull { (text, want) ->
            val got = show(parseRawUri(text))
            // Only iOS may differ, only by being stricter (null), and only on a listed text.
            if (got == want || (got == null && text in IOS_STRICTER)) null else "$text: wanted $want, got $got"
        }
        assertEquals(emptyList(), wrong)
    }

    @Test
    fun theStricterListIsAllRows() {
        val texts = rows.map { it.first }.toSet()
        for (t in IOS_STRICTER) assertTrue(t in texts, t)
        assertEquals(rows.size, texts.size, "no duplicate rows")
    }
}
