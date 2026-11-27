//go:build windows || darwin

package spotify

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type systemProxyConfig struct {
	httpProxy  *url.URL
	httpsProxy *url.URL
	socksProxy *url.URL
	override   string
}

func (c systemProxyConfig) proxyFor(req *http.Request) *url.URL {
	if req == nil || req.URL == nil {
		return nil
	}
	if matchProxyOverrideURL(c.override, req.URL) {
		return nil
	}
	switch strings.ToLower(req.URL.Scheme) {
	case "http":
		if c.httpProxy != nil {
			return c.httpProxy
		}
		if c.httpsProxy != nil {
			return c.httpsProxy
		}
		return c.socksProxy
	default:
		if c.httpsProxy != nil {
			return c.httpsProxy
		}
		if c.httpProxy != nil {
			return c.httpProxy
		}
		return c.socksProxy
	}
}

func defaultPortForScheme(scheme string) string {
	switch strings.ToLower(scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func hasEnvProxyForScheme(scheme string) bool {
	var keys []string
	switch strings.ToLower(scheme) {
	case "http":
		keys = []string{"HTTP_PROXY", "http_proxy"}
	case "https":
		keys = []string{"HTTPS_PROXY", "https_proxy"}
	default:
		keys = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"}
	}
	for _, key := range keys {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func envNoProxyBypass(u *url.URL) bool {
	if u == nil {
		return false
	}
	noProxy := os.Getenv("NO_PROXY")
	if noProxy == "" {
		noProxy = os.Getenv("no_proxy")
	}
	noProxy = strings.TrimSpace(noProxy)
	if noProxy == "" {
		return false
	}
	if noProxy == "*" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		port = defaultPortForScheme(u.Scheme)
	}
	ip := net.ParseIP(host)
	for _, entry := range strings.Split(noProxy, ",") {
		p := strings.ToLower(strings.TrimSpace(entry))
		if p == "" {
			continue
		}
		if p == "*" {
			return true
		}
		if _, netw, err := net.ParseCIDR(p); err == nil {
			if ip != nil && netw.Contains(ip) {
				return true
			}
			continue
		}
		pHost, pPort, err := net.SplitHostPort(p)
		if err != nil {
			pHost = strings.Trim(p, "[]")
			pPort = ""
		}
		if pPort != "" && pPort != port {
			continue
		}
		if pHost == host {
			return true
		}
		pHost = strings.TrimPrefix(pHost, "*.")
		if suffix, ok := strings.CutPrefix(pHost, "."); ok && (host == suffix || strings.HasSuffix(host, "."+suffix)) {
			return true
		}
		if pHost != "" && strings.HasSuffix(host, "."+pHost) {
			return true
		}
	}
	return false
}

// matchProxyOverride evaluates Windows-style semicolon/space-delimited proxy
// bypass patterns (e.g. "<local>", "*.corp.com", "10.*", "localhost:8080").
func matchProxyOverride(override, hostPort string) bool {
	return matchProxyOverrideWithScheme(override, "", hostPort)
}

func matchProxyOverrideURL(override string, u *url.URL) bool {
	if u == nil {
		return false
	}
	return matchProxyOverrideWithScheme(override, u.Scheme, u.Host)
}

func matchProxyOverrideWithScheme(override, reqScheme, hostPort string) bool {
	override = strings.TrimSpace(override)
	if override == "" || hostPort == "" {
		return false
	}
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = strings.Trim(hostPort, "[]")
		port = ""
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if port == "" && reqScheme != "" {
		port = defaultPortForScheme(reqScheme)
	}
	ip := net.ParseIP(host)
	parts := strings.FieldsFunc(override, func(r rune) bool {
		return r == ';' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	})
	for _, raw := range parts {
		p := strings.ToLower(strings.TrimSpace(raw))
		if p == "" {
			continue
		}
		if p == "<local>" {
			if host != "" && ip == nil && !strings.Contains(host, ".") {
				return true
			}
			continue
		}
		if idx := strings.Index(p, "://"); idx > 0 {
			pScheme := p[:idx]
			if reqScheme != "" && !strings.EqualFold(pScheme, reqScheme) {
				continue
			}
			p = p[idx+3:]
		}
		if slash := strings.IndexByte(p, '/'); slash >= 0 {
			p = p[:slash]
		}
		if p == "" {
			continue
		}
		if _, netw, cidrErr := net.ParseCIDR(p); cidrErr == nil {
			if ip != nil && netw.Contains(ip) {
				return true
			}
			continue
		}
		pHost, pPort, perr := net.SplitHostPort(p)
		if perr != nil {
			// Handle host:port where host contains a glob like *.example.com:8443.
			if strings.Count(p, ":") == 1 {
				if idx := strings.LastIndexByte(p, ':'); idx > 0 {
					pHost, pPort = p[:idx], p[idx+1:]
				} else {
					pHost = p
				}
			} else {
				pHost = strings.Trim(p, "[]")
			}
		}
		if pPort != "" && !matchGlob(pPort, port) {
			continue
		}
		if matchGlob(pHost, host) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, s string) bool {
	p, t := 0, 0
	star, match := -1, 0
	for t < len(s) {
		if p < len(pattern) && pattern[p] == s[t] {
			p++
			t++
		} else if p < len(pattern) && pattern[p] == '*' {
			star = p
			match = t
			p++
		} else if star != -1 {
			p = star + 1
			match++
			t = match
		} else {
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// systemProxy routes requests through the proxy the system is set to, read
// again every few seconds, so switching it needs no restart. Go reads only
// the environment, which wins here too.
func systemProxy(req *http.Request) (*url.URL, error) {
	if req != nil && envNoProxyBypass(req.URL) {
		return nil, nil
	}
	if req != nil && req.URL != nil && hasEnvProxyForScheme(req.URL.Scheme) {
		return http.ProxyFromEnvironment(req)
	}
	proxyMu.Lock()
	if time.Since(proxyRead) > 5*time.Second {
		proxyRead, proxyCfg = time.Now(), readSystemProxyConfig()
	}
	cfg := proxyCfg
	proxyMu.Unlock()
	return cfg.proxyFor(req), nil
}

var (
	proxyMu   sync.Mutex
	proxyRead time.Time
	proxyCfg  systemProxyConfig
)
