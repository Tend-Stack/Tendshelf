package app.tendshelf.shared

private const val HEX = "0123456789ABCDEF"

/** Characters application/x-www-form-urlencoded leaves as they are (the same set as java.net.URLEncoder). */
private fun isUnreserved(c: Char): Boolean =
    c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || c == '.' || c == '-' || c == '*' || c == '_'

/**
 * application/x-www-form-urlencoded, UTF-8, the way java.net.URLEncoder does it: A-Z a-z 0-9 . - * _ stay,
 * a space becomes '+', every other byte becomes %XX in uppercase hex. A lone surrogate has no UTF-8 form;
 * both encoders write it as '?' (%3F).
 */
internal fun formUrlEncode(text: String): String {
    val out = StringBuilder(text.length + 8)
    var i = 0
    while (i < text.length) {
        val c = text[i]
        if (isUnreserved(c)) {
            out.append(c)
            i++
            continue
        }
        if (c == ' ') {
            out.append('+')
            i++
            continue
        }
        val codePoint: Int
        if (c.isHighSurrogate() && i + 1 < text.length && text[i + 1].isLowSurrogate()) {
            codePoint = 0x10000 + ((c.code - 0xD800) shl 10) + (text[i + 1].code - 0xDC00)
            i += 2
        } else if (c.isSurrogate()) {
            codePoint = '?'.code
            i++
        } else {
            codePoint = c.code
            i++
        }
        for (b in utf8Bytes(codePoint)) {
            out.append('%').append(HEX[b shr 4]).append(HEX[b and 0xF])
        }
    }
    return out.toString()
}

private fun utf8Bytes(cp: Int): IntArray = when {
    cp < 0x80 -> intArrayOf(cp)
    cp < 0x800 -> intArrayOf(0xC0 or (cp shr 6), 0x80 or (cp and 0x3F))
    cp < 0x10000 -> intArrayOf(0xE0 or (cp shr 12), 0x80 or ((cp shr 6) and 0x3F), 0x80 or (cp and 0x3F))
    else -> intArrayOf(
        0xF0 or (cp shr 18),
        0x80 or ((cp shr 12) and 0x3F),
        0x80 or ((cp shr 6) and 0x3F),
        0x80 or (cp and 0x3F),
    )
}

private fun hexValue(c: Char): Int = when (c) {
    in '0'..'9' -> c - '0'
    in 'a'..'f' -> c - 'a' + 10
    in 'A'..'F' -> c - 'A' + 10
    else -> -1
}

/**
 * The inverse, like java.net.URLDecoder with UTF-8: '+' is a space, %XX is a byte, and the bytes are read as
 * UTF-8 (invalid sequences become U+FFFD). A '%' not followed by two hex digits throws IllegalArgumentException.
 */
internal fun formUrlDecode(text: String): String {
    val out = StringBuilder(text.length)
    val pending = ArrayList<Byte>()
    fun flush() {
        if (pending.isEmpty()) return
        out.append(pending.toByteArray().decodeToString())
        pending.clear()
    }
    var i = 0
    while (i < text.length) {
        val c = text[i]
        if (c == '%') {
            if (i + 2 >= text.length) throw IllegalArgumentException("Incomplete trailing escape (%) pattern")
            val high = hexValue(text[i + 1])
            val low = hexValue(text[i + 2])
            if (high < 0 || low < 0) throw IllegalArgumentException("Illegal hex characters in escape (%) pattern")
            pending.add(((high shl 4) or low).toByte())
            i += 3
        } else {
            flush()
            out.append(if (c == '+') ' ' else c)
            i++
        }
    }
    flush()
    return out.toString()
}
