package spotify

import (
	"bufio"
	"net"
	"net/url"
	"os/exec"
	"strings"
)

// readSystemProxy reads the proxy macOS is set to, as scutil --proxy shows
// it: what apps opened from the Finder get no environment for.
func readSystemProxyConfig() systemProxyConfig {
	out, err := exec.Command("/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return systemProxyConfig{}
	}
	return parseScutilProxyConfig(string(out))
}

func parseScutilProxyConfig(text string) systemProxyConfig {
	fields, exceptions := scutilProxyFields(text)
	urlFor := func(key, scheme string) *url.URL {
		host, port := fields[key+"Proxy"], fields[key+"Port"]
		if fields[key+"Enable"] != "1" || host == "" || port == "" {
			return nil
		}
		return &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	}
	if fields["ExcludeSimpleHostnames"] == "1" {
		exceptions = append(exceptions, "<local>")
	}
	return systemProxyConfig{
		httpsProxy: urlFor("HTTPS", "http"),
		httpProxy:  urlFor("HTTP", "http"),
		socksProxy: urlFor("SOCKS", "socks5"),
		override:   strings.Join(exceptions, ";"),
	}
}

func scutilProxyFields(text string) (map[string]string, []string) {
	fields := map[string]string{}
	var exceptions []string
	inExceptions := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if inExceptions {
			if line == "}" {
				inExceptions = false
				continue
			}
			if _, v, ok := strings.Cut(line, " : "); ok {
				v = strings.TrimSpace(v)
				if v != "" {
					exceptions = append(exceptions, v)
				}
			}
			continue
		}
		if k, v, ok := strings.Cut(line, " : "); ok {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k == "ExceptionsList" && strings.HasPrefix(v, "<array>") {
				inExceptions = true
				continue
			}
			fields[k] = v
		}
	}
	return fields, exceptions
}

// parseScutilProxy picks the HTTPS proxy, else the HTTP one, else SOCKS,
// whichever is enabled; a proxy auto-configuration file it leaves alone.
func parseScutilProxy(text string) *url.URL {
	cfg := parseScutilProxyConfig(text)
	for _, u := range []*url.URL{cfg.httpsProxy, cfg.httpProxy, cfg.socksProxy} {
		if u != nil {
			return u
		}
	}
	return nil
}
