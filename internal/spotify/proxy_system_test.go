//go:build windows || darwin

package spotify

import (
	"net/http"
	"net/url"
	"testing"
)

// System proxy bypass rules must handle IPv6 literals, scheme prefixes, implicit default ports, and NO_PROXY wildcards.
func TestSystemProxyBypassRules(t *testing.T) {
	uIPv6, _ := url.Parse("https://[2606:4700::1]/")
	if matchProxyOverrideURL("<local>", uIPv6) {
		t.Fatal("<local> must not bypass public IPv6 literals")
	}
	uLocal, _ := url.Parse("http://intranet/")
	if !matchProxyOverrideURL("<local>", uLocal) {
		t.Fatal("<local> must bypass simple intranet hostnames")
	}
	uScheme, _ := url.Parse("https://api.corp.com/")
	if !matchProxyOverrideURL("https://*.corp.com:443", uScheme) {
		t.Fatal("scheme + implicit default port ProxyOverride rule failed to match")
	}
	uHTTP, _ := url.Parse("http://api.corp.com/")
	if matchProxyOverrideURL("https://*.corp.com", uHTTP) {
		t.Fatal("https-only ProxyOverride rule wrongly matched http URL")
	}

	t.Setenv("NO_PROXY", "localhost, *")
	if !envNoProxyBypass(uScheme) {
		t.Fatal("NO_PROXY wildcard inside list did not bypass")
	}
	t.Setenv("NO_PROXY", "api.corp.com:443")
	if !envNoProxyBypass(uScheme) {
		t.Fatal("NO_PROXY port-qualified rule failed on implicit default port")
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8080")
	t.Setenv("HTTPS_PROXY", "")
	if hasEnvProxyForScheme("https") {
		t.Fatal("HTTP_PROXY alone should not claim an env proxy for https")
	}
	if !hasEnvProxyForScheme("http") {
		t.Fatal("HTTP_PROXY should apply to http")
	}
	reqHTTPS, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if reqHTTPS == nil {
		t.Fatal("nil request")
	}
}
