package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"strconv"
	"strings"
)

func MidjourneyErrorWrapper(code int, desc string) *dto.MidjourneyResponse {
	return &dto.MidjourneyResponse{
		Code:        code,
		Description: desc,
	}
}

func MidjourneyErrorWithStatusCodeWrapper(code int, desc string, statusCode int) *dto.MidjourneyResponseWithStatusCode {
	return &dto.MidjourneyResponseWithStatusCode{
		StatusCode: statusCode,
		Response:   *MidjourneyErrorWrapper(code, desc),
	}
}

// OpenAIErrorWrapper wraps an error into an OpenAIErrorWithStatusCode
func OpenAIErrorWrapper(err error, code string, statusCode int) *dto.OpenAIErrorWithStatusCode {
	// 客户端主动取消/断开（SSE 停止、浏览器中断等）属于正常行为：
	// - 不应记录为“请求上游失败”，避免刷屏
	// - 不应触发重试/自动禁用渠道
	// - 避免把上游地址等细节暴露到日志/返回体
	if common.IsClientDisconnectError(err) {
		openAIError := dto.OpenAIError{
			Message: "context canceled",
			Type:    "new_api_error",
			Code:    code,
		}
		return &dto.OpenAIErrorWithStatusCode{
			Error:      openAIError,
			StatusCode: 499, // Client Closed Request（常见约定码，Go 标准库无常量）
			LocalError: true,
		}
	}

	text := err.Error()
	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
		common.SysLog(fmt.Sprintf("error: %s", text))
		text = "请求上游地址失败"
	}
	openAIError := dto.OpenAIError{
		Message: text,
		Type:    "new_api_error",
		Code:    code,
	}
	return &dto.OpenAIErrorWithStatusCode{
		Error:      openAIError,
		StatusCode: statusCode,
	}
}

func OpenAIErrorWrapperLocal(err error, code string, statusCode int) *dto.OpenAIErrorWithStatusCode {
	openaiErr := OpenAIErrorWrapper(err, code, statusCode)
	openaiErr.LocalError = true
	return openaiErr
}

func RelayErrorHandler(resp *http.Response) (errWithStatusCode *dto.OpenAIErrorWithStatusCode) {
	errWithStatusCode = &dto.OpenAIErrorWithStatusCode{
		StatusCode: resp.StatusCode,
		Error: dto.OpenAIError{
			Type:  "upstream_error",
			Code:  "bad_response_status_code",
			Param: strconv.Itoa(resp.StatusCode),
		},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	err = resp.Body.Close()
	if err != nil {
		return
	}
	var errResponse dto.GeneralErrorResponse
	err = json.Unmarshal(responseBody, &errResponse)
	if err == nil {
		if errResponse.Error.Message != "" {
			// OpenAI format error, so we override the default one
			errWithStatusCode.Error = errResponse.Error
		} else {
			errWithStatusCode.Error.Message = errResponse.ToMessage()
		}
	}

	// fallback：无法解析标准 error 时，尽量把上游原始 body 带出来，方便定位（截断避免过长）
	if strings.TrimSpace(errWithStatusCode.Error.Message) == "" {
		msg := strings.TrimSpace(string(responseBody))
		if msg != "" {
			if len(msg) > 500 {
				msg = msg[:500]
			}
			errWithStatusCode.Error.Message = msg
		}
	}
	if strings.TrimSpace(errWithStatusCode.Error.Message) == "" {
		errWithStatusCode.Error.Message = fmt.Sprintf("bad response status code %d", resp.StatusCode)
	}
	return
}

func ResetStatusCode(openaiErr *dto.OpenAIErrorWithStatusCode, statusCodeMappingStr string) {
	if statusCodeMappingStr == "" || statusCodeMappingStr == "{}" {
		return
	}
	statusCodeMapping := make(map[string]string)
	err := json.Unmarshal([]byte(statusCodeMappingStr), &statusCodeMapping)
	if err != nil {
		return
	}
	if openaiErr.StatusCode == http.StatusOK {
		return
	}
	codeStr := strconv.Itoa(openaiErr.StatusCode)
	if _, ok := statusCodeMapping[codeStr]; ok {
		intCode, _ := strconv.Atoi(statusCodeMapping[codeStr])
		openaiErr.StatusCode = intCode
	}
}

func TaskErrorWrapperLocal(err error, code string, statusCode int) *dto.TaskError {
	openaiErr := TaskErrorWrapper(err, code, statusCode)
	openaiErr.LocalError = true
	return openaiErr
}

func TaskErrorWrapper(err error, code string, statusCode int) *dto.TaskError {
	// 客户端取消/断开不应当作上游失败，也不应暴露内部细节
	if common.IsClientDisconnectError(err) {
		return &dto.TaskError{
			Code:       code,
			Message:    "context canceled",
			StatusCode: 499,
			Error:      err,
			LocalError: true,
		}
	}

	text := err.Error()
	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "post") || strings.Contains(lowerText, "dial") || strings.Contains(lowerText, "http") {
		common.SysLog(fmt.Sprintf("error: %s", text))
		text = "请求上游地址失败"
	}
	//避免暴露内部错误
	taskError := &dto.TaskError{
		Code:       code,
		Message:    text,
		StatusCode: statusCode,
		Error:      err,
	}

	return taskError
}
