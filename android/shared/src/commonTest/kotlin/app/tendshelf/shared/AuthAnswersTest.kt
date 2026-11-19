package app.tendshelf.shared

import kotlin.test.Test
import kotlin.test.assertEquals

class AuthAnswersTest {
    private val okBody = """{"status":"ok","version":"1","connect":1}"""
    private val oldBody = """{"status":"ok","version":"1"}"""

    @Test
    fun healthBranches() {
        assertEquals(Health.Certificate, healthOf(0, "", true))
        assertEquals(Health.Certificate, healthOf(200, okBody, true), "a certificate failure wins")
        assertEquals(Health.NotTendshelf, healthOf(404, okBody, false))
        assertEquals(Health.NotTendshelf, healthOf(200, "not json", false))
        assertEquals(Health.NotTendshelf, healthOf(200, """{"status":"down","version":"1"}""", false))
        assertEquals(Health.NotTendshelf, healthOf(200, """{"status":"ok","version":"1","app":"other"}""", false))
        assertEquals(Health.OldServer, healthOf(200, oldBody, false))
        assertEquals(Health.Ok, healthOf(200, okBody, false))
    }

    @Test
    fun swapBranches() {
        val body = """{"token":"tok","device":{"id":"d1"},"user":{"id":"u1"}}"""
        assertEquals(Swap.Done(SwapAnswer("tok", "d1", "u1")), swapOf(201, body))
        assertEquals(Swap.Done(SwapAnswer("tok", "d1", "u1")), swapOf(200, body))
        assertEquals(Swap.Expired, swapOf(201, "garbage"))
        assertEquals(Swap.Conflict, swapOf(409, ""))
        assertEquals(Swap.Expired, swapOf(401, ""))
        assertEquals(Swap.Expired, swapOf(400, ""))
        assertEquals(Swap.Unreachable, swapOf(500, ""))
        assertEquals(Swap.Unreachable, swapOf(302, ""))
    }

    @Test
    fun meBranches() {
        assertEquals(Me.Unauthorized, meOf(401, ""))
        assertEquals(Me.Ok("u1"), meOf(200, """{"user":{"id":"u1"}}"""))
        assertEquals(Me.Unreachable, meOf(200, """{"user":{}}"""))
        assertEquals(Me.Unreachable, meOf(200, "nope"))
        assertEquals(Me.Unreachable, meOf(500, ""))
    }
}
