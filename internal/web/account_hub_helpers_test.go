package web

import "strings"

// accountHubMain is the page from <main> on: the navigation's account chip names the signed-in person
// and their kind ("Member"), words that tests of page content must not trip over.
func accountHubMain(body string) string {
	if i := strings.Index(body, "<main"); i >= 0 {
		return body[i:]
	}
	return body
}
