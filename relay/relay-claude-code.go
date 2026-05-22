package relay

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"one-api/setting"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	claudeCodeSystemCLIKeyword       = "You are Claude Code, Anthropic's official CLI for Claude."
	claudeCodeSystemBillingHeader    = "x-anthropic-billing-header: cc_version=2.1.49.7ea; cc_entrypoint=cli; cch=00000;"
	claudeCodeSystemInstructionBlock = "\nYou are an interactive CLI tool that helps users with software engineering tasks. Use the instructions below and the tools available to you to assist the user.\n\nIMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.\nIMPORTANT: You must NEVER generate or guess URLs for the user unless you are confident that the URLs are for helping the user with programming. You may use URLs provided by the user in their messages or local files.\n\nIf the user asks for help or wants to give feedback inform them of the following:\n- /help: Get help with using Claude Code\n- To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues\n\n# Tone and style\n- Only use emojis if the user explicitly requests it. Avoid using emojis in all communication unless asked.\n- Your output will be displayed on a command line interface. Your responses should be short and concise. You can use Github-flavored markdown for formatting, and will be rendered in a monospace font using the CommonMark specification.\n- Output text to communicate with the user; all text you output outside of tool use is displayed to the user. Only use tools to complete tasks. Never use tools like Bash or code comments as means to communicate with the user during the session.\n- NEVER create files unless they're absolutely necessary for achieving your goal. ALWAYS prefer editing an existing file to creating a new one. This includes markdown files.\n- Do not use a colon before tool calls. Your tool calls may not be shown directly in the output, so text like \"Let me read the file:\" followed by a read tool call should just be \"Let me read the file.\" with a period.\n\n# Professional objectivity\nPrioritize technical accuracy and truthfulness over validating the user's beliefs. Focus on facts and problem-solving, providing direct, objective technical info without any unnecessary superlatives, praise, or emotional validation. It is best for the user if Claude honestly applies the same rigorous standards to all ideas and disagrees when necessary, even if it may not be what the user wants to hear. Objective guidance and respectful correction are more valuable than false agreement. Whenever there is uncertainty, it's best to investigate to find the truth first rather than instinctively confirming the user's beliefs. Avoid using over-the-top validation or excessive praise when responding to users such as \"You're absolutely right\" or similar phrases.\n\n# No time estimates\nNever give time estimates or predictions for how long tasks will take, whether for your own work or for users planning their projects. Avoid phrases like \"this will take me a few minutes,\" \"should be done in about 5 minutes,\" \"this is a quick fix,\" \"this will take 2-3 weeks,\" or \"we can do this later.\" Focus on what needs to be done, not how long it might take. Break work into actionable steps and let users judge timing for themselves.\n\n# Asking questions as you work\n\nYou have access to the AskUserQuestion tool to ask the user questions when you need clarification, want to validate assumptions, or need to make a decision you're unsure about. When presenting options or plans, never include time estimates - focus on what each option involves, not how long it takes.\n\nUsers may configure 'hooks', shell commands that execute in response to events like tool calls, in settings. Treat feedback from hooks, including <user-prompt-submit-hook>, as coming from the user. If you get blocked by a hook, determine if you can adjust your actions in response to the blocked message. If not, ask the user to check their hooks configuration.\n\n# Doing tasks\nThe user will primarily request you perform software engineering tasks. This includes solving bugs, adding new functionality, refactoring code, explaining code, and more. For these tasks the following steps are recommended:\n- NEVER propose changes to code you haven't read. If a user asks about or wants you to modify a file, read it first. Understand existing code before suggesting modifications.\n- Use the AskUserQuestion tool to ask questions, clarify and gather information as needed.\n- Be careful not to introduce security vulnerabilities such as command injection, XSS, SQL injection, and other OWASP top 10 vulnerabilities. If you notice that you wrote insecure code, immediately fix it.\n- Avoid over-engineering. Only make changes that are directly requested or clearly necessary. Keep solutions simple and focused.\n  - Don't add features, refactor code, or make \"improvements\" beyond what was asked. A bug fix doesn't need surrounding code cleaned up. A simple feature doesn't need extra configurability. Don't add docstrings, comments, or type annotations to code you didn't change. Only add comments where the logic isn't self-evident.\n  - Don't add error handling, fallbacks, or validation for scenarios that can't happen. Trust internal code and framework guarantees. Only validate at system boundaries (user input, external APIs). Don't use feature flags or backwards-compatibility shims when you can just change the code.\n  - Don't create helpers, utilities, or abstractions for one-time operations. Don't design for hypothetical future requirements. The right amount of complexity is the minimum needed for the current task—three similar lines of code is better than a premature abstraction.\n- Avoid backwards-compatibility hacks like renaming unused `_vars`, re-exporting types, adding `// removed` comments for removed code, etc. If something is unused, delete it completely.\n\n- Tool results and user messages may include <system-reminder> tags. <system-reminder> tags contain useful information and reminders. They are automatically added by the system, and bear no direct relation to the specific tool results or user messages in which they appear.\n- The conversation has unlimited context through automatic summarization.\n\n# Tool usage policy\n- When doing file search, prefer to use the Task tool in order to reduce context usage.\n- You should proactively use the Task tool with specialized agents when the task at hand matches the agent's description.\n- /<skill-name> (e.g., /commit) is shorthand for users to invoke a user-invocable skill. When executed, the skill gets expanded to a full prompt. Use the Skill tool to execute them. IMPORTANT: Only use Skill for skills listed in its user-invocable skills section - do not guess or use built-in CLI commands.\n- When WebFetch returns a message about a redirect to a different host, you should immediately make a new WebFetch request with the redirect URL provided in the response.\n- You can call multiple tools in a single response. If you intend to call multiple tools and there are no dependencies between them, make all independent tool calls in parallel. Maximize use of parallel tool calls where possible to increase efficiency. However, if some tool calls depend on previous calls to inform dependent values, do NOT call these tools in parallel and instead call them sequentially. For instance, if one operation must complete before another starts, run these operations sequentially instead. Never use placeholders or guess missing parameters in tool calls.\n- If the user specifies that they want you to run tools \"in parallel\", you MUST send a single message with multiple tool use content blocks. For example, if you need to launch multiple agents in parallel, send a single message with multiple Task tool calls.\n- Use specialized tools instead of bash commands when possible, as this provides a better user experience. For file operations, use dedicated tools: Read for reading files instead of cat/head/tail, Edit for editing instead of sed/awk, and Write for creating files instead of cat with heredoc or echo redirection. Reserve bash tools exclusively for actual system commands and terminal operations that require shell execution. NEVER use bash echo or other command-line tools to communicate thoughts, explanations, or instructions to the user. Output all communication directly in your response text instead.\n- For broader codebase exploration and deep research, use the Task tool with subagent_type=Explore. This is slower than calling Glob or Grep directly so use this only when a simple, directed search proves to be insufficient or when your task will clearly require more than 3 queries.\n<example>\nuser: Where are errors from the client handled?\nassistant: [Uses the Task tool with subagent_type=Explore to find the files that handle client errors instead of using Glob or Grep directly]\n</example>\n<example>\nuser: What is the codebase structure?\nassistant: [Uses the Task tool with subagent_type=Explore]\n</example>\n\nIMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes. Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases.\n\n# Code References\n\nWhen referencing specific functions or pieces of code include the pattern `file_path:line_number` to allow the user to easily navigate to the source code location.\n\n<example>\nuser: Where are errors from the client handled?\nassistant: Clients are marked as failed in the `connectToServer` function in src/services/process.ts:712.\n</example>\n\nHere is useful information about the environment you are running in:\n<env>\nWorking directory: /Users/wyf\nIs directory a git repo: No\nPlatform: darwin\nShell: zsh\nOS Version: Darwin 24.5.0\n</env>\nYou are powered by the model named Sonnet 4.6 (with 1M context). The exact model ID is claude-sonnet-4-6[1m].\n\nAssistant knowledge cutoff is August 2025.\n\n<claude_background_info>\nThe most recent frontier Claude model is Claude Opus 4.6 (model ID: 'claude-opus-4-6').\n</claude_background_info>\n\n<fast_mode_info>\nFast mode for Claude Code uses the same Claude Opus 4.6 model with faster output. It does NOT switch to a different model. It can be toggled with /fast.\n</fast_mode_info>"
)

const claudeCodeDeepSeekV4CompatibilityMode = "deepseek_v4"

var claudeCLIUserAgentRegex = regexp.MustCompile(`(?i)^claude-cli\/[\d.]+(?:[-\w]*)?\s+\(external,\s*(?:cli|claude-[\w-]+|sdk-[\w-]+)\)$`)
var claudeToolUseIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var claudeToolUseInvalidCharRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func buildFixedClaudeCodeSystem() []claudecode.ClaudeContent {
	return buildFixedClaudeCodeSystemWithCacheSlots(2)
}

func marshalClaudeRequestBodyWithModelFirst(bodyMap map[string]json.RawMessage, model string) ([]byte, error) {
	if bodyMap == nil {
		return nil, fmt.Errorf("bodyMap is nil")
	}

	keys := make([]string, 0, len(bodyMap))
	for key, raw := range bodyMap {
		if key == "model" || len(raw) == 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	buf.WriteString(`"model":`)
	buf.WriteString(strconv.Quote(model))
	for _, key := range keys {
		buf.WriteByte(',')
		buf.WriteString(strconv.Quote(key))
		buf.WriteByte(':')
		buf.Write(bodyMap[key])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type ClaudeCodePreparedRequest struct {
	BodyMap            map[string]json.RawMessage
	PatchedSystemRaw   json.RawMessage
	ShouldPatchSystem  bool
	ShouldDeleteSystem bool
}

func (p *ClaudeCodePreparedRequest) Marshal(model string) ([]byte, error) {
	if p == nil || p.BodyMap == nil {
		return nil, fmt.Errorf("prepared Claude Code request is nil")
	}
	p.BodyMap["model"] = []byte(strconv.Quote(model))
	if p.ShouldDeleteSystem {
		delete(p.BodyMap, "system")
	} else if p.ShouldPatchSystem {
		p.BodyMap["system"] = p.PatchedSystemRaw
	}
	return marshalClaudeRequestBodyWithModelFirst(p.BodyMap, model)
}

func PrepareClaudeCodeMessagesRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, jsonData []byte) (*ClaudeCodePreparedRequest, error) {
	if claudeReq == nil {
		return nil, fmt.Errorf("claudeReq is nil")
	}
	var bodyMap map[string]json.RawMessage
	if err := json.Unmarshal(jsonData, &bodyMap); err != nil {
		return nil, err
	}
	if shouldUseDeepSeekV4Compatibility(relayInfo) {
		applyDeepSeekV4ThinkingCompatibility(bodyMap)
	}
	if messagesRaw, ok := bodyMap["messages"]; ok {
		patchedMessages := messagesRaw
		messagesChanged := false
		if !shouldUseDeepSeekV4Compatibility(relayInfo) {
			if normalized, changed := normalizeInvalidThinkingInMessagesRaw(patchedMessages); changed {
				patchedMessages = normalized
				messagesChanged = true
			}
		}
		if normalized, changed := normalizeInvalidToolUseIDInMessagesRaw(patchedMessages); changed {
			patchedMessages = normalized
			messagesChanged = true
		}
		if messagesChanged {
			bodyMap["messages"] = patchedMessages
			_ = json.Unmarshal(patchedMessages, &claudeReq.Messages)
		}
	}
	if patchedMetadata, changed, metadataErr := claudecode.EnsureMetadataUserIDRaw(bodyMap["metadata"], relayInfo.ApiKey); metadataErr != nil {
		common.SysError("ensure Claude Code metadata.user_id failed: " + metadataErr.Error())
	} else if changed {
		bodyMap["metadata"] = patchedMetadata
		_ = json.Unmarshal(patchedMetadata, &claudeReq.Metadata)
	}
	if _, ok := bodyMap["tools"]; !ok && shouldSimulateClaudeCodeCLI(relayInfo.ChannelSetting) {
		if toolsRaw, shouldInject, toolsErr := claudecode.GetEmbeddedCLIToolsRaw(); toolsErr != nil {
			common.SysError("load Claude Code CLI tools failed: " + toolsErr.Error())
		} else if shouldInject {
			bodyMap["tools"] = toolsRaw
		}
	}

	userSystemRaw := bodyMap["system"]
	c.Set("claude_is_official_cli", isOfficialClaudeCLIRequest(c, userSystemRaw, claudeReq.System))
	patchedSystemRaw, shouldPatchSystem, shouldDeleteSystem := applyClaudeCodeSystemRules(c, claudeReq, bodyMap)
	return &ClaudeCodePreparedRequest{
		BodyMap:            bodyMap,
		PatchedSystemRaw:   patchedSystemRaw,
		ShouldPatchSystem:  shouldPatchSystem,
		ShouldDeleteSystem: shouldDeleteSystem,
	}, nil
}

// BuildClaudeCodeNativeTestRequest 构造 Claude Code 原生 /v1/messages 测试请求体。
// 仅用于管理端通道测试，避免 OpenAI 兼容转换造成上游风控误判。
func BuildClaudeCodeNativeTestRequest(model string, stream bool) map[string]any {
	m := strings.TrimSpace(model)
	if m == "" {
		m = "claude-sonnet-4-20250514"
	}
	return map[string]any{
		"model":      m,
		"stream":     stream,
		"max_tokens": 10,
		"system":     buildFixedClaudeCodeSystem(),
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": "hi",
					},
				},
			},
		},
	}
}

func shouldSimulateClaudeCodeCLI(setting map[string]interface{}) bool {
	if setting == nil {
		return false
	}
	raw, ok := setting["simulate_claude_code_cli"]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	case float64:
		return v != 0
	default:
		return false
	}
}

func buildFixedClaudeCodeSystemWithCacheSlots(cacheSlots int) []claudecode.ClaudeContent {
	if cacheSlots < 0 {
		cacheSlots = 0
	}
	system := []claudecode.ClaudeContent{
		{
			Type: "text",
			Text: claudeCodeSystemBillingHeader,
		},
		{
			Type: "text",
			Text: claudeCodeSystemCLIKeyword,
		},
		{
			Type: "text",
			Text: claudeCodeSystemInstructionBlock,
		},
	}
	// 优先把 cache_control 留给更长的主指令块，其次是 Claude Code 身份块。
	if cacheSlots > 0 {
		system[2].CacheControl = &claudecode.CacheControl{Type: "ephemeral"}
		cacheSlots--
	}
	if cacheSlots > 0 {
		system[1].CacheControl = &claudecode.CacheControl{Type: "ephemeral"}
	}
	return system
}

func isEmptySystemRaw(systemRaw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(systemRaw))
	return trimmed == "" || trimmed == "null" || trimmed == "[]"
}

func hasCLIKeywordInSystemRaw(systemRaw json.RawMessage) bool {
	return bytes.Contains(systemRaw, []byte(claudeCodeSystemCLIKeyword))
}

func hasCLIKeywordInSystem(system []claudecode.ClaudeContent) bool {
	for _, item := range system {
		if strings.Contains(item.Text, claudeCodeSystemCLIKeyword) {
			return true
		}
	}
	return false
}

func hasBillingHeaderInSystem(system []claudecode.ClaudeContent) bool {
	for _, item := range system {
		if strings.Contains(item.Text, claudeCodeSystemBillingHeader) {
			return true
		}
	}
	return false
}

func isClaudeBillingHeaderSystemText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "x-anthropic-billing-header:")
}

func filterClaudeBillingHeaderSystem(system []claudecode.ClaudeContent) []claudecode.ClaudeContent {
	if len(system) == 0 {
		return nil
	}
	filtered := make([]claudecode.ClaudeContent, 0, len(system))
	for _, item := range system {
		if isClaudeBillingHeaderSystemText(item.Text) {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func isClaudeBillingHeaderSystemRawItem(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return false
		}
		return isClaudeBillingHeaderSystemText(text)
	case '{':
		var item map[string]any
		if err := json.Unmarshal(trimmed, &item); err != nil {
			return false
		}
		text, _ := item["text"].(string)
		return isClaudeBillingHeaderSystemText(text)
	default:
		return false
	}
}

func filterClaudeBillingHeaderSystemRaw(systemRaw json.RawMessage) (patched json.RawMessage, shouldPatch bool, shouldDelete bool) {
	trimmed := bytes.TrimSpace(systemRaw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false, false
	}
	wasArray := trimmed[0] == '['
	items, err := normalizeSystemRawToList(systemRaw)
	if err != nil {
		return nil, false, false
	}
	filtered := make([]json.RawMessage, 0, len(items))
	changed := false
	for _, item := range items {
		if isClaudeBillingHeaderSystemRawItem(item) {
			changed = true
			continue
		}
		filtered = append(filtered, append(json.RawMessage(nil), item...))
	}
	if !changed {
		return nil, false, false
	}
	if len(filtered) == 0 {
		return nil, false, true
	}
	if !wasArray && len(filtered) == 1 {
		return filtered[0], true, false
	}
	patched, err = json.Marshal(filtered)
	if err != nil {
		return nil, false, false
	}
	return patched, true, false
}

func normalizeSystemRawToList(systemRaw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(systemRaw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	return []json.RawMessage{append(json.RawMessage(nil), trimmed...)}, nil
}

func mergeFixedAndUserSystemRaw(userSystemRaw json.RawMessage, fixedSystem []claudecode.ClaudeContent) (json.RawMessage, error) {
	fixedRaw, err := json.Marshal(fixedSystem)
	if err != nil {
		return nil, err
	}
	var fixedItems []json.RawMessage
	if err = json.Unmarshal(fixedRaw, &fixedItems); err != nil {
		return nil, err
	}
	userItems, err := normalizeSystemRawToList(userSystemRaw)
	if err != nil {
		return nil, err
	}
	merged := append(fixedItems, userItems...)
	return json.Marshal(merged)
}

func countCacheControlBlocks(v any) int {
	switch t := v.(type) {
	case map[string]any:
		total := 0
		if _, ok := t["cache_control"]; ok {
			total++
		}
		for _, sub := range t {
			total += countCacheControlBlocks(sub)
		}
		return total
	case []any:
		total := 0
		for _, sub := range t {
			total += countCacheControlBlocks(sub)
		}
		return total
	default:
		return 0
	}
}

func countCacheControlBlocksInBodyMap(bodyMap map[string]json.RawMessage) int {
	total := 0
	for _, raw := range bodyMap {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		total += countCacheControlBlocks(v)
	}
	return total
}

func isOfficialClaudeCLIRequest(c *gin.Context, systemRaw json.RawMessage, parsedSystem []claudecode.ClaudeContent) bool {
	score := 0
	if c != nil {
		if strings.EqualFold(strings.TrimSpace(c.GetHeader("x-app")), "cli") {
			score += 2
		}
		ua := strings.TrimSpace(c.GetHeader("User-Agent"))
		if claudeCLIUserAgentRegex.MatchString(ua) {
			score += 2
		}
		beta := strings.ToLower(c.GetHeader("anthropic-beta"))
		if strings.Contains(beta, "claude-code-") {
			score++
		}
	}
	if len(systemRaw) > 0 {
		if hasCLIKeywordInSystemRaw(systemRaw) {
			score++
		}
		if bytes.Contains(systemRaw, []byte(claudeCodeSystemBillingHeader)) {
			score++
		}
	}
	if hasCLIKeywordInSystem(parsedSystem) {
		score++
	}
	if hasBillingHeaderInSystem(parsedSystem) {
		score++
	}
	return score >= 2
}

func applyClaudeCodeSystemRules(c *gin.Context, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) (patchedSystemRaw json.RawMessage, shouldPatchSystem bool, shouldDeleteSystem bool) {
	cacheSlots := 2
	if bodyMap != nil {
		cacheSlots = 4 - countCacheControlBlocksInBodyMap(bodyMap)
	}
	fixedSystem := filterClaudeBillingHeaderSystem(buildFixedClaudeCodeSystemWithCacheSlots(cacheSlots))
	claudeReq.System = filterClaudeBillingHeaderSystem(claudeReq.System)

	// bodyMap 解析失败时，至少保证本地敏感词与计费逻辑使用正确的 system。
	if bodyMap == nil {
		if len(claudeReq.System) == 0 {
			claudeReq.System = fixedSystem
			return nil, false, false
		}
		if c.GetBool("claude_is_official_cli") {
			return nil, false, false
		}
		claudeReq.System = append(fixedSystem, claudeReq.System...)
		return nil, false, false
	}

	userSystemRaw, hasSystem := bodyMap["system"]
	if patchedRaw, rawChanged, rawDeleted := filterClaudeBillingHeaderSystemRaw(userSystemRaw); rawDeleted {
		userSystemRaw = nil
		hasSystem = false
		shouldDeleteSystem = true
	} else if rawChanged {
		userSystemRaw = patchedRaw
		shouldPatchSystem = true
	}

	if !hasSystem || isEmptySystemRaw(userSystemRaw) {
		if c.GetBool("claude_is_official_cli") {
			claudeReq.System = nil
			return nil, false, true
		}
		claudeReq.System = fixedSystem
		patched, err := json.Marshal(claudeReq.System)
		if err != nil {
			return nil, false, false
		}
		return patched, true, false
	}

	if c.GetBool("claude_is_official_cli") {
		if shouldPatchSystem {
			return userSystemRaw, true, false
		}
		return nil, false, false
	}

	claudeReq.System = append(fixedSystem, claudeReq.System...)
	mergedRaw, err := mergeFixedAndUserSystemRaw(userSystemRaw, fixedSystem)
	if err == nil {
		return mergedRaw, true, false
	}
	patched, err := json.Marshal(claudeReq.System)
	if err != nil {
		return nil, false, false
	}
	return patched, true, false
}

func ClaudeCodeMessagesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)
	if relayInfo.RelayMode != relayconstant.RelayModeClaudeMessages {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "invalid relay mode")
		return nil
	}
	if relayInfo.ChannelType != common.ChannelTypeClaudeCode {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "当前渠道不支持 /v1/messages")
		return nil
	}

	var claudeReq claudecode.ClaudeRequest
	if err := common.UnmarshalBodyReusable(c, &claudeReq); err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil
	}
	if claudeReq.Model == "" {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil
	}
	if len(claudeReq.Messages) == 0 {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "messages is required")
		return nil
	}
	relayInfo.IsStream = claudeReq.Stream

	// 先解析原始 body：用于 /v1/messages system 规则处理，并保持未知字段透传。
	jsonData, err := common.GetRequestBody(c)
	if err != nil {
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}
	preparedReq, err := PrepareClaudeCodeMessagesRequest(c, relayInfo, &claudeReq, jsonData)
	if err != nil {
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil
	}

	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" && modelMapping != "{}" {
		modelMap := make(map[string]string)
		if err = json.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
			writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
			return nil
		}
		if modelMap[claudeReq.Model] != "" {
			claudeReq.Model = modelMap[claudeReq.Model]
		}
	}
	relayInfo.UpstreamModelName = claudeReq.Model

	openaiMessages := claudeRequestToMessages(&claudeReq)
	if setting.ShouldCheckPromptSensitive() {
		if err := service.CheckSensitiveMessages(openaiMessages); err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return nil
		}
	}

	promptTokens, err := service.CountTokenMessages(relayInfo, openaiMessages, claudeReq.Model, claudeReq.Stream)
	if err != nil {
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}
	relayInfo.PromptTokens = promptTokens

	modelPrice, getModelPriceSuccess := common.GetModelPrice(claudeReq.Model, false)
	groupRatio := setting.GetGroupRatio(relayInfo.Group)
	var preConsumedQuota int
	var ratio float64
	var modelRatio float64
	if !getModelPriceSuccess {
		preConsumedTokens := common.PreConsumedQuota
		if claudeReq.MaxTokens > 0 {
			preConsumedTokens = promptTokens + int(claudeReq.MaxTokens)
		}
		modelRatio = common.GetModelRatio(claudeReq.Model)
		ratio = modelRatio * groupRatio
		preConsumedQuota = int(float64(preConsumedTokens) * ratio)
	} else {
		preConsumedQuota = int(modelPrice * common.QuotaPerUnit * groupRatio)
	}

	preConsumedQuota, userQuota, openaiErr := preConsumeQuota(c, preConsumedQuota, relayInfo)
	if openaiErr != nil {
		writeClaudeMaybeStreamError(c, relayInfo, openaiErr.StatusCode, mapOpenAIErrorTypeToClaude(openaiErr.Error.Type), openaiErr.Error.Message)
		return nil
	}

	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("invalid api type: %d", relayInfo.ApiType))
		return nil
	}
	adaptor.Init(relayInfo)

	// 注意：/v1/messages 请求体可能包含 thinking 等 beta 字段。
	// 转发时保留原始 JSON，仅替换必要字段（model / system）。
	if patched, marshalErr := preparedReq.Marshal(claudeReq.Model); marshalErr == nil {
		jsonData = patched
	}

	resp, err := adaptor.DoRequest(c, relayInfo, bytes.NewBuffer(jsonData))
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		return service.OpenAIErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}

	httpResp := resp.(*http.Response)
	statusCodeMappingStr := c.GetString("status_code_mapping")
	if httpResp.StatusCode != http.StatusOK {
		openaiErr = service.RelayErrorHandler(httpResp)
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		return openaiErr
	}

	var usage *dto.Usage
	var upstreamErr *dto.OpenAIErrorWithStatusCode
	if relayInfo.IsStream {
		usage, upstreamErr, err = streamClaudeCodePassthrough(c, httpResp, relayInfo)
	} else {
		usage, err = nonStreamClaudeCodePassthrough(c, httpResp, relayInfo)
	}
	if upstreamErr != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		if c.Writer.Written() {
			writeClaudeMaybeStreamError(c, relayInfo, upstreamErr.StatusCode, mapOpenAIErrorTypeToClaude(upstreamErr.Error.Type), upstreamErr.Error.Message)
			return nil
		}
		service.ResetStatusCode(upstreamErr, statusCodeMappingStr)
		return upstreamErr
	}
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		// 客户端中断连接（broken pipe / connection reset）不应视为渠道失败，避免误报与误禁用
		if common.IsClientDisconnectError(err) {
			return nil
		}
		if !c.Writer.Written() {
			return service.OpenAIErrorWrapper(err, "upstream_response_failed", http.StatusInternalServerError)
		}
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}

	postConsumeQuota(c, relayInfo, claudeReq.Model, usage, ratio, preConsumedQuota, userQuota, modelRatio, groupRatio, modelPrice, getModelPriceSuccess, "")
	return nil
}

func claudeRequestToMessages(req *claudecode.ClaudeRequest) []dto.Message {
	messages := make([]dto.Message, 0, len(req.Messages)+1)
	if len(req.System) > 0 {
		var sb strings.Builder
		for _, item := range req.System {
			if strings.TrimSpace(item.Text) != "" {
				sb.WriteString(item.Text)
			}
		}
		if sb.Len() > 0 {
			msg := dto.Message{Role: "system"}
			msg.SetStringContent(sb.String())
			messages = append(messages, msg)
		}
	}
	for _, m := range req.Messages {
		content := claudeMessageText(m.Content)
		msg := dto.Message{Role: m.Role}
		if content != "" {
			msg.SetStringContent(content)
		} else {
			msg.SetStringContent("")
		}
		messages = append(messages, msg)
	}
	return messages
}

func claudeMessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok && text != "" {
					sb.WriteString(text)
				}
			}
		}
		return sb.String()
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
	}
	return ""
}

func shouldUseDeepSeekV4Compatibility(relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo == nil {
		return false
	}
	mode, ok := relayInfo.ChannelSetting["compatibility_mode"].(string)
	return ok && strings.EqualFold(strings.TrimSpace(mode), claudeCodeDeepSeekV4CompatibilityMode)
}

func ensureDeepSeekV4ThinkingOptions(bodyMap map[string]json.RawMessage) {
	if bodyMap == nil {
		return
	}
	if !hasClaudeRequestField(bodyMap, "thinking") {
		bodyMap["thinking"] = json.RawMessage(`{"type":"enabled"}`)
	}
	if outputConfig := normalizeDeepSeekV4OutputConfig(bodyMap["output_config"]); outputConfig != nil {
		bodyMap["output_config"] = outputConfig
	}
}

func applyDeepSeekV4ThinkingCompatibility(bodyMap map[string]json.RawMessage) {
	if hasIncompleteDeepSeekV4ThinkingHistory(bodyMap["messages"]) {
		disableDeepSeekV4Thinking(bodyMap)
		return
	}
	ensureDeepSeekV4ThinkingOptions(bodyMap)
}

func hasIncompleteDeepSeekV4ThinkingHistory(raw json.RawMessage) bool {
	var messages []any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return false
	}
	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok || m["role"] != "assistant" {
			continue
		}
		blocks, ok := m["content"].([]any)
		if !ok {
			continue
		}
		hasToolUse := false
		hasThinking := false
		for _, block := range blocks {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			switch b["type"] {
			case "thinking":
				hasThinking = true
			case "tool_use":
				hasToolUse = true
			}
			if hasToolUse && hasThinking {
				break
			}
		}
		if hasToolUse && !hasThinking {
			return true
		}
	}
	return false
}

func disableDeepSeekV4Thinking(bodyMap map[string]json.RawMessage) {
	if bodyMap == nil {
		return
	}
	bodyMap["thinking"] = json.RawMessage(`{"type":"disabled"}`)
	delete(bodyMap, "output_config")
	delete(bodyMap, "context_management")
	if stripped, changed := stripThinkingBlocksFromMessagesRaw(bodyMap["messages"]); changed {
		bodyMap["messages"] = stripped
	}
}

func stripThinkingBlocksFromMessagesRaw(raw json.RawMessage) (json.RawMessage, bool) {
	var messages []any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return raw, false
	}
	changed := false
	for i := range messages {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(blocks))
		for _, block := range blocks {
			b, ok := block.(map[string]any)
			if ok && b["type"] == "thinking" {
				changed = true
				continue
			}
			filtered = append(filtered, block)
		}
		if changed {
			msg["content"] = filtered
			messages[i] = msg
		}
	}
	if !changed {
		return raw, false
	}
	patched, err := json.Marshal(messages)
	if err != nil {
		return raw, false
	}
	return patched, true
}

func normalizeDeepSeekV4OutputConfig(raw json.RawMessage) json.RawMessage {
	effort := "high"
	if hasClaudeRequestRawField(raw) {
		var outputConfig map[string]any
		if err := json.Unmarshal(raw, &outputConfig); err == nil && outputConfig != nil {
			if rawEffort, ok := outputConfig["effort"].(string); ok {
				switch strings.ToLower(strings.TrimSpace(rawEffort)) {
				case "high":
					effort = "high"
				case "max":
					effort = "max"
				}
			}
		}
	}
	return json.RawMessage(`{"effort":"` + effort + `"}`)
}

func hasClaudeRequestField(bodyMap map[string]json.RawMessage, key string) bool {
	raw, ok := bodyMap[key]
	if !ok {
		return false
	}
	return hasClaudeRequestRawField(raw)
}

func hasClaudeRequestRawField(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func isLikelyClaudeThinkingSignature(signature string) bool {
	return strings.HasPrefix(strings.TrimSpace(signature), "ErUB")
}

func extractThinkingText(block map[string]any) string {
	for _, key := range []string{"text", "thinking", "reasoning"} {
		if v, ok := block[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func normalizeInvalidThinkingInMessagesRaw(raw json.RawMessage) (json.RawMessage, bool) {
	var messages []any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return raw, false
	}
	changed := false
	for i := range messages {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		normalized := make([]any, 0, len(blocks))
		for _, block := range blocks {
			bm, ok := block.(map[string]any)
			if !ok {
				normalized = append(normalized, block)
				continue
			}
			typ, _ := bm["type"].(string)
			if typ != "thinking" && typ != "redacted_thinking" {
				normalized = append(normalized, block)
				continue
			}
			signature, _ := bm["signature"].(string)
			if isLikelyClaudeThinkingSignature(signature) {
				normalized = append(normalized, block)
				continue
			}
			if text := extractThinkingText(bm); text != "" {
				normalized = append(normalized, map[string]any{
					"type": "text",
					"text": text,
				})
			}
			changed = true
		}
		msg["content"] = normalized
		messages[i] = msg
	}
	if !changed {
		return raw, false
	}
	patched, err := json.Marshal(messages)
	if err != nil {
		return raw, false
	}
	return patched, true
}

func sanitizeClaudeToolUseID(rawID string, idMap map[string]string) string {
	trimmed := strings.TrimSpace(rawID)
	if cached, ok := idMap[trimmed]; ok {
		return cached
	}

	if trimmed == "" {
		generated := "tool_" + common.GetUUID()
		idMap[trimmed] = generated
		return generated
	}
	if claudeToolUseIDRegex.MatchString(trimmed) {
		idMap[trimmed] = trimmed
		return trimmed
	}

	base := claudeToolUseInvalidCharRegex.ReplaceAllString(trimmed, "_")
	base = strings.Trim(base, "_")
	if base == "" {
		base = "tool"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	sum := sha1.Sum([]byte(trimmed))
	normalized := fmt.Sprintf("%s_%x", base, sum[:4])
	idMap[trimmed] = normalized
	return normalized
}

func patchClaudeToolUseID(v any, idMap map[string]string) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false

		blockType, _ := t["type"].(string)
		switch blockType {
		case "tool_use":
			if id, ok := t["id"].(string); ok {
				if normalized := sanitizeClaudeToolUseID(id, idMap); normalized != id {
					t["id"] = normalized
					changed = true
				}
			}
		case "tool_result":
			if toolUseID, ok := t["tool_use_id"].(string); ok {
				if normalized := sanitizeClaudeToolUseID(toolUseID, idMap); normalized != toolUseID {
					t["tool_use_id"] = normalized
					changed = true
				}
			}
		}

		if toolUseObj, ok := t["tool_use"].(map[string]any); ok {
			if id, ok := toolUseObj["id"].(string); ok {
				if normalized := sanitizeClaudeToolUseID(id, idMap); normalized != id {
					toolUseObj["id"] = normalized
					changed = true
				}
			}
		}

		for _, sub := range t {
			if patchClaudeToolUseID(sub, idMap) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, sub := range t {
			if patchClaudeToolUseID(sub, idMap) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func normalizeInvalidToolUseIDInMessagesRaw(raw json.RawMessage) (json.RawMessage, bool) {
	var messages any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return raw, false
	}
	idMap := make(map[string]string)
	if !patchClaudeToolUseID(messages, idMap) {
		return raw, false
	}
	patched, err := json.Marshal(messages)
	if err != nil {
		return raw, false
	}
	return patched, true
}

func patchMissingThinkingSignature(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		if typ, ok := t["type"].(string); ok && typ == "thinking" {
			signatureRaw, exists := t["signature"]
			if !exists {
				t["signature"] = "skip_thought_signature_validator"
				changed = true
			} else if signature, ok := signatureRaw.(string); ok && strings.TrimSpace(signature) == "" {
				t["signature"] = "skip_thought_signature_validator"
				changed = true
			}
		}
		for _, sub := range t {
			if patchMissingThinkingSignature(sub) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, sub := range t {
			if patchMissingThinkingSignature(sub) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func patchEmptyClaudeCodeReadPages(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		if typ, _ := t["type"].(string); typ == "tool_use" {
			if name, _ := t["name"].(string); name == "Read" {
				if input, ok := t["input"].(map[string]any); ok {
					if pages, ok := input["pages"].(string); ok && pages == "" {
						delete(input, "pages")
						changed = true
					}
				}
			}
		}
		for _, sub := range t {
			if patchEmptyClaudeCodeReadPages(sub) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, sub := range t {
			if patchEmptyClaudeCodeReadPages(sub) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func sanitizeClaudeCodeReadInput(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return raw
	}
	if pages, ok := input["pages"].(string); !ok || pages != "" {
		return raw
	}
	delete(input, "pages")
	patched, err := json.Marshal(input)
	if err != nil {
		return raw
	}
	return string(patched)
}

func patchClaudeCodeResponse(v any) bool {
	changed := patchMissingThinkingSignature(v)
	if patchEmptyClaudeCodeReadPages(v) {
		changed = true
	}
	return changed
}

func patchClaudeCodeResponseJSON(raw []byte) ([]byte, bool) {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return raw, false
	}
	if !patchClaudeCodeResponse(parsed) {
		return raw, false
	}
	patched, err := json.Marshal(parsed)
	if err != nil {
		return raw, false
	}
	return patched, true
}

type claudeCodeStreamPatchState struct {
	readToolInputs map[int]*strings.Builder
}

func newClaudeCodeStreamPatchState() *claudeCodeStreamPatchState {
	return &claudeCodeStreamPatchState{
		readToolInputs: make(map[int]*strings.Builder),
	}
}

func (s *claudeCodeStreamPatchState) patchEvent(root map[string]any) (extraLines []string, suppress bool) {
	if s == nil {
		return nil, false
	}
	eventType, _ := root["type"].(string)
	idx, hasIndex := claudeCodeStreamEventIndex(root["index"])

	switch eventType {
	case "content_block_start":
		if !hasIndex {
			return nil, false
		}
		block, ok := root["content_block"].(map[string]any)
		if !ok {
			return nil, false
		}
		blockType, _ := block["type"].(string)
		name, _ := block["name"].(string)
		if blockType == "tool_use" && name == "Read" {
			s.readToolInputs[idx] = &strings.Builder{}
		}
	case "content_block_delta":
		if !hasIndex {
			return nil, false
		}
		input, ok := s.readToolInputs[idx]
		if !ok {
			return nil, false
		}
		delta, ok := root["delta"].(map[string]any)
		if !ok {
			return nil, false
		}
		deltaType, _ := delta["type"].(string)
		if deltaType != "input_json_delta" {
			return nil, false
		}
		if partialJSON, ok := delta["partial_json"].(string); ok {
			input.WriteString(partialJSON)
		}
		return nil, true
	case "content_block_stop":
		if !hasIndex {
			return nil, false
		}
		input, ok := s.readToolInputs[idx]
		if !ok {
			return nil, false
		}
		delete(s.readToolInputs, idx)
		raw := input.String()
		if raw == "" {
			return nil, false
		}
		event := map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": sanitizeClaudeCodeReadInput(raw),
			},
		}
		data, err := json.Marshal(event)
		if err != nil {
			return nil, false
		}
		return []string{"event: content_block_delta", "data: " + string(data), ""}, false
	}
	return nil, false
}

func patchClaudeCodeStreamEventData(data string, state *claudeCodeStreamPatchState) (string, []string, bool, bool) {
	var parsed any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return data, nil, false, false
	}
	root, _ := parsed.(map[string]any)
	var extraLines []string
	if root != nil && state != nil {
		var suppress bool
		extraLines, suppress = state.patchEvent(root)
		if suppress {
			return data, nil, true, false
		}
	}
	if !patchClaudeCodeResponse(parsed) {
		return data, extraLines, false, false
	}
	patched, err := json.Marshal(parsed)
	if err != nil {
		return data, extraLines, false, false
	}
	return string(patched), extraLines, false, true
}

func claudeCodeStreamEventIndex(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func claudeCodeStreamError(resp *http.Response, claudeResp *claudecode.ClaudeResponse) *dto.OpenAIErrorWithStatusCode {
	if claudeResp == nil {
		return nil
	}
	errType := strings.TrimSpace(claudeResp.Error.Type)
	if errType == "" && strings.EqualFold(strings.TrimSpace(claudeResp.Type), "error") {
		errType = "upstream_error"
	}
	if errType == "" {
		return nil
	}
	message := strings.TrimSpace(claudeResp.Error.Message)
	if message == "" {
		message = "upstream error"
	}
	statusCode := claudeCodeStreamErrorStatusCode(resp, errType)
	return &dto.OpenAIErrorWithStatusCode{
		Error: dto.OpenAIError{
			Message: message,
			Type:    errType,
			Code:    errType,
		},
		StatusCode: statusCode,
		LocalError: false,
	}
}

func claudeCodeStreamErrorStatusCode(resp *http.Response, errType string) int {
	if resp != nil && resp.StatusCode >= http.StatusBadRequest {
		return resp.StatusCode
	}
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "rate_limit_error", "rate_limit_exceeded", "usage_limit_reached", "overloaded_error":
		return http.StatusTooManyRequests
	case "invalid_request_error":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case "permission_error":
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}

func writeClaudeCodeStreamLine(c *gin.Context, line string) error {
	if !c.Writer.Written() {
		service.SetEventStreamHeaders(c)
	}
	if _, err := c.Writer.Write([]byte(line + "\n")); err != nil {
		return err
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func streamClaudeCodePassthrough(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	defer resp.Body.Close()

	usage := &dto.Usage{}
	var responseText strings.Builder
	pendingLines := make([]string, 0, 2)
	streamStarted := false
	streamPatchState := newClaudeCodeStreamPatchState()
	pendingEventLine := ""

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		info.SetFirstResponseTime()
		outputLine := line
		parsedData := ""
		if strings.HasPrefix(line, "event:") {
			if pendingEventLine != "" {
				if !streamStarted {
					if len(pendingLines) < 4 {
						pendingLines = append(pendingLines, pendingEventLine)
					}
				} else if err := writeClaudeCodeStreamLine(c, pendingEventLine); err != nil {
					return nil, nil, err
				}
			}
			pendingEventLine = line
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			parsedData = data
			if data != "" && data != "[DONE]" {
				if patchedData, extraLines, suppress, changed := patchClaudeCodeStreamEventData(data, streamPatchState); suppress {
					pendingEventLine = ""
					continue
				} else {
					for _, extraLine := range extraLines {
						if !streamStarted {
							pendingLines = append(pendingLines, extraLine)
						} else {
							if err := writeClaudeCodeStreamLine(c, extraLine); err != nil {
								return nil, nil, err
							}
						}
					}
					if changed {
						if strings.HasPrefix(line, "data: ") {
							outputLine = "data: " + patchedData
						} else {
							outputLine = "data:" + patchedData
						}
						parsedData = patchedData
					}
				}
			}
		}
		if parsedData == "" || parsedData == "[DONE]" {
			if pendingEventLine != "" {
				if !streamStarted {
					if len(pendingLines) < 4 {
						pendingLines = append(pendingLines, pendingEventLine)
					}
				} else if err := writeClaudeCodeStreamLine(c, pendingEventLine); err != nil {
					return nil, nil, err
				}
				pendingEventLine = ""
			}
			if !streamStarted {
				if strings.TrimSpace(outputLine) != "" && len(pendingLines) < 4 {
					pendingLines = append(pendingLines, outputLine)
				}
				continue
			}
			if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
				return nil, nil, err
			}
			streamStarted = true
			continue
		}
		var claudeResp claudecode.ClaudeResponse
		if err := json.Unmarshal([]byte(parsedData), &claudeResp); err != nil {
			if !streamStarted {
				return nil, nil, errors.New("invalid claude stream event before message_start")
			}
			if pendingEventLine != "" {
				pendingLines = append(pendingLines, pendingEventLine)
				pendingEventLine = ""
			}
			for _, pendingLine := range pendingLines {
				if err := writeClaudeCodeStreamLine(c, pendingLine); err != nil {
					return nil, nil, err
				}
			}
			pendingLines = pendingLines[:0]
			if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
				return nil, nil, err
			}
			streamStarted = true
			continue
		}
		if upstreamErr := claudeCodeStreamError(resp, &claudeResp); upstreamErr != nil {
			if streamStarted {
				if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
					return nil, nil, err
				}
				return nil, nil, fmt.Errorf("%s", upstreamErr.Error.Message)
			}
			return nil, upstreamErr, nil
		}
		if !streamStarted && claudeResp.Type != "message_start" {
			pendingLines = pendingLines[:0]
			pendingEventLine = ""
			continue
		}
		if pendingEventLine != "" {
			pendingLines = append(pendingLines, pendingEventLine)
			pendingEventLine = ""
		}
		for _, pendingLine := range pendingLines {
			if err := writeClaudeCodeStreamLine(c, pendingLine); err != nil {
				return nil, nil, err
			}
		}
		pendingLines = pendingLines[:0]
		if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
			return nil, nil, err
		}
		streamStarted = true
		if claudeResp.Type == "message_start" && claudeResp.Message != nil {
			info.UpstreamModelName = claudeResp.Message.Model
			usage.PromptTokens = claudeResp.Message.Usage.InputTokens
		} else if claudeResp.Type == "content_block_delta" && claudeResp.Delta != nil {
			responseText.WriteString(claudeResp.Delta.Text)
		} else if claudeResp.Type == "message_delta" {
			usage.CompletionTokens = claudeResp.Usage.OutputTokens
			usage.TotalTokens = claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	// 流式响应结束但没有 message_start：保持未写响应状态，让 controller 统一重试。
	if !streamStarted {
		return nil, nil, io.ErrUnexpectedEOF
	}

	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		u, _ := service.ResponseText2Usage(responseText.String(), info.UpstreamModelName, usage.PromptTokens)
		usage.CompletionTokens = u.CompletionTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage, nil, nil
}

func nonStreamClaudeCodePassthrough(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if patched, changed := patchClaudeCodeResponseJSON(body); changed {
		body = patched
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(body); err != nil {
		return nil, err
	}

	var claudeResp claudecode.ClaudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, err
	}
	if claudeResp.Model != "" {
		info.UpstreamModelName = claudeResp.Model
	}
	usage := &dto.Usage{
		PromptTokens:     claudeResp.Usage.InputTokens,
		CompletionTokens: claudeResp.Usage.OutputTokens,
		TotalTokens:      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	return usage, nil
}

func writeClaudeError(c *gin.Context, status int, errType, message string) {
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(status)
	_ = json.NewEncoder(c.Writer).Encode(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	})
}

func writeClaudeMaybeStreamError(c *gin.Context, info *relaycommon.RelayInfo, status int, errType, message string) {
	if c == nil || c.Writer.Written() {
		return
	}
	writeClaudeError(c, status, errType, message)
}

func WriteClaudeMessagesError(c *gin.Context, openaiErr *dto.OpenAIErrorWithStatusCode) {
	if openaiErr == nil {
		return
	}
	info := &relaycommon.RelayInfo{IsStream: isClaudeMessagesStreamRequest(c)}
	writeClaudeMaybeStreamError(c, info, openaiErr.StatusCode, mapOpenAIErrorTypeToClaude(openaiErr.Error.Type), openaiErr.Error.Message)
}

func isClaudeMessagesStreamRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	var req struct {
		Stream bool `json:"stream"`
	}
	body, err := common.GetRequestBody(c)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	return req.Stream
}

func mapOpenAIErrorTypeToClaude(t string) string {
	tt := strings.TrimSpace(t)
	if tt == "" {
		return "api_error"
	}
	switch tt {
	case "invalid_request_error":
		return "invalid_request_error"
	case "authentication_error":
		return "authentication_error"
	case "permission_error":
		return "permission_error"
	case "not_found_error":
		return "not_found_error"
	case "rate_limit_error", "rate_limit_exceeded", "usage_limit_reached":
		return "rate_limit_error"
	case "overloaded_error":
		return "overloaded_error"
	default:
		return "api_error"
	}
}
