package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ConnectRulesTest {
    @Test
    fun signInPaths() {
        for (p in listOf("/login", "/setup", "/welcome", "/invite/x", "/pair/x", "/app/connect", "/app/connect/done")) assertTrue(signInPath(p), p)
        for (p in listOf("/", "/books", "/login/oidc", "/invite", "/pair", "/me/downloads")) assertFalse(signInPath(p), p)
    }

    private fun step(state: ConnectState, path: String, hasToken: Boolean, connectLoading: Boolean = false, suppress: Boolean = false) =
        afterPageFinished(state, path, hasToken, connectLoading, suppress)

    @Test
    fun pageFinishedBranches() {
        // Held token on /login: check it, unless already reconnecting.
        assertEquals(PageStep.CheckMe, step(ConnectState.Connected, "/login", true))
        assertEquals(PageStep.None, step(ConnectState.Reconnecting, "/login", true))
        // Reconnecting off the sign-in pages: connect again.
        assertEquals(PageStep.BeginConnect, step(ConnectState.Reconnecting, "/books", true))
        assertEquals(PageStep.None, step(ConnectState.Reconnecting, "/books", true, connectLoading = true))
        assertEquals(PageStep.None, step(ConnectState.Reconnecting, "/app/connect", true))
        // No token: connect from any ordinary page, with the exceptions.
        assertEquals(PageStep.BeginConnect, step(ConnectState.NeedsSignIn, "/books", false))
        assertEquals(PageStep.None, step(ConnectState.NeedsSignIn, "/books", false, suppress = true))
        assertEquals(PageStep.None, step(ConnectState.NeedsSignIn, "/books", false, connectLoading = true))
        assertEquals(PageStep.None, step(ConnectState.Connecting, "/books", false))
        assertEquals(PageStep.None, step(ConnectState.NeedsSignIn, "/login", false))
        // Token held and connected on an ordinary page: nothing.
        assertEquals(PageStep.None, step(ConnectState.Connected, "/books", true))
    }

    @Test
    fun pathOfAnOriginsPage() {
        val o = Origin.parse("https://a.example")!!
        assertEquals("/books/b1", o.pathOf("https://a.example/books/b1?x=1"))
        assertEquals("", o.pathOf("https://a.example"))
        assertNull(o.pathOf("https://b.example/books"))
        assertNull(o.pathOf("https://a.example:8443/"))
        assertNull(o.pathOf("https://u@a.example/"))
        assertNull(o.pathOf("not a url"))
    }
}
