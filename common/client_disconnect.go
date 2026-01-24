package common

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
)

// IsClientDisconnectError 判断错误是否由客户端主动断开/取消导致。
// 典型场景：SSE/流式响应过程中，客户端中断连接导致服务端写入失败（broken pipe / connection reset by peer）。
func IsClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.EPIPE) || errors.Is(opErr.Err, syscall.ECONNRESET) {
			return true
		}
		if errors.Is(opErr.Err, context.Canceled) {
			return true
		}
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if errors.Is(sysErr.Err, syscall.EPIPE) || errors.Is(sysErr.Err, syscall.ECONNRESET) {
				return true
			}
		}
	}

	return IsClientDisconnectMessage(err.Error())
}

// IsClientDisconnectMessage 仅通过文本判断（用于无法拿到原始 error 的场景）。
func IsClientDisconnectMessage(msg string) bool {
	m := strings.ToLower(strings.TrimSpace(msg))
	if m == "" {
		return false
	}
	return strings.Contains(m, "broken pipe") ||
		strings.Contains(m, "connection reset by peer") ||
		strings.Contains(m, "client disconnected") ||
		strings.Contains(m, "context canceled")
}

// IsUpstreamTransportFailureMessage 判断是否为网关/代理层的转发失败（通常还未拿到上游响应头），
// 这类问题往往是临时网络/依赖故障，不应触发“自动禁用渠道”。
func IsUpstreamTransportFailureMessage(msg string) bool {
	m := strings.ToLower(strings.TrimSpace(msg))
	if m == "" {
		return false
	}
	return strings.Contains(m, "upstream connect error") ||
		strings.Contains(m, "disconnect/reset before headers") ||
		strings.Contains(m, "remote connection failure") ||
		strings.Contains(m, "transport failure reason") ||
		strings.Contains(m, "delayed connect error") ||
		strings.Contains(m, "connection refused")
}
