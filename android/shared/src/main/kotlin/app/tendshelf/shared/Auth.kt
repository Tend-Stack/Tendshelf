package app.tendshelf.shared

/** What the server answers to a code swap: a token only the first time, the device and person always. */
data class SwapAnswer(val token: String?, val deviceId: String, val userId: String)
