package app.tendshelf.android

import app.tendshelf.shared.ApiJson
import app.tendshelf.shared.Origin
import app.tendshelf.shared.SwapAnswer
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import javax.net.ssl.SSLException

enum class Health { Ok, NotTendshelf, OldServer, Unreachable, Certificate }

sealed interface Swap {
    data class Done(val answer: SwapAnswer) : Swap
    object Expired : Swap
    object Conflict : Swap
    object Unreachable : Swap
}

sealed interface Me {
    data class Ok(val userId: String) : Me
    object Unauthorized : Me
    object Unreachable : Me
}

/**
 * The shell's own HTTP: /healthz, the code swap, /me and revoke. Redirects are never followed, nothing is read past
 * 4 KiB, and the WebView's cookie jar is never used. The token goes in an Authorization header only on /api/v1 calls.
 * Every call blocks; run it off the main thread.
 */
class AuthApi {
    fun health(origin: Origin): Health {
        val r = call(origin.healthUrl(), "GET", null, null, 5_000) ?: return Health.Unreachable
        if (r.certificate) return Health.Certificate
        if (r.status != 200) return Health.NotTendshelf
        val h = ApiJson.healthOk(r.body) ?: return Health.NotTendshelf
        if (!h.ok) return Health.NotTendshelf
        return if (h.connect) Health.Ok else Health.OldServer
    }

    /** The Tendshelf identity at [origin] (`app`, `id`), or null when nothing answered 200 with JSON. Never sends a credential. */
    fun identity(origin: Origin): app.tendshelf.shared.Identity? {
        val r = call(origin.healthUrl(), "GET", null, null, 5_000) ?: return null
        if (r.certificate || r.status != 200) return null
        return ApiJson.identity(r.body)
    }

    /** Exchange a code for a device. [bearer] is the token already held, when the phone is reconnecting. */
    fun swap(origin: Origin, code: String, verifier: String, name: String, bearer: String?): Swap {
        val r = call("${origin.text}/api/v1/auth/token", "POST", ApiJson.swapRequest(code, verifier, name), bearer, 15_000)
            ?: return Swap.Unreachable
        return when (r.status) {
            200, 201 -> ApiJson.swapAnswer(r.body)?.let { Swap.Done(it) } ?: Swap.Expired
            409 -> Swap.Conflict
            401, 400 -> Swap.Expired
            else -> Swap.Unreachable
        }
    }

    fun me(origin: Origin, token: String): Me {
        val r = call(origin.meUrl(), "GET", null, token, 10_000) ?: return Me.Unreachable
        return when {
            r.status == 401 -> Me.Unauthorized
            r.status == 200 -> ApiJson.meId(r.body)?.let { Me.Ok(it) } ?: Me.Unreachable
            else -> Me.Unreachable
        }
    }

    /** Best effort: the device is revoked on the server, and a failure changes nothing here. */
    fun revoke(origin: Origin, token: String) {
        call("${origin.text}/api/v1/auth/token", "DELETE", null, token, 5_000)
    }

    private class Reply(val status: Int, val body: String, val certificate: Boolean = false)

    private fun call(url: String, method: String, json: String?, bearer: String?, timeoutMs: Int): Reply? {
        // The bearer goes only to the server the phone was connected to (ServerGate); a stranger at the address gets nothing.
        if (bearer != null && !ServerGate.ok(URL(url).let { "${it.protocol}://${it.authority}" })) return null
        val c = URL(url).openConnection() as HttpURLConnection
        return try {
            c.instanceFollowRedirects = false
            c.connectTimeout = timeoutMs
            c.readTimeout = timeoutMs
            c.requestMethod = method
            c.setRequestProperty("Accept", "application/json")
            if (bearer != null) c.setRequestProperty("Authorization", "Bearer $bearer")
            if (json != null) {
                c.doOutput = true
                c.setRequestProperty("Content-Type", "application/json")
                c.outputStream.use { it.write(json.toByteArray(Charsets.UTF_8)) }
            }
            val status = c.responseCode
            val stream = if (status >= 400) c.errorStream else c.inputStream
            val body = stream?.use { s ->
                val buf = ByteArray(4096)
                var n = 0
                while (n < buf.size) {
                    val k = s.read(buf, n, buf.size - n)
                    if (k < 0) break
                    n += k
                }
                String(buf, 0, n, Charsets.UTF_8)
            } ?: ""
            Reply(status, body)
        } catch (e: SSLException) {
            Reply(0, "", certificate = true)
        } catch (e: IOException) {
            null
        } finally {
            c.disconnect()
        }
    }
}
