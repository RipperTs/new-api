package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"

	"github.com/gin-gonic/gin"
)

type nativeClaudeMessagesAdapter struct{}

func (a *nativeClaudeMessagesAdapter) ValidateChannel(relayInfo *relaycommon.RelayInfo) error {
	if relayInfo.ChannelType != common.ChannelTypeClaudeCode {
		return errors.New("当前渠道不支持 /v1/messages")
	}
	return nil
}

func (a *nativeClaudeMessagesAdapter) NormalizeRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) error {
	return PrepareClaudeCodeMessagesRequest(c, relayInfo, claudeReq, bodyMap)
}

func (a *nativeClaudeMessagesAdapter) PrepareRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) (*ClaudeMessagesPreparedRequest, error) {
	jsonData, err := marshalClaudeRequestBodyWithModelFirst(bodyMap, claudeReq.Model)
	if err != nil {
		return nil, err
	}
	return &ClaudeMessagesPreparedRequest{Payload: jsonData}, nil
}

func (a *nativeClaudeMessagesAdapter) DoRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, prepared *ClaudeMessagesPreparedRequest) (*http.Response, error) {
	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		return nil, fmt.Errorf("invalid api type: %d", relayInfo.ApiType)
	}
	adaptor.Init(relayInfo)
	resp, err := adaptor.DoRequest(c, relayInfo, bytes.NewBuffer(prepared.Payload))
	if err != nil {
		return nil, err
	}
	httpResp, ok := resp.(*http.Response)
	if !ok {
		return nil, errors.New("invalid native Claude Messages response")
	}
	return httpResp, nil
}

func (a *nativeClaudeMessagesAdapter) HandleResponse(c *gin.Context, resp *http.Response, relayInfo *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	return handleClaudeCodeNativeResponse(c, resp, relayInfo)
}

func handleClaudeCodeNativeResponse(c *gin.Context, resp *http.Response, relayInfo *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	if relayInfo.IsStream {
		return streamClaudeCodePassthrough(c, resp, relayInfo)
	}
	usage, err := nonStreamClaudeCodePassthrough(c, resp, relayInfo)
	return usage, nil, err
}

// HandleClaudeCodeNativeChannelTestResponse lets management channel tests reuse
// the same native Claude Messages response path as the online relay.
func HandleClaudeCodeNativeChannelTestResponse(c *gin.Context, resp *http.Response, relayInfo *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	return handleClaudeCodeNativeResponse(c, resp, relayInfo)
}

func ClaudeCodeMessagesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)
	return handleClaudeMessagesWithAdapter(c, relayInfo, &nativeClaudeMessagesAdapter{})
}
