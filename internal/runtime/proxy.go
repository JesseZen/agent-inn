package runtime

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	proxySchemeHTTP    = "http"
	proxySchemeHTTPS   = "https"
	proxySchemeSOCKS5  = "socks5"
	proxySchemeSOCKS5H = "socks5h"
)

func ParseProxyURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	proxyURL, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy_url is invalid: %w", err)
	}
	switch proxyURL.Scheme {
	case proxySchemeHTTP, proxySchemeHTTPS, proxySchemeSOCKS5, proxySchemeSOCKS5H:
	default:
		return nil, fmt.Errorf("proxy_url must use http, https, socks5, or socks5h")
	}
	if proxyURL.Host == "" {
		return nil, fmt.Errorf("proxy_url must include host")
	}
	return proxyURL, nil
}

func RedactProxyURL(raw string) string {
	if raw == "" {
		return raw
	}
	proxyURL, err := url.Parse(raw)
	if err != nil {
		if index := strings.LastIndex(raw, "@"); index >= 0 {
			return raw[index+1:]
		}
		return raw
	}
	if proxyURL.User == nil {
		return raw
	}
	proxyURL.User = nil
	return proxyURL.String()
}

func ProxyURLRedacted(raw string) bool {
	return RedactProxyURL(raw) != raw
}
