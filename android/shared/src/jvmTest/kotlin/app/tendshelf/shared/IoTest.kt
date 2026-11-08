package app.tendshelf.shared

import java.io.ByteArrayInputStream
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals

class IoTest {
    @Test
    fun readsAtMostTheLimit() {
        val data = ByteArray(20_000) { (it % 251).toByte() }
        assertContentEquals(data.copyOf(10), ByteArrayInputStream(data).readAtMost(10))
        assertContentEquals(data, ByteArrayInputStream(data).readAtMost(20_000))
        assertEquals(20_000, ByteArrayInputStream(data).readAtMost(30_000).size)
        assertEquals(0, ByteArrayInputStream(data).readAtMost(0).size)
        assertEquals(0, ByteArrayInputStream(ByteArray(0)).readAtMost(5).size)
    }
}
