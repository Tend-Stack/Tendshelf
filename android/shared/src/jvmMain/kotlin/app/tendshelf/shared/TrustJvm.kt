package app.tendshelf.shared

import javax.crypto.AEADBadTagException

/**
 * Whether a failure to read the saved token means the token is gone for good. Only a ciphertext that fails its tag
 * does (the key is a different one). A Keystore that is busy, locked or restarting throws other things and must not
 * sign the person out.
 */
fun tokenIsLost(e: Throwable): Boolean = e is AEADBadTagException
