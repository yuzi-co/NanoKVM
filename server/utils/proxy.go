package utils

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http/httpproxy"

	"NanoKVM-Server/config"
)

// ProxyFromConfig selects the proxy for an outbound request: the one set on
// the device if there is one, the environment otherwise.
//
// Operators who keep a NanoKVM off the open internet still need it to reach
// the update server, and until now the only way to point it at a proxy was to
// edit the init script that starts the server.
func ProxyFromConfig(r *http.Request) (*url.URL, error) {
	return proxyFor(config.GetInstance().Proxy, r)
}

// OutboundTransport is a transport for reaching the internet, honouring the
// configured proxy. Callers that need their own timeouts should copy it.
func OutboundTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = ProxyFromConfig

	return transport
}

// OutboundClient is a client with an overall deadline that honours the
// configured proxy.
func OutboundClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: OutboundTransport(),
	}
}

// proxyFor resolves the proxy for one request.
//
// httpproxy rather than http.ProxyFromEnvironment: the latter reads the
// environment once for the lifetime of the process, so a proxy set after
// startup would never be seen. It also accepts a bare host:port, which is what
// someone filling in a settings field will write, and sends loopback traffic
// direct.
func proxyFor(configured string, r *http.Request) (*url.URL, error) {
	environment := httpproxy.FromEnvironment()

	address, ok := normalizeProxy(configured)
	if !ok {
		if strings.TrimSpace(configured) != "" {
			// A typo in a settings field must not take updates offline.
			log.Errorf("ignoring unusable proxy %q", configured)
		}

		return environment.ProxyFunc()(r.URL)
	}

	custom := &httpproxy.Config{
		HTTPProxy:  address,
		HTTPSProxy: address,
		NoProxy:    environment.NoProxy,
	}

	return custom.ProxyFunc()(r.URL)
}

// normalizeProxy turns a configured value into an address httpproxy can use,
// or reports that it cannot.
//
// The check is ours because httpproxy does not do one: given "://not a proxy"
// it prefixes http:// and hands back http://://not%20a%20proxy without an
// error, so a mistyped setting would silently become a proxy that never
// answers and every download would fail against it.
func normalizeProxy(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	candidate := value
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}

	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", false
	}

	return candidate, true
}
