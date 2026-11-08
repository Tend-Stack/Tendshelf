package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class FormUrlKnownTest {
    @Test fun encodesTheFormSet() {
        assertEquals("a+b%2Bc%25%26%3D%2F%3F%23", formUrlEncode("a b+c%&=/?#"))
        assertEquals("A-z_0.9*", formUrlEncode("A-z_0.9*"))
        assertEquals("%C3%A9%E4%B8%AD%F0%9F%98%80", formUrlEncode("é中😀"))
    }

    @Test fun decodesTheFormSet() {
        assertEquals("a b+c%", formUrlDecode("a+b%2Bc%25"))
        assertEquals("é中😀", formUrlDecode("%C3%A9%E4%B8%AD%F0%9F%98%80"))
    }

    @Test fun malformedEscapesThrow() {
        assertFailsWith<IllegalArgumentException> { formUrlDecode("%") }
        assertFailsWith<IllegalArgumentException> { formUrlDecode("ab%4") }
        assertFailsWith<IllegalArgumentException> { formUrlDecode("%zz") }
    }
}
