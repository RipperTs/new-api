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
	baseTrim := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(baseTrim, "/v1") {
		path = "/responses"
	} else if strings.HasSuffix(baseTrim, "/codex") {
		// 官方：https://chatgpt.com/backend-api/codex -> /responses
		path = "/responses"
	}

	// 透传 /v1/responses 的子路径（例如 /v1/responses/compact）
	// 同时兼容 /v1/codex-cli/responses/*（base_url=/v1/codex-cli 时 Codex CLI 会这样请求）。
	suffix := extractResponsesSuffixFromRequestURLPath(info.RequestURLPath)
	return relaycommon.GetFullRequestURL(base, path+suffix, info.ChannelType), nil
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
		// 自动补齐 chatgpt-account-id：当与 token 解出的 account_id 不一致时，以 token 为准（避免沿用旧账号）
		if tokenData != nil && tokenData.AccountID != "" {
			if v, ok := info.ChannelSetting["chatgpt_account_id"].(string); !ok || strings.TrimSpace(v) == "" || strings.TrimSpace(v) != tokenData.AccountID {
				info.ChannelSetting["chatgpt_account_id"] = tokenData.AccountID
			}
		}
	}
	header.Set("Authorization", "Bearer "+apiKey)

	// Codex Responses 实验标识（同时设置两种大小写，兼容部分网关）
	header.Set("OpenAI-Beta", "responses=experimental")
	header.Set("Openai-Beta", "responses=experimental")
	header.Set("openai-beta", "responses=experimental")

	// SSE / JSON：
	// - /responses/compact：规范为非流式 JSON（Codex CLI 也会调用），这里强制按 JSON 拉取。
	// - /responses：非流式尽量按 JSON 拉取；其它场景默认按 SSE。
	if isResponsesCompactPath(c.Request.URL.Path) {
		header.Set("Accept", "application/json")
	} else if info != nil && info.RelayMode == constant.RelayModeResponses && !info.IsStream {
		header.Set("Accept", "application/json")
	} else {
		header.Set("Accept", "text/event-stream")
		header.Set("Connection", "Keep-Alive")
	}
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

func (a *Adaptor) ConvertResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request map[string]any) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	model, _ := request["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("model is required")
	}

	// 为了不影响预扣配额/敏感词/计费统计，这里返回 JSON RawMessage（上游请求体）并尽量不修改 requestMap 本身。
	upstream := cloneMapStringAny(request)

	// Codex backend-api 对 input 的兼容性更严格：要求 input 必须是 list。
	// 兼容用户用 OpenAI Responses 习惯传 input="..." 的写法。
	normalizeCodexResponsesInput(upstream)

	// /responses/compact：规范为非流式 JSON；避免透传 stream/store 导致上游报 Unsupported parameter。
	if isResponsesCompactPath(c.Request.URL.Path) {
		delete(upstream, "stream")
		delete(upstream, "store")
	}

	// /responses：Codex 上游要求显式传 store=false（若省略会报 “Store must be set to false”）
	// /responses/compact：规范不包含 store 字段，避免注入导致上游报 Unsupported parameter。
	if !isResponsesCompactPath(c.Request.URL.Path) {
		upstream["store"] = false
	}

	base := ""
	if info != nil {
		base = strings.TrimRight(strings.TrimSpace(info.BaseUrl), "/")
	}
	isV1Upstream := strings.HasSuffix(base, "/v1")
	isOAuth := false
	if info != nil && info.ChannelSetting != nil {
		if m, ok := info.ChannelSetting["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
			isOAuth = true
		}
	}

	// chatgpt backend-api 或 API Key 模式的上游对参数兼容性不一：避免 400
	if !isV1Upstream || !isOAuth {
		delete(upstream, "max_output_tokens")
		delete(upstream, "temperature")
		delete(upstream, "top_p")
		delete(upstream, "top_k")
		delete(upstream, "seed")
	}

	// 兼容老字段：max_tokens/max_completion_tokens -> max_output_tokens（仅影响上游请求体）
	if _, ok := upstream["max_output_tokens"]; !ok {
		if v, ok := upstream["max_completion_tokens"]; ok && v != nil {
			upstream["max_output_tokens"] = v
		} else if v, ok := upstream["max_tokens"]; ok && v != nil {
			upstream["max_output_tokens"] = v
		}
	}
	delete(upstream, "max_tokens")
	delete(upstream, "max_completion_tokens")

	info.UpstreamModelName = model
	b, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func isResponsesCompactPath(path string) bool {
	// /v1/responses/compact 或 /v1/codex-cli/responses/compact
	return strings.HasSuffix(strings.TrimSpace(path), "/responses/compact")
}

func extractResponsesSuffixFromRequestURLPath(requestURLPath string) string {
	p := strings.TrimSpace(requestURLPath)
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	// /v1/responses/compact -> /compact
	if strings.HasPrefix(p, "/v1/responses") {
		return strings.TrimPrefix(p, "/v1/responses")
	}
	// /v1/codex-cli/responses/compact -> /compact
	if strings.HasPrefix(p, "/v1/codex-cli") {
		rest := strings.TrimPrefix(p, "/v1/codex-cli")
		// /v1/codex-cli -> ""（默认即 /responses）
		if rest == "" || rest == "/" {
			return ""
		}
		if strings.HasPrefix(rest, "/responses") {
			return strings.TrimPrefix(rest, "/responses")
		}
		return rest
	}
	return ""
}

func cloneMapStringAny(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func normalizeCodexResponsesInput(m map[string]any) {
	if m == nil {
		return
	}
	inputVal, ok := m["input"]
	if !ok || inputVal == nil {
		m["input"] = []any{codexDefaultInputMessage("")}
		return
	}

	switch v := inputVal.(type) {
	case string:
		m["input"] = []any{codexDefaultInputMessage(v)}
	case []any:
		// 允许空数组，但很多上游仍会 400；这里给个最小占位，避免直接报错。
		if len(v) == 0 {
			m["input"] = []any{codexDefaultInputMessage("")}
		}
	case map[string]any:
		// 单对象 -> list
		m["input"] = []any{v}
	default:
		// 兜底：保持原样（交给上游报错），但尽量别传 nil
	}
}

func codexDefaultInputMessage(text string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{
			map[string]any{
				"type": "input_text",
				"text": text,
			},
		},
	}
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
	isOAuth := false
	if info != nil {
		if m, ok := info.ChannelSetting["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
			isOAuth = true
		}
	}

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
	// 与可选参数同策略：仅 OAuth + /v1 上游才透传 max_output_tokens，避免镜像站参数不兼容导致 400。
	if isV1Upstream && isOAuth {
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
	// 经验：API Key 模式的 Codex 上游（尤其镜像站）对参数兼容性不一，容易出现 Unsupported parameter，
	// 这里对齐 OAuth 行为：仅 OAuth + /v1 上游才透传这些可选参数。
	if isV1Upstream && isOAuth {
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
	if info.RelayMode == constant.RelayModeResponses {
		if info.IsStream {
			err, usage = codexCLIPassthroughStreamHandler(c, resp, info)
		} else {
			err, usage = codexResponsesPassthroughHandler(c, resp, info)
		}
		return
	}
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
