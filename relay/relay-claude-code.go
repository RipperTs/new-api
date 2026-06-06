package relay

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"one-api/common"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
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

func MarshalClaudeCodeMessagesRequest(bodyMap map[string]json.RawMessage, model string) ([]byte, error) {
	return marshalClaudeRequestBodyWithModelFirst(bodyMap, model)
}

func PrepareClaudeCodeMessagesRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) error {
	if claudeReq == nil {
		return fmt.Errorf("claudeReq is nil")
	}
	if bodyMap == nil {
		return fmt.Errorf("bodyMap is nil")
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
		}
		_ = json.Unmarshal(patchedMessages, &claudeReq.Messages)
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
	if shouldDeleteSystem {
		delete(bodyMap, "system")
	} else if shouldPatchSystem {
		bodyMap["system"] = patchedSystemRaw
	}
	return nil
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
