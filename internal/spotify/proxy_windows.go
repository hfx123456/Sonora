package spotify

import (
	"net/url"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// readSystemProxyConfig reads Windows Internet Settings proxy & bypass rules.
func readSystemProxyConfig() systemProxyConfig {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return systemProxyConfig{}
	}
	defer k.Close()
	if on, _, _ := k.GetIntegerValue("ProxyEnable"); on == 0 {
		return systemProxyConfig{}
	}
	server, _, _ := k.GetStringValue("ProxyServer")
	override, _, _ := k.GetStringValue("ProxyOverride")
	return parseWindowsProxyConfig(server, override)
}

func parseWindowsProxyConfig(server, override string) systemProxyConfig {
	parseRaw := func(raw string) *url.URL {
		if raw == "" {
			return nil
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return nil
		}
		return u
	}
	return systemProxyConfig{
		httpProxy:  parseRaw(pickProxyForScheme(server, "http")),
		httpsProxy: parseRaw(pickProxyForScheme(server, "https")),
		socksProxy: parseRaw(pickProxyForScheme(server, "socks")),
		override:   override,
	}
}

// pickProxy reads ProxyServer: "host:port", or "http=a:1;https=b:2;socks=c:3".
func pickProxy(server string) string {
	return pickProxyForScheme(server, "https")
}

func pickProxyForScheme(server, reqScheme string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	withScheme := func(addr, fallback string) string {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return ""
		}
		if idx := strings.Index(addr, "://"); idx >= 0 {
			scheme := strings.ToLower(addr[:idx])
			rest := addr[idx+3:]
			if scheme == "socks" {
				scheme = "socks5"
			}
			return scheme + "://" + rest
		}
		return fallback + "://" + addr
	}
	parts := strings.FieldsFunc(server, func(r rune) bool {
		return r == ';' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	})
	entries := map[string]string{}
	var bare string
	for _, part := range parts {
		if k, v, ok := strings.Cut(part, "="); ok {
			key := strings.ToLower(strings.TrimSpace(k))
			val := strings.TrimSpace(v)
			if val != "" {
				entries[key] = val
			}
		} else if bare == "" {
			bare = part
		}
	}
	if len(entries) == 0 {
		return withScheme(bare, "http")
	}
	var order []string
	switch strings.ToLower(reqScheme) {
	case "http":
		order = []string{"http", "https", "socks"}
	case "socks":
		order = []string{"socks", "https", "http"}
	default:
		order = []string{"https", "http", "socks"}
	}
	for _, k := range order {
		if v := entries[k]; v != "" {
			fb := "http"
			if k == "socks" {
				fb = "socks5"
			}
			return withScheme(v, fb)
		}
	}
	return ""
}
