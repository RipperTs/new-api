package claudecode

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel"
	relaycommon "one-api/relay/common"
	"strings"
)

const (
	RequestModeCompletion = 1
	RequestModeMessage    = 2
)

type Adaptor struct {
	RequestMode int
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

// 不再兼容 claud 2 版本模型
func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.RequestMode = RequestModeMessage
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if a.RequestMode == RequestModeMessage {
		return fmt.Sprintf("%s/v1/messages?beta=true", info.BaseUrl), nil
	} else {
		return fmt.Sprintf("%s/v1/complete", info.BaseUrl), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("x-api-key", info.ApiKey)
	anthropicVersion := c.Request.Header.Get("anthropic-version")
	if anthropicVersion == "" {
		anthropicVersion = "2023-06-01"
	}
	req.Set("anthropic-version", anthropicVersion)

	// 设置 Claude Code 特有的请求头
	req.Set("X-Stainless-Retry-Count", "0")
	req.Set("X-Stainless-Timeout", "600")
	req.Set("X-Stainless-Lang", "js")
	req.Set("X-Stainless-Package-Version", "0.55.1")
	req.Set("X-Stainless-OS", "MacOS")
	req.Set("X-Stainless-Arch", "arm64")
	req.Set("X-Stainless-Runtime", "node")
	req.Set("x-stainless-helper-method", "stream")
	req.Set("x-app", "cli")
	req.Set("User-Agent", "claude-cli/1.0.44 (external, cli)")
	req.Set("anthropic-beta", "fine-grained-tool-streaming-2025-05-14")
	req.Set("X-Stainless-Runtime-Version", "v20.18.1")
	req.Set("anthropic-dangerous-direct-browser-access", "true")

	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	if a.RequestMode == RequestModeCompletion {
		return RequestOpenAI2ClaudeComplete(*request), nil
	} else {
		return RequestOpenAI2ClaudeMessage(*request)
	}
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	// 读取请求体内容用于日志输出
	bodyBytes, err := io.ReadAll(requestBody)
	if err != nil {
		fmt.Printf("[ClaudeCode] Error reading request body: %v\n", err)
		return nil, err
	}

	// 重新创建 reader 供实际请求使用
	newRequestBody := bytes.NewReader(bodyBytes)

	return channel.DoApiRequest(a, c, info, newRequestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	// 检查响应类型，如果是 text/event-stream，强制使用流式处理
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		fmt.Printf("[ClaudeCode] Detected SSE response, forcing stream mode\n")
		err, usage = ClaudeStreamHandler(c, resp, info, a.RequestMode)
	} else if info.IsStream {
		err, usage = ClaudeStreamHandler(c, resp, info, a.RequestMode)
	} else {
		err, usage = ClaudeHandler(c, resp, a.RequestMode, info)
	}

	fmt.Printf("[ClaudeCode] DoResponse finished, error: %v\n", err)
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
