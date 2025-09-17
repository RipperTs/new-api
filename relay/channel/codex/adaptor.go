package codex

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel"
	relaycommon "one-api/relay/common"
	"strings"
)

// Codex 请求结构（对齐 Python Codex2API responses 支持的最小字段）
type codexContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageUrl any    `json:"image_url,omitempty"`
}

type codexInputMessage struct {
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Content []codexContentItem `json:"content"`
}

type codexTool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Strict      bool   `json:"strict"`
	Parameters  any    `json:"parameters,omitempty"`
}

type codexRequest struct {
	Model             string              `json:"model"`
	Instructions      string              `json:"instructions,omitempty"`
	Input             []codexInputMessage `json:"input"`
	Tools             []codexTool         `json:"tools,omitempty"`
	ToolChoice        any                 `json:"tool_choice,omitempty"`
	ParallelToolCalls bool                `json:"parallel_tool_calls,omitempty"`
	Store             bool                `json:"store,omitempty"`
	Stream            bool                `json:"stream,omitempty"`
	Include           []string            `json:"include,omitempty"`
	Reasoning         map[string]any      `json:"reasoning,omitempty"`
}

type Adaptor struct{}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	// 目标上游为 Codex Responses 接口
	// 兼容两类上游：
	// 1) chatgpt.com/backend-api -> /codex/responses
	// 2) apic1.ohmycdn.com/.../v1 -> /responses
	path := "/codex/responses"
	base := info.BaseUrl
	if strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/v1/") {
		path = "/responses"
	}
	return relaycommon.GetFullRequestURL(base, path, info.ChannelType), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Authorization", "Bearer "+info.ApiKey)
	// Codex Responses 实验标识（同时设置两种大小写，兼容部分网关）
	header.Set("OpenAI-Beta", "responses=experimental")
	header.Set("openai-beta", "responses=experimental")
	// 强制 SSE
	header.Set("Accept", "text/event-stream")
	// 固定 UA 与 originator
	header.Set("User-Agent", "codex_cli_rs/0.36.0 (Mac OS 15.5.0; arm64) Apple_Terminal/455.1")
	header.Set("originator", "codex_cli_rs")
	// conversation_id 与 session_id 每次请求自动生成
	// 使用标准 UUID（带连字符），上游大小写不敏感
	cid := uuid.New().String()
	header.Set("conversation_id", cid)
	header.Set("session_id", cid)
	// 可选：chatgpt-account-id 由渠道设置传入
	if v, ok := info.ChannelSetting["chatgpt_account_id"].(string); ok && v != "" {
		header.Set("chatgpt-account-id", v)
	}
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.GeneralOpenAIRequest) (any, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}

	// 指令：优先取 system 消息内容（无则留空）
	instructions := ""
	// 构造 input 消息
	input := make([]codexInputMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		// system -> 提取为 instructions，不进入对话
		if m.Role == "system" {
			if m.IsStringContent() {
				instructions = m.StringContent()
			}
			continue
		}
		// 仅提取文本内容，其他类型忽略（保持最小实现）
		items := m.ParseContent()
		if len(items) == 0 {
			continue
		}
		contentItems := make([]codexContentItem, 0, len(items))
		for _, it := range items {
			switch it.Type {
			case dto.ContentTypeText:
				if it.Text != "" {
					kind := "input_text"
					if m.Role == "assistant" {
						kind = "output_text"
					}
					contentItems = append(contentItems, codexContentItem{Type: kind, Text: it.Text})
				}
			case dto.ContentTypeImageURL:
				// 将 OpenAI image_url 映射为 Codex 的 input_image
				var urlStr string
				switch v := it.ImageUrl.(type) {
				case dto.MessageImageUrl:
					urlStr = v.Url
				case map[string]any:
					if u, ok := v["url"].(string); ok {
						urlStr = u
					}
				case string:
					urlStr = v
				}
				if urlStr != "" {
					contentItems = append(contentItems, codexContentItem{Type: "input_image", ImageUrl: urlStr})
				}
			}
		}
		if len(contentItems) == 0 {
			continue
		}
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		input = append(input, codexInputMessage{Type: "message", Role: role, Content: contentItems})
	}

	// 工具转换（仅 function）
	tools := make([]codexTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		// t.Type 为 any，兼容 string
		ttype, _ := t.Type.(string)
		if ttype != "function" {
			continue
		}
		tools = append(tools, codexTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Strict:      false,
			Parameters:  t.Function.Parameters,
		})
	}

	// Reasoning 参数与 include
	reasoningEffort := req.ReasoningEffort
	if reasoningEffort == "" {
		reasoningEffort = "medium"
	}
	include := []string{}
	if reasoningEffort != "none" {
		include = append(include, "reasoning.encrypted_content")
	}
	reasoning := map[string]any{
		"effort":  reasoningEffort,
		"summary": "auto",
	}

	// Codex 要求始终使用流式返回，上游会按 SSE 返回；非流式在 DoResponse 聚合
	body := codexRequest{
		Model:             req.Model,
		Instructions:      instructions,
		Input:             input,
		Tools:             tools,
		ToolChoice:        req.ToolChoice,
		ParallelToolCalls: true,
		Store:             false,
		Stream:            true,
		Include:           include,
		Reasoning:         reasoning,
	}
	return body, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	if info.IsStream {
		err, usage = codexStreamHandler(c, resp, info)
	} else {
		err, usage = codexHandler(c, resp, info)
	}
	return
}

func (a *Adaptor) GetModelList() []string { return ModelList }
func (a *Adaptor) GetChannelName() string { return ChannelName }
