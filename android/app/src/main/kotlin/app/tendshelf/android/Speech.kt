package app.tendshelf.android

import android.content.Context
import android.media.AudioAttributes
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.speech.tts.TextToSpeech
import android.speech.tts.UtteranceProgressListener
import android.util.Log
import app.tendshelf.shared.BridgeMessage
import app.tendshelf.shared.VoiceInfo
import app.tendshelf.shared.encodeTts
import app.tendshelf.shared.encodeVoices
import app.tendshelf.shared.usableVoices

/**
 * Read aloud through the phone's own speech engine (decision 0032). One lazily created [TextToSpeech]; only voices that
 * need no network are listed or used, and the sentence text goes from the bridge to the engine and nowhere else: it is
 * never logged (the log gets a length) and never sent back. Everything runs on the main thread; the engine's callbacks
 * arrive on a binder thread and are posted over.
 *
 * [reply] is how a result reaches the page that asked last. [drop] forgets it (a new document or sign-out), so an old
 * page never hears about a new one's speech.
 */
class Speech(private val context: Context) {
    private val main = Handler(Looper.getMainLooper())
    private var tts: TextToSpeech? = null

    /** Null while the engine starts; true when it is ready; false when it failed to start. */
    private var ready: Boolean? = null
    private val waiting = ArrayList<() -> Unit>()
    private var reply: ((String) -> Unit)? = null

    /** The number of the sentence being spoken, or 0 for none. Any engine event of another number is stale. */
    private var current = 0
    private var known: Set<String> = emptySet()

    fun voices(send: (String) -> Unit) {
        reply = send
        whenReady { ok ->
            val list = if (ok) listVoices() else emptyList()
            known = list.map { it.id }.toSet()
            reply?.invoke(encodeVoices(list))
        }
    }

    fun say(msg: BridgeMessage.Say, send: (String) -> Unit) {
        reply = send
        Log.d(TAG, "say ${msg.text.length}") // the length only
        whenReady { ok ->
            val engine = tts
            val voice = if (ok && engine != null && msg.voiceId in known) engine.voices?.firstOrNull { it.name == msg.voiceId } else null
            if (engine == null || voice == null || voice.isNetworkConnectionRequired) {
                current = 0
                reply?.invoke(encodeTts(msg.n, "error"))
                return@whenReady
            }
            current = msg.n
            engine.voice = voice
            engine.setSpeechRate(msg.rate.toFloat())
            val result = engine.speak(msg.text, TextToSpeech.QUEUE_FLUSH, Bundle.EMPTY, "u${msg.n}")
            if (result != TextToSpeech.SUCCESS) {
                current = 0
                reply?.invoke(encodeTts(msg.n, "error"))
            }
        }
    }

    /**
     * Stops at once. [silent] is the page's own `tts stop` (it knows); otherwise the page hears `stopped` for the
     * sentence that was speaking, so it shows Paused (the app went to the background).
     */
    fun stop(silent: Boolean = false) {
        val n = current
        current = 0
        if (n != 0) {
            tts?.stop()
            if (!silent) reply?.invoke(encodeTts(n, "stopped"))
        }
    }

    /** Forgets the page that asked, and stops without telling it. */
    fun drop() {
        stop(silent = true)
        reply = null
    }

    fun shutdown() {
        drop()
        waiting.clear()
        tts?.shutdown()
        tts = null
        ready = null
    }

    private fun whenReady(action: (Boolean) -> Unit) {
        when (ready) {
            null -> {
                waiting.add { action(ready == true) }
                if (tts == null) create()
            }
            else -> action(ready == true)
        }
    }

    private fun create() {
        tts = TextToSpeech(context.applicationContext) { status ->
            main.post {
                val engine = tts
                if (status == TextToSpeech.SUCCESS && engine != null) {
                    engine.setAudioAttributes(
                        AudioAttributes.Builder()
                            .setUsage(AudioAttributes.USAGE_MEDIA)
                            .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                            .build(),
                    )
                    engine.setOnUtteranceProgressListener(listener)
                    ready = true
                } else {
                    Log.i(TAG, "speech engine did not start")
                    engine?.shutdown()
                    tts = null
                    ready = false
                }
                val run = ArrayList(waiting)
                waiting.clear()
                run.forEach { it() }
                // A failed start may be retried by the next ask (the engine may have been installed meanwhile).
                if (ready == false) ready = null
            }
        }
    }

    private fun listVoices(): List<VoiceInfo> {
        val engine = tts ?: return emptyList()
        val all = try {
            engine.voices.orEmpty()
        } catch (_: Exception) {
            emptySet() // some engines throw while their service restarts
        }
        val infos = all.map {
            VoiceInfo(
                id = it.name,
                lang = it.locale.toLanguageTag(),
                network = it.isNetworkConnectionRequired,
                notInstalled = it.features.contains(TextToSpeech.Engine.KEY_FEATURE_NOT_INSTALLED),
                isDefault = false,
            )
        }
        val default = try {
            engine.defaultVoice?.name
        } catch (_: Exception) {
            null
        }
        return usableVoices(infos, default)
    }

    private val listener = object : UtteranceProgressListener() {
        override fun onStart(utteranceId: String?) = event(utteranceId, "start")
        override fun onDone(utteranceId: String?) = event(utteranceId, "end")

        @Deprecated("Deprecated in the platform; the two-argument form below is the one called.")
        override fun onError(utteranceId: String?) = event(utteranceId, "error")
        override fun onError(utteranceId: String?, errorCode: Int) = event(utteranceId, "error")

        // A flush by the next sentence and our own stop() both end here; neither is news (stop() has told the page).
        override fun onStop(utteranceId: String?, interrupted: Boolean) {}
    }

    private fun event(utteranceId: String?, ev: String) {
        val n = utteranceId?.removePrefix("u")?.toIntOrNull() ?: return
        main.post {
            if (n != current) return@post
            if (ev != "start") current = 0
            reply?.invoke(encodeTts(n, ev))
        }
    }

    companion object {
        private const val TAG = "Tendshelf"
    }
}
