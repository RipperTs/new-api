package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"one-api/common"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

var httpClient *http.Client
var impatientHTTPClient *http.Client
var streamingHTTPClient *http.Client

func init() {
	if common.RelayTimeout == 0 {
		httpClient = &http.Client{}
	} else {
		httpClient = &http.Client{
			Timeout: time.Duration(common.RelayTimeout) * time.Second,
		}
	}

	impatientHTTPClient = &http.Client{
		Timeout: 5 * time.Second,
	}

	// For SSE/streaming requests, net/http.Client.Timeout must be 0, otherwise the
	// stream will be cut off mid-flight (Timeout includes reading the body).
	streamingHTTPClient = &http.Client{}
}

func GetHttpClient() *http.Client {
	return httpClient
}

func GetStreamingHttpClient() *http.Client {
	return streamingHTTPClient
}

func GetImpatientHttpClient() *http.Client {
	return impatientHTTPClient
}

func cloneDefaultTransport() *http.Transport {
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		return dt.Clone()
	}
	return &http.Transport{}
}

func newTransportWithProxy(proxyURL string) (*http.Transport, error) {
	// Keep behavior explicit: require a scheme (http/https/socks5/socks5h).
	if !strings.Contains(proxyURL, "://") {
		return nil, fmt.Errorf("proxy_url missing scheme (expected http(s):// or socks5://): %s", proxyURL)
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		transport := cloneDefaultTransport()
		transport.Proxy = http.ProxyURL(u)
		return transport, nil
	case "socks5", "socks5h":
		host := u.Host
		if host == "" {
			return nil, fmt.Errorf("proxy_url missing host: %s", proxyURL)
		}

		var auth *proxy.Auth
		if u.User != nil {
			user := u.User.Username()
			pass, _ := u.User.Password()
			if user != "" {
				auth = &proxy.Auth{
					User:     user,
					Password: pass,
				}
			}
		}

		d, err := proxy.SOCKS5("tcp", host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}

		transport := cloneDefaultTransport()
		// Avoid env proxy interference when an explicit socks proxy is provided.
		transport.Proxy = nil
		if cd, ok := d.(proxy.ContextDialer); ok {
			transport.DialContext = cd.DialContext
		} else {
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.Dial(network, addr)
			}
		}
		return transport, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (proxy_url=%s)", u.Scheme, proxyURL)
	}
}

func GetHttpClientWithProxy(proxyURL string) *http.Client {
	if proxyURL == "" {
		return httpClient
	}

	transport, err := newTransportWithProxy(proxyURL)
	if err != nil {
		common.SysLog("Failed to init proxy transport: " + err.Error())
		return httpClient
	}

	client := &http.Client{
		Transport: transport,
	}

	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}

	return client
}

func GetStreamingHttpClientWithProxy(proxyURL string) *http.Client {
	if proxyURL == "" {
		return streamingHTTPClient
	}

	transport, err := newTransportWithProxy(proxyURL)
	if err != nil {
		common.SysLog("Failed to init proxy transport: " + err.Error())
		return streamingHTTPClient
	}

	// Timeout must stay 0 for streaming.
	return &http.Client{
		Transport: transport,
	}
}
