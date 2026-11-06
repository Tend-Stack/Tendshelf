package app.tendshelf.android

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import app.tendshelf.shared.Origin
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * The device token, encrypted with an AES-256-GCM key that lives in the Android Keystore and never leaves it.
 * The key needs no screen unlock, because the playback service reads the token while the phone is locked.
 * The token is sent only by native HTTP; nothing here logs it or hands it to the WebView. A token that cannot be
 * decrypted counts as signed out.
 */
class TokenStore(context: Context) : ConnectionSource {
    private val prefs = context.applicationContext.getSharedPreferences("tendshelf", Context.MODE_PRIVATE)

    /** The server the person chose; kept after sign-out so the start page can fill it in. It is not a secret. */
    var origin: Origin?
        get() = prefs.getString("origin", null)?.let(Origin::parse)
        set(value) {
            prefs.edit().apply { if (value == null) remove("origin") else putString("origin", value.text) }.apply()
        }

    val userId: String? get() = prefs.getString("userId", null)
    fun hasToken(): Boolean = prefs.contains("tokenCt")

    override fun current(): Connection? {
        val o = origin ?: return null
        val ct = prefs.getString("tokenCt", null) ?: return null
        val iv = prefs.getString("tokenIv", null) ?: return null
        val device = prefs.getString("deviceId", null) ?: return null
        val token = decrypt(ct, iv)
        if (token == null) {
            clearToken()
            return null
        }
        return Connection(o.text, token, device)
    }

    fun save(token: String, deviceId: String, userId: String) {
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val ct = cipher.doFinal(token.toByteArray(Charsets.UTF_8))
        prefs.edit()
            .putString("tokenCt", android.util.Base64.encodeToString(ct, android.util.Base64.NO_WRAP))
            .putString("tokenIv", android.util.Base64.encodeToString(cipher.iv, android.util.Base64.NO_WRAP))
            .putString("deviceId", deviceId)
            .putString("userId", userId)
            .apply()
    }

    /** Rebinding keeps the token; only the device and person it belongs to are refreshed. */
    fun saveIds(deviceId: String, userId: String) {
        prefs.edit().putString("deviceId", deviceId).putString("userId", userId).apply()
    }

    fun clearToken() {
        prefs.edit().remove("tokenCt").remove("tokenIv").remove("deviceId").remove("userId").apply()
    }

    fun httpWarned(o: Origin): Boolean = prefs.getBoolean("httpWarned.${o.text}", false)
    fun markHttpWarned(o: Origin) = prefs.edit().putBoolean("httpWarned.${o.text}", true).apply()

    private fun decrypt(ct: String, iv: String): String? = try {
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, android.util.Base64.decode(iv, android.util.Base64.NO_WRAP)))
        String(cipher.doFinal(android.util.Base64.decode(ct, android.util.Base64.NO_WRAP)), Charsets.UTF_8)
    } catch (_: Exception) {
        null
    }

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        gen.init(
            KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return gen.generateKey()
    }

    private companion object {
        const val ALIAS = "tendshelf.token"
        const val TRANSFORM = "AES/GCM/NoPadding"
    }
}
