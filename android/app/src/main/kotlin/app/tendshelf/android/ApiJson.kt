package app.tendshelf.android

import app.tendshelf.shared.SwapAnswer
import org.json.JSONObject

/** The only place server JSON is read (org.json), into plain data classes from `shared`. */
object ApiJson {
    /** `{token?,device:{id},user:{id}}` from POST /api/v1/auth/token. Null when it is not that shape. */
    fun swapAnswer(body: String): SwapAnswer? = try {
        val j = JSONObject(body)
        SwapAnswer(
            token = j.optString("token", "").ifEmpty { null },
            deviceId = j.getJSONObject("device").getString("id"),
            userId = j.getJSONObject("user").getString("id"),
        )
    } catch (_: Exception) {
        null
    }

    /** `{user:{id},device:{...}}` from GET /api/v1/me; the person's id is wanted, nothing else. */
    fun meId(body: String): String? = try {
        JSONObject(body).getJSONObject("user").optString("id", "").ifEmpty { null }
    } catch (_: Exception) {
        null
    }

    fun healthOk(body: String): HealthShape? = try {
        val j = JSONObject(body)
        HealthShape(
            ok = j.opt("status") == "ok" && j.has("version") && (!j.has("app") || j.opt("app") == "tendshelf"),
            connect = j.has("connect"),
        )
    } catch (_: Exception) {
        null
    }

    data class HealthShape(val ok: Boolean, val connect: Boolean)

    fun swapRequest(code: String, verifier: String, name: String): String =
        JSONObject().put("code", code).put("verifier", verifier).put("name", name).put("kind", "mobile").toString()
}
