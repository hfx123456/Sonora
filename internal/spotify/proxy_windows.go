package spotify

import (
	"net/url"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// readSystemProxy reads the proxy Windows is set to.
func readSystemProxy() *url.URL {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	if on, _, _ := k.GetIntegerValue("ProxyEnable"); on == 0 {
		return nil
	}
	server, _, _ := k.GetStringValue("ProxyServer")
	raw := pickProxy(server)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

// pickProxy reads ProxyServer: "host:port", or "http=a:1;https=b:2;socks=c:3".
func pickProxy(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	withScheme := func(addr, fallback string) string {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return ""
		}
		if strings.Contains(addr, "://") {
			return addr
		}
		return fallback + "://" + addr
	}
	if !strings.Contains(server, "=") {
		return withScheme(server, "http")
	}
	entries := map[string]string{}
	for _, part := range strings.Split(server, ";") {
		if k, v, ok := strings.Cut(part, "="); ok {
			entries[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	if v := entries["https"]; v != "" {
		return withScheme(v, "http")
	}
	if v := entries["http"]; v != "" {
		return withScheme(v, "http")
	}
	if v := entries["socks"]; v != "" {
		return withScheme(v, "socks5")
	}
	return ""
}
