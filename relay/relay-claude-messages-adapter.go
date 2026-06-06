package relay

import (
	"encoding/json"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"

	"github.com/gin-gonic/gin"
)

type ClaudeMessagesPreparedRequest struct {
	Payload []byte
}

type ClaudeMessagesAdapter interface {
	ValidateChannel(relayInfo *relaycommon.RelayInfo) error
	NormalizeRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) error
	PrepareRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) (*ClaudeMessagesPreparedRequest, error)
	DoRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, prepared *ClaudeMessagesPreparedRequest) (*http.Response, error)
	HandleResponse(c *gin.Context, resp *http.Response, relayInfo *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error)
}
