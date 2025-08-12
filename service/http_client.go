package service

import (
	"net/http"
	"net/url"
	"one-api/common"
	"time"
)

var httpClient *http.Client
var impatientHTTPClient *http.Client

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
}

func GetHttpClient() *http.Client {
	return httpClient
}

func GetImpatientHttpClient() *http.Client {
	return impatientHTTPClient
}

func GetHttpClientWithProxy(proxyURL string) *http.Client {
	if proxyURL == "" {
		return httpClient
	}

	proxyURLParsed, err := url.Parse(proxyURL)
	if err != nil {
		common.SysLog("Failed to parse proxy URL: " + err.Error())
		return httpClient
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURLParsed),
	}

	client := &http.Client{
		Transport: transport,
	}

	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}

	return client
}
