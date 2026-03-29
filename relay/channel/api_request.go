package channel

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"io"
	"net"
	"net/http"
	"net/url"
	"one-api/relay/common"
	"one-api/relay/constant"
	"one-api/service"
	"strings"

	"golang.org/x/net/proxy"
)

func SetupApiRequestHeader(info *common.RelayInfo, c *gin.Context, req *http.Header) {
	if info.RelayMode == constant.RelayModeAudioTranscription || info.RelayMode == constant.RelayModeAudioTranslation {
		// multipart/form-data
	} else if info.RelayMode == constant.RelayModeRealtime {
		// websocket
	} else {
		req.Set("Content-Type", c.Request.Header.Get("Content-Type"))
		req.Set("Accept", c.Request.Header.Get("Accept"))
		if info.IsStream && c.Request.Header.Get("Accept") == "" {
			req.Set("Accept", "text/event-stream")
		}
	}
}

func newWssDialerWithProxy(proxyURL string) *websocket.Dialer {
	d := *websocket.DefaultDialer

	if proxyURL == "" {
		return &d
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return &d
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		d.Proxy = http.ProxyURL(u)
		return &d
	case "socks5", "socks5h":
		host := u.Host
		if host == "" {
			return &d
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

		socksDialer, err := proxy.SOCKS5("tcp", host, auth, proxy.Direct)
		if err != nil {
			return &d
		}

		d.Proxy = nil
		if cd, ok := socksDialer.(proxy.ContextDialer); ok {
			d.NetDialContext = cd.DialContext
		} else {
			d.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return socksDialer.Dial(network, addr)
			}
		}
		return &d
	default:
		return &d
	}
}

func DoApiRequest(a Adaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	req, err := BuildAPIRequest(a, c, info, requestBody)
	if err != nil {
		return nil, err
	}
	resp, err := DoPreparedRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func BuildAPIRequest(a Adaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Request, error) {
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	req = req.WithContext(c.Request.Context())
	err = a.SetupRequestHeader(c, &req.Header, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	return req, nil
}

func DoFormRequest(a Adaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	req = req.WithContext(c.Request.Context())
	// set form data
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))

	err = a.SetupRequestHeader(c, &req.Header, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}

func DoWssRequest(a Adaptor, c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (*websocket.Conn, error) {
	fullRequestURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	targetHeader := http.Header{}
	err = a.SetupRequestHeader(c, &targetHeader, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	targetHeader.Set("Content-Type", c.Request.Header.Get("Content-Type"))

	dialer := websocket.DefaultDialer
	if info != nil && info.ProxyURL != "" {
		dialer = newWssDialerWithProxy(info.ProxyURL)
	}

	targetConn, _, err := dialer.Dial(fullRequestURL, targetHeader)
	if err != nil {
		return nil, fmt.Errorf("dial failed to %s: %w", fullRequestURL, err)
	}
	// send request body
	//all, err := io.ReadAll(requestBody)
	//err = service.WssString(c, targetConn, string(all))
	return targetConn, nil
}

func doRequest(c *gin.Context, req *http.Request, info interface{}) (*http.Response, error) {
	var client *http.Client
	var proxyURL string

	switch v := info.(type) {
	case *common.RelayInfo:
		if v != nil {
			proxyURL = v.ProxyURL
		}
		// Streaming/SSE requests must NOT use a http.Client timeout, otherwise the stream
		// can be cut off before response.completed/[DONE] are received.
		if v != nil && v.IsStream {
			if proxyURL != "" {
				client = service.GetStreamingHttpClientWithProxy(proxyURL)
			} else {
				client = service.GetStreamingHttpClient()
			}
		}
	case *common.TaskRelayInfo:
		// TaskRelayInfo 暂时不支持代理，使用默认客户端
		client = service.GetHttpClient()
	default:
		client = service.GetHttpClient()
	}

	if client == nil {
		if proxyURL != "" {
			client = service.GetHttpClientWithProxy(proxyURL)
		} else {
			client = service.GetHttpClient()
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("resp is nil")
	}
	_ = req.Body.Close()
	_ = c.Request.Body.Close()
	return resp, nil
}

func DoPreparedRequest(c *gin.Context, req *http.Request, info interface{}) (*http.Response, error) {
	return doRequest(c, req, info)
}

func DoTaskApiRequest(a TaskAdaptor, c *gin.Context, info *common.TaskRelayInfo, requestBody io.Reader) (*http.Response, error) {
	fullRequestURL, err := a.BuildRequestURL(info)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("new request failed: %w", err)
	}
	req = req.WithContext(c.Request.Context())
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(requestBody), nil
	}

	err = a.BuildRequestHeader(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("setup request header failed: %w", err)
	}
	resp, err := doRequest(c, req, info)
	if err != nil {
		return nil, fmt.Errorf("do request failed: %w", err)
	}
	return resp, nil
}
