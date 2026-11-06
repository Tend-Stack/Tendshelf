package app.tendshelf.android

import app.tendshelf.shared.BatchItemResult
import app.tendshelf.shared.DEFAULT_REWIND
import app.tendshelf.shared.RemoteProgress
import app.tendshelf.shared.RewindStep
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

    fun remoteProgress(it: JSONObject): RemoteProgress = RemoteProgress(
        it.getLong("positionMs"), it.getBoolean("finished"), it.getLong("clientUpdatedAt"),
        it.optString("deviceId"), it.optString("deviceLabel"),
    )

    /** The server's rewind table from the settings answer; the built-in one when it has none. */
    fun rewindSteps(settings: JSONObject): List<RewindStep> {
        val arr = settings.optJSONArray("rewindSteps") ?: return DEFAULT_REWIND
        val steps = (0 until arr.length()).mapNotNull {
            val o = arr.optJSONObject(it) ?: return@mapNotNull null
            RewindStep(o.optLong("fromMs", -1), o.optInt("seconds", -1)).takeIf { s -> s.fromMs >= 0 && s.seconds >= 0 }
        }
        return steps.ifEmpty { DEFAULT_REWIND }
    }

    /** `{"results":[{bookId,applied,progress,error:{code}}]}` from POST /api/v1/progress/batch. */
    fun batchResults(body: String): List<BatchItemResult> {
        val arr = JSONObject(body).getJSONArray("results")
        return (0 until arr.length()).map {
            val o = arr.getJSONObject(it)
            BatchItemResult(
                o.getString("bookId"), o.optBoolean("applied"),
                o.optJSONObject("error")?.optString("code", "error"),
                o.optJSONObject("progress")?.let(::remoteProgress),
            )
        }
    }

    data class HealthShape(val ok: Boolean, val connect: Boolean)

    fun swapRequest(code: String, verifier: String, name: String): String =
        JSONObject().put("code", code).put("verifier", verifier).put("name", name).put("kind", "mobile").toString()
}
