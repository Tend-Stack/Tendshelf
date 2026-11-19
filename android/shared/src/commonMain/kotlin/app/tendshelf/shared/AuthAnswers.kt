package app.tendshelf.shared

/** What `/healthz` said, as the connect flow needs it. */
enum class Health { Ok, NotTendshelf, OldServer, Unreachable, Certificate }

/** The result of trading a code and verifier for a device token. */
sealed interface Swap {
    data class Done(val answer: SwapAnswer) : Swap
    object Expired : Swap
    object Conflict : Swap
    object Unreachable : Swap
}

/** The result of asking `/api/v1/me` with the token. */
sealed interface Me {
    data class Ok(val userId: String) : Me
    object Unauthorized : Me
    object Unreachable : Me
}

/**
 * The reading of a `/healthz` answer. A call that got no answer at all is [Health.Unreachable]; the caller says so
 * without calling this. [certificate] is true when the TLS handshake failed.
 */
fun healthOf(status: Int, body: String, certificate: Boolean): Health {
    if (certificate) return Health.Certificate
    if (status != 200) return Health.NotTendshelf
    val h = ApiJson.healthOk(body) ?: return Health.NotTendshelf
    if (!h.ok) return Health.NotTendshelf
    return if (h.connect) Health.Ok else Health.OldServer
}

/** The reading of the answer to `POST /api/v1/auth/token`. */
fun swapOf(status: Int, body: String): Swap = when (status) {
    200, 201 -> ApiJson.swapAnswer(body)?.let { Swap.Done(it) } ?: Swap.Expired
    409 -> Swap.Conflict
    401, 400 -> Swap.Expired
    else -> Swap.Unreachable
}

/** The reading of the answer to `GET /api/v1/me`. */
fun meOf(status: Int, body: String): Me = when {
    status == 401 -> Me.Unauthorized
    status == 200 -> ApiJson.meId(body)?.let { Me.Ok(it) } ?: Me.Unreachable
    else -> Me.Unreachable
}
