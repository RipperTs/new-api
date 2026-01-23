package service

import (
	"fmt"
	"net/http"
	"one-api/common"
	relaymodel "one-api/dto"
	"one-api/model"
	"strings"
)

// disable & notify
func DisableChannel(channelId int, channelName string, reason string) {
	model.UpdateChannelStatusById(channelId, common.ChannelStatusAutoDisabled, reason)
	subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channelName, channelId)
	content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channelName, channelId, reason)
	notifyRootUser(subject, content)
}

func EnableChannel(channelId int, channelName string) {
	model.UpdateChannelStatusById(channelId, common.ChannelStatusEnabled, "")
	subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
	content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
	notifyRootUser(subject, content)
}

// ShouldDisableChannel 根据错误信息判断是否应自动禁用通道
func ShouldDisableChannel(channelType int, err *relaymodel.OpenAIErrorWithStatusCode) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if err.LocalError {
		return false
	}
	// 客户端中断连接导致的写入失败不应作为“渠道异常”
	if common.IsClientDisconnectMessage(err.Error.Message) {
		return false
	}
	// 服务器异常类错误（5xx）直接判定为禁用，避免问题渠道持续被选中
	// 超时（504/524）通常为暂时性网络/边缘问题，保持不禁用，仅交给重试策略
	if err.StatusCode/100 == 5 {
		if err.StatusCode == http.StatusGatewayTimeout || err.StatusCode == 524 {
			return false
		}
		return true
	}
	if err.StatusCode == http.StatusUnauthorized {
		return true
	}
	if err.StatusCode == http.StatusForbidden {
		switch channelType {
		case common.ChannelTypeGemini:
			return true
		case common.ChannelTypeCodex:
			// Codex 403 通常表示账号被封或权限不足,应自动禁用
			return true
		case common.ChannelTypeClaudeCode:
			// Codex 403 通常表示账号被封或权限不足,应自动禁用
			return true
		}
	}
	// 429 通常是限流，可重试但不应直接禁用；但 Codex/Team 等场景可能出现 usage_limit_reached，
	// 该错误通常在一段时间内持续存在（直到 resets_at），适合自动禁用以避免反复被选中。
	if err.StatusCode == http.StatusTooManyRequests {
		// 429 往往会在一段时间内持续出现（账号/会话级限流），启用自动禁用避免反复被选中
		if channelType == common.ChannelTypeClaudeCode {
			return true
		}
		if channelType == common.ChannelTypeCodex {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(err.Error.Type), "usage_limit_reached") {
			return true
		}
		codeStr := strings.TrimSpace(fmt.Sprint(err.Error.Code))
		if strings.EqualFold(codeStr, "usage_limit_reached") {
			return true
		}
	}
	// 402 codex 表示工作空间已被停用
	if err.StatusCode == http.StatusPaymentRequired {
		if channelType == common.ChannelTypeCodex {
			return true
		}
	}
	switch err.Error.Code {
	case "invalid_api_key":
		return true
	case "account_deactivated":
		return true
	case "billing_not_active":
		return true
	case "deactivated_workspace":
		return true
	}
	switch err.Error.Type {
	case "insufficient_quota":
		return true
	case "insufficient_user_quota":
		return true
	// https://docs.anthropic.com/claude/reference/errors
	case "authentication_error":
		return true
	case "permission_error":
		return true
	case "forbidden":
		return true
	}
	if strings.HasPrefix(err.Error.Message, "Your credit balance is too low") { // anthropic
		return true
	} else if strings.HasPrefix(err.Error.Message, "This organization has been disabled.") {
		return true
	} else if strings.HasPrefix(err.Error.Message, "You exceeded your current quota") {
		return true
	} else if strings.HasPrefix(err.Error.Message, "Permission denied") {
		return true
	}

	if strings.Contains(err.Error.Message, "The security token included in the request is invalid") { // anthropic
		return true
	} else if strings.Contains(err.Error.Message, "Operation not allowed") {
		return true
	} else if strings.Contains(err.Error.Message, "Your account is not authorized") {
		return true
	}

	return false
}

func ShouldEnableChannel(err error, openaiWithStatusErr *relaymodel.OpenAIErrorWithStatusCode, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if err != nil {
		return false
	}
	if openaiWithStatusErr != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
