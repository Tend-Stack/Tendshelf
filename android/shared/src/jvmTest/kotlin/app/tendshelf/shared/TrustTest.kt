package app.tendshelf.shared

import java.security.KeyStoreException
import javax.crypto.AEADBadTagException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TrustTest {
    @Test
    fun onlyATendshelfWithTheSameIdIsTheServer() {
        assertFalse(sameServer("abc", null), "no answer")
        assertFalse(sameServer("abc", Identity(null, null)), "a plain server")
        assertFalse(sameServer("abc", Identity("other", "abc")), "another app")
        assertFalse(sameServer("abc", Identity("tendshelf", "xyz")), "another install")
        assertFalse(sameServer("abc", Identity("tendshelf", null)), "the id vanished")
        assertTrue(sameServer("abc", Identity("tendshelf", "abc")))
        assertTrue(sameServer(null, Identity("tendshelf", null)), "an older server has no id")
        assertTrue(sameServer("", Identity("tendshelf", "abc")))
    }

    @Test
    fun onlyABadTagLosesTheToken() {
        assertTrue(tokenIsLost(AEADBadTagException()))
        assertFalse(tokenIsLost(KeyStoreException("busy")))
        assertFalse(tokenIsLost(java.security.InvalidKeyException()))
        assertFalse(tokenIsLost(IllegalStateException()))
        assertEquals(false, tokenIsLost(RuntimeException(AEADBadTagException())), "only the exception itself")
    }
}
