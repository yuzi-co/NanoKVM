package utils

import (
	"net/http"
	"testing"
)

func requestTo(t *testing.T, target string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("setup: %s", err)
	}

	return req
}

func TestConfiguredProxyIsUsed(t *testing.T) {
	proxy, err := proxyFor("http://proxy.lan:3128", requestTo(t, "https://cdn.sipeed.com/latest"))
	if err != nil {
		t.Fatalf("expected the configured proxy to be accepted: %s", err)
	}

	if proxy == nil || proxy.Host != "proxy.lan:3128" {
		t.Fatalf("proxy = %v, want the configured one", proxy)
	}
}

func TestBareHostIsAcceptedAsAProxy(t *testing.T) {
	// Someone filling in a settings field types a host and a port, not a URL.
	proxy, err := proxyFor("proxy.lan:3128", requestTo(t, "https://cdn.sipeed.com/latest"))
	if err != nil {
		t.Fatalf("expected a bare host to be accepted: %s", err)
	}

	if proxy == nil || proxy.Host != "proxy.lan:3128" {
		t.Fatalf("proxy = %v, want the configured one", proxy)
	}
}

func TestEnvironmentIsUsedWhenNothingIsConfigured(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy.lan:8080")

	proxy, err := proxyFor("", requestTo(t, "https://cdn.sipeed.com/latest"))
	if err != nil {
		t.Fatalf("expected the environment proxy to be used: %s", err)
	}

	if proxy == nil || proxy.Host != "env-proxy.lan:8080" {
		t.Fatalf("proxy = %v, want the environment one", proxy)
	}
}

func TestUnusableProxyFallsBackInsteadOfBreakingDownloads(t *testing.T) {
	// A typo in a settings field must not take firmware updates offline.
	t.Setenv("HTTPS_PROXY", "http://env-proxy.lan:8080")

	// httpproxy accepts most of these by pasting http:// on the front, which
	// turns a typo into a proxy that exists and never answers.
	for _, configured := range []string{"://not a proxy", "not a proxy", "http://", " ://"} {
		proxy, err := proxyFor(configured, requestTo(t, "https://cdn.sipeed.com/latest"))
		if err != nil {
			t.Fatalf("expected %q to be survivable: %s", configured, err)
		}

		if proxy == nil || proxy.Host != "env-proxy.lan:8080" {
			t.Fatalf("proxy for %q = %v, want the environment one", configured, proxy)
		}
	}
}

func TestTheDeviceDoesNotProxyItself(t *testing.T) {
	// Reaching a service on this board through an external proxy cannot work,
	// and the environment selector already skips loopback.
	proxy, err := proxyFor("http://proxy.lan:3128", requestTo(t, "http://127.0.0.1:8000/health"))
	if err != nil {
		t.Fatalf("expected a loopback request to be fine: %s", err)
	}

	if proxy != nil {
		t.Fatalf("proxy = %v, want loopback to go direct", proxy)
	}
}
