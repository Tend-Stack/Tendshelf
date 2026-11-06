package app.tendshelf.shared

import java.io.ByteArrayOutputStream
import java.io.InputStream

/**
 * Reads up to [max] bytes (fewer at the end of the stream). The same as `InputStream.readNBytes(int)`, which is API 33
 * on Android and so crashes on Android 9 to 12.
 */
fun InputStream.readAtMost(max: Int): ByteArray {
    val out = ByteArrayOutputStream(minOf(max, 8192).coerceAtLeast(16))
    val buf = ByteArray(8192)
    var left = max
    while (left > 0) {
        val n = read(buf, 0, minOf(buf.size, left))
        if (n < 0) break
        out.write(buf, 0, n)
        left -= n
    }
    return out.toByteArray()
}
