package web

import (
	"net/http"
	"net/url"
	"strings"
)

// androidPackage is the app's package name. The intent: address below names it, so only this app can
// receive the link; another app that claims the tendshelf: scheme never sees it (decision 0028, D2).
const androidPackage = "app.tendshelf.android"

// appOpenURL is the address of the "Open in the app" button for a server link: an Android intent that
// opens tendshelf://open?link=<link> in the app's package. If the app is not installed Chrome goes to
// the fallback, the same page with ?noapp=1, instead of the Play Store.
func appOpenURL(link string) string {
	q := url.QueryEscape(link)
	q2 := url.QueryEscape(link + "?noapp=1")
	return "intent://open?link=" + q + "#Intent;scheme=tendshelf;package=" + androidPackage + ";S.browser_fallback_url=" + q2 + ";end"
}

// androidBrowser reports whether the request looks like a browser on an Android phone. It only decides
// whether to show the "Open in the app" button, so a wrong guess costs nothing.
func androidBrowser(r *http.Request) bool {
	return shellFrom(r) == "" && strings.Contains(r.UserAgent(), "Android")
}
