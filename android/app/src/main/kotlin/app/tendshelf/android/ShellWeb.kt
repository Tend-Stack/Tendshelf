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
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import app.tendshelf.shared.BUNDLED_HOST
import app.tendshelf.shared.Nav
import app.tendshelf.shared.decide

/**
 * The WebView's settings and clients (plan A2 step 6). One navigation policy, `decide`, answers every main-frame
 * navigation; nothing is injected into any page; file and content access are off; no second window opens.
 */
object ShellWeb {
    const val FILE_CHOOSER = 4102

    @SuppressLint("SetJavaScriptEnabled")
    fun configure(web: WebView, activity: Activity, store: TokenStore, pages: StartPages, flow: () -> ConnectFlow, onNavigated: () -> Unit, onStarted: () -> Unit, saver: FileSaver, chooser: FileChooser) {
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
                when (val nav = decide(request.url.toString(), request.isForMainFrame, store.origin, f.codePending)) {
                    Nav.Stay -> return false
                    Nav.Block -> {}
                    Nav.External -> openOutside(activity, request.url)
                    is Nav.TakeCode -> f.onCode(nav.code)
                    is Nav.Connect -> f.onAddress(nav.address)
                }
                return true
            }

            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? =
                pages.intercept(request.url)

            override fun onPageFinished(view: WebView, url: String) {
                onNavigated()
                if (Uri.parse(url).host != BUNDLED_HOST) flow().onPageFinished(url)
            }

            override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) = onNavigated()

            override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
                onStarted()
                onNavigated()
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame && request.url.host != BUNDLED_HOST) goOffline(view)
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
