package app.tendshelf.android

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import app.tendshelf.shared.Origin
import app.tendshelf.shared.tokenIsLost
import javax.crypto.AEADBadTagException
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
            // The pinned install id belongs to one address; another address is another server.
            val same = value != null && value.text == prefs.getString("origin", null)
            prefs.edit().apply {
                if (value == null) remove("origin") else putString("origin", value.text)
                if (!same) remove("serverId")
            }.apply()
        }

    /** The `/healthz` install id seen when this server was connected; empty for a server that sent none. */
    var serverId: String?
        get() = prefs.getString("serverId", null)
        set(value) {
            prefs.edit().apply { if (value.isNullOrEmpty()) remove("serverId") else putString("serverId", value) }.apply()
        }

    val userId: String? get() = prefs.getString("userId", null)
    fun hasToken(): Boolean = prefs.contains("tokenCt")

    override fun current(): Connection? {
        val o = origin ?: return null
        val ct = prefs.getString("tokenCt", null) ?: return null
        val iv = prefs.getString("tokenIv", null) ?: return null
        val device = prefs.getString("deviceId", null) ?: return null
        return try {
            Connection(o.text, decrypt(ct, iv), device)
        } catch (e: Exception) {
            // Only a token that can never be read again signs the person out. A Keystore that is busy, locked or
            // restarting is "not now": the token stays and the next call tries again.
            if (tokenIsLost(e)) clearToken()
            null
        }
    }

    fun save(token: String, deviceId: String, userId: String) {
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(Cipher.ENCRYPT_MODE, key(create = true)!!)
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

    /** Throws on every failure; [current] decides which ones are permanent. A key that is not there is as lost as a bad tag. */
    private fun decrypt(ct: String, iv: String): String {
        val key = key(create = false) ?: throw AEADBadTagException("the key is gone")
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, android.util.Base64.decode(iv, android.util.Base64.NO_WRAP)))
        return String(cipher.doFinal(android.util.Base64.decode(ct, android.util.Base64.NO_WRAP)), Charsets.UTF_8)
    }

    /** The Keystore key. Only [save] may make one: a read that cannot find it must not invent a key that fits nothing. */
    private fun key(create: Boolean): SecretKey? {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        if (!create) return null
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
