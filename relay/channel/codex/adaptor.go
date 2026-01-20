package codex

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel"
	relaycommon "one-api/relay/common"
	"one-api/relay/constant"
	"one-api/service"
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

type codexTextFormat struct {
	Type   string `json:"type,omitempty"`
	Name   string `json:"name,omitempty"`
	Strict any    `json:"strict,omitempty"`
	Schema any    `json:"schema,omitempty"`
}

type codexText struct {
	Format *codexTextFormat `json:"format,omitempty"`
}

type codexRequest struct {
	Model             string      `json:"model"`
	Instructions      string      `json:"instructions"`
	Input             []any       `json:"input"`
	Tools             []codexTool `json:"tools,omitempty"`
	ToolChoice        any         `json:"tool_choice,omitempty"`
	ParallelToolCalls bool        `json:"parallel_tool_calls,omitempty"`
	// Codex 上游要求显式传 store=false（若省略会报 “Store must be set to false”）
	Store           bool           `json:"store"`
	Stream          bool           `json:"stream,omitempty"`
	Include         []string       `json:"include,omitempty"`
	Reasoning       map[string]any `json:"reasoning,omitempty"`
	MaxOutputTokens uint           `json:"max_output_tokens,omitempty"`
	Temperature     *float64       `json:"temperature,omitempty"`
	TopP            float64        `json:"top_p,omitempty"`
	Seed            float64        `json:"seed,omitempty"`
	Text            *codexText     `json:"text,omitempty"`
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
	} else if strings.HasSuffix(base, "/codex") || strings.HasSuffix(base, "/codex/") {
		// 官方：https://chatgpt.com/backend-api/codex -> /responses
		path = "/responses"
	}
	return relaycommon.GetFullRequestURL(base, path, info.ChannelType), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)

	apiKey := info.ApiKey
	isOAuth := false
	if m, ok := info.ChannelSetting["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
		isOAuth = true
		token, tokenData, err := service.CodexGetAccessToken(c.Request.Context(), info.ChannelId, info.ApiKey, info.ProxyURL)
		if err != nil {
			return err
		}
		apiKey = token
		// 自动补齐 chatgpt-account-id（优先使用渠道 setting 的显式配置）
		if tokenData != nil && tokenData.AccountID != "" {
			if v, ok := info.ChannelSetting["chatgpt_account_id"].(string); !ok || strings.TrimSpace(v) == "" {
				info.ChannelSetting["chatgpt_account_id"] = tokenData.AccountID
			}
		}
	}
	header.Set("Authorization", "Bearer "+apiKey)

	// Codex Responses 实验标识（同时设置两种大小写，兼容部分网关）
	header.Set("OpenAI-Beta", "responses=experimental")
	header.Set("Openai-Beta", "responses=experimental")
	header.Set("openai-beta", "responses=experimental")
	// 强制 SSE
	header.Set("Accept", "text/event-stream")
	header.Set("Connection", "Keep-Alive")
	// Version：Codex CLI 会带该头；缺省给一个常用版本避免上游拒绝
	if strings.TrimSpace(c.Request.Header.Get("Version")) != "" {
		header.Set("Version", c.Request.Header.Get("Version"))
	} else {
		header.Set("Version", "0.21.0")
	}
	// 固定 UA 与 originator
	if ua := strings.TrimSpace(c.Request.UserAgent()); ua != "" {
		header.Set("User-Agent", ua)
	} else {
		header.Set("User-Agent", "codex_cli_rs/0.50.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464")
	}
	// 对齐 CLIProxyAPI：仅 OAuth 模式设置 Originator / Chatgpt-Account-Id；API key 模式避免额外头引起上游异常
	if isOAuth {
		header.Set("Originator", "codex_cli_rs")
	}
	// conversation_id 与 session_id 每次请求自动生成
	// 使用标准 UUID（带连字符），上游大小写不敏感
	cid := strings.TrimSpace(c.Request.Header.Get("Conversation_id"))
	if cid == "" {
		cid = uuid.New().String()
	}
	header.Set("Conversation_id", cid)

	sid := strings.TrimSpace(c.Request.Header.Get("Session_id"))
	if sid == "" {
		sid = cid
	}
	header.Set("Session_id", sid)
	// 可选：chatgpt-account-id 由渠道设置传入
	if isOAuth {
		if v, ok := info.ChannelSetting["chatgpt_account_id"].(string); ok && v != "" {
			header.Set("Chatgpt-Account-Id", v)
		}
	}
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.GeneralOpenAIRequest) (any, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}

	base := ""
	if info != nil {
		base = strings.TrimRight(strings.TrimSpace(info.BaseUrl), "/")
	}
	isV1Upstream := strings.HasSuffix(base, "/v1")

	// 对齐 CLIProxyAPI：instructions 始终存在，但 system 消息作为 role=developer 的 message 写入 input
	instructions := ""
	input := make([]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "tool":
			// tool -> Codex: function_call_output
			content := strings.TrimSpace(m.StringContent())
			if content == "" {
				continue
			}
			callID := strings.TrimSpace(m.ToolCallId)
			if callID == "" {
				continue
			}
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  content,
			})
			continue
		case "system":
			// system -> role=developer
			m.Role = "developer"
		}

		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		if role != "assistant" && role != "user" && role != "developer" {
			role = "user"
		}

		contentItems, err := convertChatMessageContentToCodex(m, role)
		if err != nil || len(contentItems) == 0 {
			continue
		}

		input = append(input, map[string]any{
			"type":    "message",
			"role":    role,
			"content": contentItems,
		})

		// assistant tool_calls -> Codex: top-level function_call
		if role == "assistant" {
			for _, tc := range m.ParseToolCalls() {
				ttype, _ := tc.Type.(string)
				if ttype != "function" {
					continue
				}
				callID := strings.TrimSpace(tc.ID)
				if callID == "" {
					callID = uuid.New().String()
				}
				name := strings.TrimSpace(tc.Function.Name)
				args := tc.Function.Arguments
				if name == "" {
					continue
				}
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      name,
					"arguments": args,
				})
			}
		}
	}

	// 兜底：input 不能为空，否则上游可能直接 400
	if len(input) == 0 {
		input = append(input, map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": ""},
			},
		})
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
	include := []string(nil)
	reasoning := map[string]any(nil)
	reasoningEffort := strings.TrimSpace(req.ReasoningEffort)
	if reasoningEffort != "" && reasoningEffort != "none" {
		include = append(include, "reasoning.encrypted_content")
		reasoning = map[string]any{
			"effort":  reasoningEffort,
			"summary": "auto",
		}
	}

	// Codex 要求始终使用流式返回，上游会按 SSE 返回；非流式在 DoResponse 聚合
	var maxOut uint = 0
	if isV1Upstream {
		maxOut = req.MaxCompletionTokens
		if maxOut == 0 {
			maxOut = req.MaxTokens
		}
	}

	// response_format -> Responses API: text.format
	text := convertResponseFormatToCodexText(req.ResponseFormat)

	// 官方 chatgpt backend-api 的 Codex 接口对部分参数比较严格（参照 CLIProxyAPI），这里避免传入不支持字段
	var temperature *float64
	var topP float64
	var seed float64
	if isV1Upstream {
		temperature = req.Temperature
		topP = req.TopP
		seed = req.Seed
	}
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
		MaxOutputTokens:   maxOut,
		Temperature:       temperature,
		TopP:              topP,
		Seed:              seed,
		Text:              text,
	}
	return body, nil
}

func convertResponseFormatToCodexText(rf *dto.ResponseFormat) *codexText {
	if rf == nil {
		return nil
	}
	switch strings.TrimSpace(rf.Type) {
	case "text":
		return &codexText{
			Format: &codexTextFormat{Type: "text"},
		}
	case "json_schema":
		if rf.JsonSchema == nil {
			return &codexText{
				Format: &codexTextFormat{Type: "json_schema"},
			}
		}
		return &codexText{
			Format: &codexTextFormat{
				Type:   "json_schema",
				Name:   rf.JsonSchema.Name,
				Strict: rf.JsonSchema.Strict,
				Schema: rf.JsonSchema.Schema,
			},
		}
	default:
		return nil
	}
}

func convertChatMessageContentToCodex(m dto.Message, role string) ([]any, error) {
	// content 可以是 string 或 array（OpenAI chat multimodal）
	if m.IsStringContent() {
		text := strings.TrimSpace(m.StringContent())
		if text == "" {
			return nil, nil
		}
		kind := "input_text"
		if role == "assistant" {
			kind = "output_text"
		}
		return []any{map[string]any{"type": kind, "text": text}}, nil
	}

	var rawItems []map[string]any
	if err := json.Unmarshal(m.Content, &rawItems); err != nil {
		// 兜底：尝试当作任意 JSON
		var anyVal any
		if err2 := json.Unmarshal(m.Content, &anyVal); err2 != nil {
			return nil, err
		}
		return []any{anyVal}, nil
	}

	out := make([]any, 0, len(rawItems))
	for _, it := range rawItems {
		t, _ := it["type"].(string)
		switch t {
		case dto.ContentTypeText:
			txt, _ := it["text"].(string)
			txt = strings.TrimSpace(txt)
			if txt == "" {
				continue
			}
			kind := "input_text"
			if role == "assistant" {
				kind = "output_text"
			}
			out = append(out, map[string]any{"type": kind, "text": txt})
		case dto.ContentTypeImageURL:
			// 仅 user 支持图片输入（对齐 CLIProxyAPI）
			if role != "user" {
				continue
			}
			var urlStr string
			switch v := it["image_url"].(type) {
			case map[string]any:
				if u, ok := v["url"].(string); ok {
					urlStr = u
				}
			case string:
				urlStr = v
			}
			urlStr = strings.TrimSpace(urlStr)
			if urlStr == "" {
				continue
			}
			out = append(out, map[string]any{
				"type":      "input_image",
				"image_url": urlStr,
			})
		case dto.ContentTypeInputAudio:
			out = append(out, map[string]any{
				"type":        "input_audio",
				"input_audio": it["input_audio"],
			})
		case "input_file", "file":
			// 尽量透传文件字段（file_id / filename / file_data 等），仅改 type 以符合 Responses 习惯
			cp := make(map[string]any, len(it))
			for k, v := range it {
				cp[k] = v
			}
			cp["type"] = "input_file"
			out = append(out, cp)
		default:
			// 新特性：尽量原样透传（上游不支持时由上游返回错误）
			out = append(out, it)
		}
	}
	return out, nil
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
	if info.RelayMode == constant.RelayModeCodexCLI {
		if info.IsStream {
			err, usage = codexCLIPassthroughStreamHandler(c, resp, info)
		} else {
			err, usage = codexCLIPassthroughHandler(c, resp, info)
		}
		return
	}
	if info.IsStream {
		err, usage = codexStreamHandler(c, resp, info)
	} else {
		err, usage = codexHandler(c, resp, info)
	}
	return
}

func (a *Adaptor) GetModelList() []string { return ModelList }
func (a *Adaptor) GetChannelName() string { return ChannelName }
