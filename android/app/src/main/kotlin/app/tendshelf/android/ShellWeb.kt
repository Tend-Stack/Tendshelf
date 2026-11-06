package app.tendshelf.android

import android.annotation.SuppressLint
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.graphics.Bitmap
import android.net.Uri
import android.net.http.SslError
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import app.tendshelf.shared.BUNDLED_HOST
import app.tendshelf.shared.Nav
import app.tendshelf.shared.decide
import app.tendshelf.shared.offlineRoute
import java.io.FileInputStream

/**
 * The WebView's settings and clients (plan A2 step 6). One navigation policy, `decide`, answers every main-frame
 * navigation; nothing is injected into any page; file and content access are off; no second window opens.
 */
@androidx.media3.common.util.UnstableApi
object ShellWeb {
    const val FILE_CHOOSER = 4102

    @SuppressLint("SetJavaScriptEnabled")
    fun configure(web: WebView, activity: Activity, store: TokenStore, pages: StartPages, flow: () -> ConnectFlow, onNavigated: () -> Unit, onStarted: () -> Unit, saver: FileSaver, chooser: FileChooser, onApp: (Nav) -> Unit, onLogout: () -> Unit, onRendererGone: (WebView) -> Unit) {
        web.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            javaScriptCanOpenWindowsAutomatically = false
            setSupportMultipleWindows(false)
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            saveFormData = false
        }
        web.overScrollMode = android.view.View.OVER_SCROLL_NEVER
        // The WebView draws its own scrollbar, which the page's styles cannot reach. A native app shows none.
        web.isVerticalScrollBarEnabled = false
        web.isHorizontalScrollBarEnabled = false
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG)
        web.setDownloadListener { url, _, contentDisposition, mimeType, _ -> saver.offer(url, contentDisposition, mimeType) }
        web.setOnLongClickListener {
            val type = web.hitTestResult.type
            type == WebView.HitTestResult.IMAGE_TYPE || type == WebView.HitTestResult.SRC_ANCHOR_TYPE ||
                type == WebView.HitTestResult.SRC_IMAGE_ANCHOR_TYPE
        }
        web.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean =
                chooser.show(callback, params)
        }
        web.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val f = flow()
                when (val nav = decide(request.url.toString(), request.isForMainFrame, store.origin, f.codePending, onBundledPage(view))) {
                    Nav.Stay -> return false
                    Nav.Block -> {}
                    Nav.External -> openOutside(activity, request.url)
                    is Nav.TakeCode -> f.onCode(nav.code)
                    is Nav.Connect -> f.onAddress(nav.address)
                    is Nav.Link -> f.onLink(nav.text)
                    // The bundled Downloads page and its rows (A7): the activity acts, the WebView stays where it is.
                    Nav.Downloads, is Nav.PlayBook, is Nav.RemoveBook, Nav.TogglePlay -> onApp(nav)
                }
                return true
            }

            /**
             * The browser engine's renderer died (out of memory, or killed). The default is to take the whole app down,
             * and the app also holds the playback process, so say it was handled and let the activity build a new WebView
             * when it is next on screen. The dead view is of no further use.
             */
            override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                onRendererGone(view)
                return true
            }

            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                // The page's own Sign out is a form POST to /logout. A POST has no navigation callback, so it is seen here
                // (never answered here): it is what tells a chosen sign-out, which deletes downloads, from an expired one.
                val origin = store.origin
                if (request.method == "POST" && origin != null && request.url.path == "/logout" && origin.matches(request.url.toString())) onLogout()
                pages.homeOrigin = origin?.text ?: "" // the bundled Downloads page links its Read buttons here
                return pages.intercept(request.url) ?: storedAnswer(request)
            }

            /**
             * The stored text standing in for the worker (A9): only a GET, only the stored origin, only an address the store holds,
             * and only while the server does not answer (invariant 5). Everything else goes to the network as it always did.
             */
            private fun storedAnswer(request: WebResourceRequest): WebResourceResponse? {
                val origin = store.origin ?: return null
                if (request.method != "GET") return null
                val stored = stored(request.url) ?: return null
                if (OfflineNet.reachable(origin)) return null
                val type = stored.type
                val mime = type.substringBefore(';').trim().ifEmpty { "application/octet-stream" }
                val charset = Regex("charset=([A-Za-z0-9_-]+)", RegexOption.IGNORE_CASE).find(type)?.groupValues?.get(1)
                val headers = HashMap<String, String>()
                headers["Cache-Control"] = "no-store"
                stored.csp?.let { headers["Content-Security-Policy"] = it }
                return WebResourceResponse(mime, charset, 200, "OK", headers, FileInputStream(stored.file))
            }

            private fun stored(url: Uri): Stored? {
                val origin = store.origin ?: return null
                val route = offlineRoute(url.toString(), origin) ?: return null
                return TextStore.lookup(route)
            }

            private var retried = ""

            override fun onPageFinished(view: WebView, url: String) {
                onNavigated()
                retried = ""
                if (Uri.parse(url).host != BUNDLED_HOST) {
                    flow().onPageFinished(url)
                    Offline.onPageLoaded()
                }
            }

            override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) = onNavigated()

            override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
                onStarted()
                onNavigated()
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame && request.url.host != BUNDLED_HOST) {
                    // The server stopped answering inside the 10 s the last probe was remembered: ask again, and if the
                    // page is stored it opens from the phone instead of the bundled page (A9, D2).
                    OfflineNet.invalidate()
                    val url = request.url.toString()
                    if (retried != url && stored(request.url) != null) {
                        retried = url
                        view.loadUrl(url)
                        return
                    }
                    goOffline(view)
                }
            }

            override fun onReceivedHttpError(view: WebView, request: WebResourceRequest, response: WebResourceResponse) {
                if (request.isForMainFrame && request.url.host != BUNDLED_HOST && response.statusCode >= 500) goOffline(view)
            }

            override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                handler.cancel()
                flow().showStart(StartState(store.origin?.text ?: "", activity.getString(R.string.ssl_error)))
            }

            private fun goOffline(view: WebView) {
                pages.offlineAddress = store.origin?.text ?: ""
                view.loadUrl(pages.offlineUrl)
            }
        }
    }

    /** True only when the page on screen is one of the bundled pages (https on the bundled host). */
    private fun onBundledPage(view: WebView): Boolean {
        val u = Uri.parse(view.url ?: return false)
        return u.scheme == "https" && u.host == BUNDLED_HOST
    }

    /** Hands an http(s) or mailto link to the system; the WebView stays where it is. */
    private fun openOutside(activity: Activity, uri: Uri) {
        val intent = if (uri.scheme.equals("mailto", true)) Intent(Intent.ACTION_SENDTO, uri) else Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE)
        try {
            activity.startActivity(intent)
        } catch (_: ActivityNotFoundException) {
        }
    }
}

/** The page's file input (the Add books uploader): the system picker, one callback at a time. */
class FileChooser(private val activity: Activity) {
    private var callback: ValueCallback<Array<Uri>>? = null

    fun show(cb: ValueCallback<Array<Uri>>, params: WebChromeClient.FileChooserParams): Boolean {
        callback?.onReceiveValue(null)
        callback = cb
        return try {
            activity.startActivityForResult(params.createIntent(), ShellWeb.FILE_CHOOSER)
            true
        } catch (_: ActivityNotFoundException) {
            callback = null
            cb.onReceiveValue(null)
            false
        }
    }

    fun onResult(requestCode: Int, resultCode: Int, data: Intent?): Boolean {
        if (requestCode != ShellWeb.FILE_CHOOSER) return false
        callback?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(resultCode, data))
        callback = null
        return true
    }
}
