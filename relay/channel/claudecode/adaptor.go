package claudecode

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"one-api/relay/channel"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"strings"
	"time"
)

const (
	RequestModeCompletion = 1
	RequestModeMessage    = 2

	defaultClaudeCLIUserAgent            = "claude-cli/2.1.49 (external, cli)"
	defaultClaudeStainlessTimeout        = "600"
	defaultClaudeStainlessLang           = "js"
	defaultClaudeStainlessPackageVersion = "0.74.0"
	defaultClaudeStainlessOS             = "MacOS"
	defaultClaudeStainlessArch           = "arm64"
	defaultClaudeStainlessRuntime        = "node"
	defaultClaudeStainlessRuntimeVersion = "v22.21.1"
	defaultClaudeAnthropicVersion        = "2023-06-01"
	defaultClaudeAnthropicBeta           = "claude-code-20250219,context-1m-2025-08-07,interleaved-thinking-2025-05-14,prompt-caching-scope-2026-01-05,effort-2025-11-24,adaptive-thinking-2026-01-28"

	fixedClaudeCodeSystemBilling  = "x-anthropic-billing-header: cc_version=2.1.49.7ea; cc_entrypoint=cli; cch=00000;"
	fixedClaudeCodeSystemIdentity = "You are Claude Code, Anthropic's official CLI for Claude."
	fixedClaudeCodeSystemPrompt   = "\nYou are an interactive CLI tool that helps users with software engineering tasks. Use the instructions below and the tools available to you to assist the user.\n\nIMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.\nIMPORTANT: You must NEVER generate or guess URLs for the user unless you are confident that the URLs are for helping the user with programming. You may use URLs provided by the user in their messages or local files.\n\nIf the user asks for help or wants to give feedback inform them of the following:\n- /help: Get help with using Claude Code\n- To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues\n\n# Tone and style\n- Only use emojis if the user explicitly requests it. Avoid using emojis in all communication unless asked.\n- Your output will be displayed on a command line interface. Your responses should be short and concise. You can use Github-flavored markdown for formatting, and will be rendered in a monospace font using the CommonMark specification.\n- Output text to communicate with the user; all text you output outside of tool use is displayed to the user. Only use tools to complete tasks. Never use tools like Bash or code comments as means to communicate with the user during the session.\n- NEVER create files unless they're absolutely necessary for achieving your goal. ALWAYS prefer editing an existing file to creating a new one. This includes markdown files.\n- Do not use a colon before tool calls. Your tool calls may not be shown directly in the output, so text like \"Let me read the file:\" followed by a read tool call should just be \"Let me read the file.\" with a period.\n\n# Professional objectivity\nPrioritize technical accuracy and truthfulness over validating the user's beliefs. Focus on facts and problem-solving, providing direct, objective technical info without any unnecessary superlatives, praise, or emotional validation. It is best for the user if Claude honestly applies the same rigorous standards to all ideas and disagrees when necessary, even if it may not be what the user wants to hear. Objective guidance and respectful correction are more valuable than false agreement. Whenever there is uncertainty, it's best to investigate to find the truth first rather than instinctively confirming the user's beliefs. Avoid using over-the-top validation or excessive praise when responding to users such as \"You're absolutely right\" or similar phrases.\n\n# No time estimates\nNever give time estimates or predictions for how long tasks will take, whether for your own work or for users planning their projects. Avoid phrases like \"this will take me a few minutes,\" \"should be done in about 5 minutes,\" \"this is a quick fix,\" \"this will take 2-3 weeks,\" or \"we can do this later.\" Focus on what needs to be done, not how long it might take. Break work into actionable steps and let users judge timing for themselves.\n\n# Asking questions as you work\n\nYou have access to the AskUserQuestion tool to ask the user questions when you need clarification, want to validate assumptions, or need to make a decision you're unsure about. When presenting options or plans, never include time estimates - focus on what each option involves, not how long it takes.\n\nUsers may configure 'hooks', shell commands that execute in response to events like tool calls, in settings. Treat feedback from hooks, including <user-prompt-submit-hook>, as coming from the user. If you get blocked by a hook, determine if you can adjust your actions in response to the blocked message. If not, ask the user to check their hooks configuration.\n\n# Doing tasks\nThe user will primarily request you perform software engineering tasks. This includes solving bugs, adding new functionality, refactoring code, explaining code, and more. For these tasks the following steps are recommended:\n- NEVER propose changes to code you haven't read. If a user asks about or wants you to modify a file, read it first. Understand existing code before suggesting modifications.\n- Use the AskUserQuestion tool to ask questions, clarify and gather information as needed.\n- Be careful not to introduce security vulnerabilities such as command injection, XSS, SQL injection, and other OWASP top 10 vulnerabilities. If you notice that you wrote insecure code, immediately fix it.\n- Avoid over-engineering. Only make changes that are directly requested or clearly necessary. Keep solutions simple and focused.\n  - Don't add features, refactor code, or make \"improvements\" beyond what was asked. A bug fix doesn't need surrounding code cleaned up. A simple feature doesn't need extra configurability. Don't add docstrings, comments, or type annotations to code you didn't change. Only add comments where the logic isn't self-evident.\n  - Don't add error handling, fallbacks, or validation for scenarios that can't happen. Trust internal code and framework guarantees. Only validate at system boundaries (user input, external APIs). Don't use feature flags or backwards-compatibility shims when you can just change the code.\n  - Don't create helpers, utilities, or abstractions for one-time operations. Don't design for hypothetical future requirements. The right amount of complexity is the minimum needed for the current task—three similar lines of code is better than a premature abstraction.\n- Avoid backwards-compatibility hacks like renaming unused `_vars`, re-exporting types, adding `// removed` comments for removed code, etc. If something is unused, delete it completely.\n\n- Tool results and user messages may include <system-reminder> tags. <system-reminder> tags contain useful information and reminders. They are automatically added by the system, and bear no direct relation to the specific tool results or user messages in which they appear.\n- The conversation has unlimited context through automatic summarization.\n\n# Tool usage policy\n- When doing file search, prefer to use the Task tool in order to reduce context usage.\n- You should proactively use the Task tool with specialized agents when the task at hand matches the agent's description.\n- /<skill-name> (e.g., /commit) is shorthand for users to invoke a user-invocable skill. When executed, the skill gets expanded to a full prompt. Use the Skill tool to execute them. IMPORTANT: Only use Skill for skills listed in its user-invocable skills section - do not guess or use built-in CLI commands.\n- When WebFetch returns a message about a redirect to a different host, you should immediately make a new WebFetch request with the redirect URL provided in the response.\n- You can call multiple tools in a single response. If you intend to call multiple tools and there are no dependencies between them, make all independent tool calls in parallel. Maximize use of parallel tool calls where possible to increase efficiency. However, if some tool calls depend on previous calls to inform dependent values, do NOT call these tools in parallel and instead call them sequentially. For instance, if one operation must complete before another starts, run these operations sequentially instead. Never use placeholders or guess missing parameters in tool calls.\n- If the user specifies that they want you to run tools \"in parallel\", you MUST send a single message with multiple tool use content blocks. For example, if you need to launch multiple agents in parallel, send a single message with multiple Task tool calls.\n- Use specialized tools instead of bash commands when possible, as this provides a better user experience. For file operations, use dedicated tools: Read for reading files instead of cat/head/tail, Edit for editing instead of sed/awk, and Write for creating files instead of cat with heredoc or echo redirection. Reserve bash tools exclusively for actual system commands and terminal operations that require shell execution. NEVER use bash echo or other command-line tools to communicate thoughts, explanations, or instructions to the user. Output all communication directly in your response text instead.\n- For broader codebase exploration and deep research, use the Task tool with subagent_type=Explore. This is slower than calling Glob or Grep directly so use this only when a simple, directed search proves to be insufficient or when your task will clearly require more than 3 queries.\n<example>\nuser: Where are errors from the client handled?\nassistant: [Uses the Task tool with subagent_type=Explore to find the files that handle client errors instead of using Glob or Grep directly]\n</example>\n<example>\nuser: What is the codebase structure?\nassistant: [Uses the Task tool with subagent_type=Explore]\n</example>\n\nIMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.\n\n# Code References\n\nWhen referencing specific functions or pieces of code include the pattern `file_path:line_number` to allow the user to easily navigate to the source code location.\n\n<example>\nuser: Where are errors from the client handled?\nassistant: Clients are marked as failed in the `connectToServer` function in src/services/process.ts:712.\n</example>\n\nHere is useful information about the environment you are running in:\n<env>\nWorking directory: /Users/wyf\nIs directory a git repo: No\nPlatform: darwin\nShell: zsh\nOS Version: Darwin 24.5.0\n</env>\nYou are powered by the model named Sonnet 4.6 (with 1M context). The exact model ID is claude-sonnet-4-6[1m].\n\nAssistant knowledge cutoff is August 2025.\n\n<claude_background_info>\nThe most recent frontier Claude model is Claude Opus 4.6 (model ID: 'claude-opus-4-6').\n</claude_background_info>\n\n<fast_mode_info>\nFast mode for Claude Code uses the same Claude Opus 4.6 model with faster output. It does NOT switch to a different model. It can be toggled with /fast.\n</fast_mode_info>"
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
	apiKey := info.ApiKey
	if m, ok := info.ChannelSetting["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
		token, _, err := service.ClaudeGetAccessToken(c.Request.Context(), info.ChannelId, info.ApiKey, info.ProxyURL)
		if err != nil {
			return err
		}
		apiKey = token
	}

	// 鉴权头禁止透传入站 token，避免将本地网关 token 误发给上游导致 invalid token。
	req.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Set("x-api-key", apiKey)

	// Claude Code 特有请求头：优先透传客户端头；未透传时使用硬编码默认值
	defaultAccept := "application/json"
	if info != nil && info.IsStream {
		defaultAccept = "text/event-stream"
	}
	req.Set("User-Agent", defaultClaudeCLIUserAgent)
	req.Set("Accept", pickHeaderOrDefault(c, []string{"accept", "Accept"}, defaultAccept))
	req.Set("X-Stainless-Retry-Count", pickHeaderOrDefault(c, []string{"x-stainless-retry-count", "X-Stainless-Retry-Count"}, "0"))
	req.Set("X-Stainless-Timeout", pickHeaderOrDefault(c, []string{"x-stainless-timeout", "X-Stainless-Timeout"}, defaultClaudeStainlessTimeout))
	req.Set("X-Stainless-Lang", pickHeaderOrDefault(c, []string{"x-stainless-lang", "X-Stainless-Lang"}, defaultClaudeStainlessLang))
	req.Set("X-Stainless-Package-Version", pickHeaderOrDefault(c, []string{"x-stainless-package-version", "X-Stainless-Package-Version"}, defaultClaudeStainlessPackageVersion))
	req.Set("X-Stainless-OS", pickHeaderOrDefault(c, []string{"x-stainless-os", "X-Stainless-OS"}, defaultClaudeStainlessOS))
	req.Set("X-Stainless-Arch", pickHeaderOrDefault(c, []string{"x-stainless-arch", "X-Stainless-Arch"}, defaultClaudeStainlessArch))
	req.Set("X-Stainless-Runtime", pickHeaderOrDefault(c, []string{"x-stainless-runtime", "X-Stainless-Runtime"}, defaultClaudeStainlessRuntime))
	req.Set("X-Stainless-Runtime-Version", pickHeaderOrDefault(c, []string{"x-stainless-runtime-version", "X-Stainless-Runtime-Version"}, defaultClaudeStainlessRuntimeVersion))
	req.Set("x-app", pickHeaderOrDefault(c, []string{"x-app", "X-App"}, "cli"))
	req.Set("accept-language", pickHeaderOrDefault(c, []string{"accept-language", "Accept-Language"}, "*"))
	req.Set("sec-fetch-mode", pickHeaderOrDefault(c, []string{"sec-fetch-mode", "Sec-Fetch-Mode"}, "cors"))
	req.Set("anthropic-dangerous-direct-browser-access", pickHeaderOrDefault(c, []string{"anthropic-dangerous-direct-browser-access"}, "true"))
	req.Set("anthropic-version", pickHeaderOrDefault(c, []string{"anthropic-version", "Anthropic-Version"}, defaultClaudeAnthropicVersion))

	// interleaved-thinking 在 OpenAI 兼容模式会触发 signature 校验问题，保持原有保护逻辑。
	beta := defaultClaudeAnthropicBeta
	if c.GetBool("claude_disable_interleaved_thinking") || info == nil || info.RelayMode != relayconstant.RelayModeClaudeMessages {
		beta = strings.ReplaceAll(beta, ",interleaved-thinking-2025-05-14", "")
	}
	req.Set("anthropic-beta", pickHeaderOrDefault(c, []string{"anthropic-beta", "Anthropic-Beta"}, beta))

	return nil
}

func pickHeaderOrDefault(c *gin.Context, names []string, def string) string {
	if c != nil && c.Request != nil {
		for _, n := range names {
			if v := strings.TrimSpace(c.Request.Header.Get(n)); v != "" {
				return v
			}
		}
	}
	return def
}

func buildFixedClaudeCodeSystem() []any {
	return []any{
		map[string]any{
			"type": "text",
			"text": fixedClaudeCodeSystemBilling,
		},
		map[string]any{
			"type": "text",
			"text": fixedClaudeCodeSystemIdentity,
			"cache_control": map[string]any{
				"type": "ephemeral",
			},
		},
		map[string]any{
			"type": "text",
			"text": fixedClaudeCodeSystemPrompt,
			"cache_control": map[string]any{
				"type": "ephemeral",
			},
		},
	}
}

func hasOfficialCLIIdentitySystem(systemItems []any) bool {
	for _, item := range systemItems {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		if strings.Contains(text, fixedClaudeCodeSystemIdentity) {
			return true
		}
	}
	return false
}

func enforceClaudeCodeSystemPolicy(body []byte) []byte {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}

	fixedSystem := buildFixedClaudeCodeSystem()
	rawSystem, hasSystem := root["system"]
	if !hasSystem || rawSystem == nil {
		root["system"] = fixedSystem
		out, err := json.Marshal(root)
		if err != nil {
			return body
		}
		return out
	}

	userSystem, ok := rawSystem.([]any)
	if !ok {
		userSystem = []any{rawSystem}
	}
	if hasOfficialCLIIdentitySystem(userSystem) {
		return body
	}

	merged := make([]any, 0, len(fixedSystem)+len(userSystem))
	merged = append(merged, fixedSystem...)
	merged = append(merged, userSystem...)
	root["system"] = merged

	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

func generateClaudeMetadataUserID(info *relaycommon.RelayInfo) string {
	// 未传时按示例格式生成：
	// user_<64hex>_account__session_<uuid>
	seed := ""
	if info != nil {
		seed = info.ApiKey
	}
	hash := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("user_%x_account__session_%s", hash, uuid.New().String())
}

func enforceClaudeMetadataUserIDPolicy(body []byte, info *relaycommon.RelayInfo) []byte {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}

	metadata := map[string]any{}
	if raw, ok := root["metadata"]; ok {
		if m, ok := raw.(map[string]any); ok && m != nil {
			metadata = m
		}
	}

	// 如果请求体 metadata 里已有 user_id，则直接透传。
	if uid, ok := metadata["user_id"].(string); ok && strings.TrimSpace(uid) != "" {
		root["metadata"] = metadata
		out, err := json.Marshal(root)
		if err != nil {
			return body
		}
		return out
	}

	// 否则自动生成 user_id。
	metadata["user_id"] = generateClaudeMetadataUserID(info)
	root["metadata"] = metadata

	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

func stripThinkingBlocks(body []byte) []byte {
	// 仅用于失败重试：移除 thinking/redacted_thinking block，避免 signature 校验导致 400。
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	delete(root, "thinking")

	msgs, ok := root["messages"].([]any)
	if !ok {
		out, err := json.Marshal(root)
		if err != nil {
			return body
		}
		return out
	}
	for i := range msgs {
		m, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := m["content"].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(blocks))
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				filtered = append(filtered, b)
				continue
			}
			t, _ := bm["type"].(string)
			if t == "thinking" || t == "redacted_thinking" {
				continue
			}
			filtered = append(filtered, b)
		}
		m["content"] = filtered
		msgs[i] = m
	}
	root["messages"] = msgs

	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	if a.RequestMode == RequestModeCompletion {
		return RequestOpenAI2ClaudeComplete(*request), nil
	} else {
		return RequestOpenAI2ClaudeMessage(*request, info)
	}
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func isThinkingSignatureError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "thinking signature has expired or is invalid") ||
		strings.Contains(m, "invalid `signature` in `thinking` block") ||
		strings.Contains(m, "invalid signature in thinking block")
}

func shouldLogThirdPartyUpstream(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	base := strings.ToLower(strings.TrimSpace(info.BaseUrl))
	if base == "" {
		return false
	}
	if strings.Contains(base, "claude.ai") || strings.Contains(base, "anthropic.com") {
		return false
	}
	return true
}

func logUpstreamRequestJSON(req *http.Request, bodyBytes []byte) {
	if req == nil {
		return
	}
	headers := make(map[string]any, len(req.Header))
	for k, v := range req.Header {
		if len(v) == 1 {
			headers[k] = v[0]
		} else {
			headers[k] = v
		}
	}
	if hb, err := json.MarshalIndent(headers, "", "  "); err == nil {
		common.SysLog(fmt.Sprintf("[ClaudeCode] upstream request headers: %s", string(hb)))
	} else {
		common.SysError(fmt.Sprintf("[ClaudeCode] marshal upstream headers failed: %v", err))
	}

	var bodyObj any
	if err := json.Unmarshal(bodyBytes, &bodyObj); err == nil {
		if bb, err := json.MarshalIndent(bodyObj, "", "  "); err == nil {
			common.SysLog(fmt.Sprintf("[ClaudeCode] upstream request body: %s", string(bb)))
			return
		}
	}
	common.SysLog(fmt.Sprintf("[ClaudeCode] upstream request body(raw): %s", string(bodyBytes)))
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	// 读取请求体，便于重试时重复发送（Claude Code 偶发返回 thinking signature 相关 400）。
	bodyBytes, err := io.ReadAll(requestBody)
	if err != nil {
		fmt.Printf("[ClaudeCode] Error reading request body: %v\n", err)
		return nil, err
	}
	bodyBytes = enforceClaudeCodeSystemPolicy(bodyBytes)
	bodyBytes = enforceClaudeMetadataUserIDPolicy(bodyBytes, info)

	doOnce := func() (*http.Response, error) {
		fullRequestURL, err := a.GetRequestURL(info)
		if err != nil {
			return nil, fmt.Errorf("get request url failed: %w", err)
		}
		req, err := http.NewRequest(c.Request.Method, fullRequestURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("new request failed: %w", err)
		}
		req = req.WithContext(c.Request.Context())
		if err = a.SetupRequestHeader(c, &req.Header, info); err != nil {
			return nil, fmt.Errorf("setup request header failed: %w", err)
		}
		if shouldLogThirdPartyUpstream(info) {
			logUpstreamRequestJSON(req, bodyBytes)
		}

		var client *http.Client
		proxyURL := ""
		if info != nil {
			proxyURL = info.ProxyURL
		}
		if info != nil && info.IsStream {
			if proxyURL != "" {
				client = service.GetStreamingHttpClientWithProxy(proxyURL)
			} else {
				client = service.GetStreamingHttpClient()
			}
		} else {
			if proxyURL != "" {
				client = service.GetHttpClientWithProxy(proxyURL)
			} else {
				client = service.GetHttpClient()
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("do request failed: %w", err)
		}
		if resp == nil {
			return nil, errors.New("resp is nil")
		}
		_ = req.Body.Close()
		_ = c.Request.Body.Close()
		return resp, nil
	}

	resp, err := doOnce()
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.StatusCode == http.StatusBadRequest {
		// 读取 body 判断是否为可重试的 thinking signature 错误；读完后恢复 body，避免影响后续错误处理逻辑。
		b, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		if readErr == nil {
			msg := string(b)
			if isThinkingSignatureError(msg) {
				// 该错误可能是上游偶发，也可能是请求里携带了过期 signature。
				// 重试时禁用 interleaved-thinking，并移除 thinking block，尽量做到“自动开新会话”效果。
				time.Sleep(800 * time.Millisecond)
				c.Set("claude_disable_interleaved_thinking", true)
				retryBody := bodyBytes
				if info != nil && info.RelayMode == relayconstant.RelayModeClaudeMessages {
					retryBody = stripThinkingBlocks(bodyBytes)
				}
				resp2, err2 := channel.DoApiRequest(a, c, info, bytes.NewReader(retryBody))
				if err2 == nil && resp2 != nil {
					return resp2, nil
				}
				// 重试失败则返回首次响应，保留原始错误信息。
			}
		}
	}
	return resp, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	// 检查响应类型，如果是 text/event-stream，强制使用流式处理
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		err, usage = ClaudeStreamHandler(c, resp, info, a.RequestMode)
	} else if info.IsStream {
		err, usage = ClaudeStreamHandler(c, resp, info, a.RequestMode)
	} else {
		err, usage = ClaudeHandler(c, resp, a.RequestMode, info)
	}

	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
