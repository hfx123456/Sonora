package app

import (
	"net/url"
	"os"
	"strings"
)

const webProxyNote = "；登录页面重启后生效"

// webProxy has WebView2 go through u, or clears any prior --proxy-server
// argument when u is nil. WebView2 reads its arguments as the first web page
// opens, and Relaunch inherits the updated environment variable cleanly.
var webProxy = func(u *url.URL) {
	var kept []string
	for _, arg := range strings.Fields(os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS")) {
		if strings.HasPrefix(arg, "--proxy-server=") || arg == "--proxy-server" {
			continue
		}
		kept = append(kept, arg)
	}
	if u != nil {
		// Chromium takes no password here, and knows socks5 alone.
		scheme := u.Scheme
		if scheme == "socks5h" {
			scheme = "socks5"
		}
		kept = append(kept, "--proxy-server="+scheme+"://"+u.Host)
	}
	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", strings.Join(kept, " "))
}
