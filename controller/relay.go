package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"one-api/middleware"
	"one-api/model"
	"one-api/relay"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"one-api/setting"
	"strings"
)

func relayHandler(c *gin.Context, relayMode int) *dto.OpenAIErrorWithStatusCode {
	var err *dto.OpenAIErrorWithStatusCode
	switch relayMode {
	case relayconstant.RelayModeImagesGenerations:
		err = relay.ImageHelper(c, relayMode)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, relayMode)
	case relayconstant.RelayModeResponses:
		err = relay.ResponsesHelper(c)
	case relayconstant.RelayModeCodexCLI:
		err = relay.CodexCLIHelper(c)
	case relayconstant.RelayModeClaudeMessages:
		err = relay.ClaudeMessagesHelper(c)
	default:
		err = relay.TextHelper(c)
	}
	return err
}

func wsHandler(c *gin.Context, ws *websocket.Conn, relayMode int) *dto.OpenAIErrorWithStatusCode {
	var err *dto.OpenAIErrorWithStatusCode
	switch relayMode {
	default:
		err = relay.TextHelper(c)
	}
	return err
}

func Playground(c *gin.Context) {
	var openaiErr *dto.OpenAIErrorWithStatusCode

	defer func() {
		if openaiErr != nil {
			c.JSON(openaiErr.StatusCode, gin.H{
				"error": openaiErr.Error,
			})
		}
	}()

	useAccessToken := c.GetBool("use_access_token")
	if useAccessToken {
		openaiErr = service.OpenAIErrorWrapperLocal(errors.New("暂不支持使用 access token"), "access_token_not_supported", http.StatusBadRequest)
		return
	}

	playgroundRequest := &dto.PlayGroundRequest{}
	err := common.UnmarshalBodyReusable(c, playgroundRequest)
	if err != nil {
		openaiErr = service.OpenAIErrorWrapperLocal(err, "unmarshal_request_failed", http.StatusBadRequest)
		return
	}

	if playgroundRequest.Model == "" {
		openaiErr = service.OpenAIErrorWrapperLocal(errors.New("请选择模型"), "model_required", http.StatusBadRequest)
		return
	}
	c.Set("original_model", playgroundRequest.Model)
	group := playgroundRequest.Group
	userGroup := c.GetString("group")

	if group == "" {
		group = userGroup
	} else {
		if !setting.GroupInUserUsableGroups(group) && group != userGroup {
			openaiErr = service.OpenAIErrorWrapperLocal(errors.New("无权访问该分组"), "group_not_allowed", http.StatusForbidden)
			return
		}
		c.Set("group", group)
	}
	c.Set("token_name", "playground-"+group)
	channel, err := model.GetRandomSatisfiedChannel(group, playgroundRequest.Model, 0)
	if err != nil {
		message := fmt.Sprintf("当前分组 %s 下对于模型 %s 无可用渠道", group, playgroundRequest.Model)
		openaiErr = service.OpenAIErrorWrapperLocal(errors.New(message), "get_playground_channel_failed", http.StatusInternalServerError)
		return
	}
	middleware.SetupContextForSelectedChannel(c, channel, playgroundRequest.Model)
	Relay(c)
}

func Relay(c *gin.Context) {
	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)
	requestId := c.GetString(common.RequestIdKey)
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	var openaiErr *dto.OpenAIErrorWithStatusCode

	for i := 0; i <= common.RetryTimes; i++ {
		channel, err := getChannel(c, group, originalModel, i)
		if err != nil {
			common.LogError(c, err.Error())
			openaiErr = service.OpenAIErrorWrapperLocal(err, "get_channel_failed", http.StatusInternalServerError)
			break
		}

		openaiErr = relayRequest(c, relayMode, channel)

		if openaiErr == nil {
			return // 成功处理请求，直接返回
		}

		canRetry := shouldRetry(c, openaiErr, common.RetryTimes-i)
		go processChannelError(requestId, group, channel.Id, channel.Type, channel.Name, originalModel, relayMode, channel.GetAutoBan(), openaiErr, !canRetry)

		if !canRetry {
			break
		}
	}
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		common.LogInfo(c, retryLogStr)
	}

	if openaiErr != nil {
		if relayMode == relayconstant.RelayModeCodexCLI {
			// Codex CLI 的 /responses/compact 为非流式 JSON；错误也按 JSON 返回（避免客户端误判 SSE）。
			if strings.HasSuffix(strings.TrimSpace(c.Request.URL.Path), "/responses/compact") {
				if openaiErr.StatusCode == http.StatusTooManyRequests && !strings.EqualFold(strings.TrimSpace(openaiErr.Error.Type), "usage_limit_reached") {
					openaiErr.Error.Message = "当前分组上游负载已饱和，请稍后再试"
				}
				openaiErr.Error.Message = common.MessageWithRequestId(openaiErr.Error.Message, requestId)
				c.JSON(openaiErr.StatusCode, gin.H{
					"error": openaiErr.Error,
				})
				return
			}

			// Codex CLI 只认 SSE：最终失败时也用 SSE 输出，避免客户端静默中断。
			// 429 场景下保留 usage_limit_reached 等上游信息，其它 429 仍返回通用提示。
			if openaiErr.StatusCode == http.StatusTooManyRequests && !strings.EqualFold(strings.TrimSpace(openaiErr.Error.Type), "usage_limit_reached") {
				openaiErr.Error.Message = "当前分组上游负载已饱和，请稍后再试"
			}
			msg := common.MessageWithRequestId(openaiErr.Error.Message, requestId)
			relay.WriteCodexCLIErrorSSE(c, msg)
			return
		}

		if openaiErr.StatusCode == http.StatusTooManyRequests {
			// 429 场景下保留 usage_limit_reached 等上游信息，其它 429 仍返回通用提示。
			if !strings.EqualFold(strings.TrimSpace(openaiErr.Error.Type), "usage_limit_reached") {
				openaiErr.Error.Message = "当前分组上游负载已饱和，请稍后再试"
			}
		}
		openaiErr.Error.Message = common.MessageWithRequestId(openaiErr.Error.Message, requestId)
		c.JSON(openaiErr.StatusCode, gin.H{
			"error": openaiErr.Error,
		})
	}
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol TODO add other protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func WssRelay(c *gin.Context) {
	// 将 HTTP 连接升级为 WebSocket 连接

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	defer ws.Close()

	if err != nil {
		openaiErr := service.OpenAIErrorWrapper(err, "get_channel_failed", http.StatusInternalServerError)
		service.WssError(c, ws, openaiErr.Error)
		return
	}

	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)
	requestId := c.GetString(common.RequestIdKey)
	group := c.GetString("group")
	//wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-10-01
	originalModel := c.GetString("original_model")
	var openaiErr *dto.OpenAIErrorWithStatusCode

	for i := 0; i <= common.RetryTimes; i++ {
		channel, err := getChannel(c, group, originalModel, i)
		if err != nil {
			common.LogError(c, err.Error())
			openaiErr = service.OpenAIErrorWrapperLocal(err, "get_channel_failed", http.StatusInternalServerError)
			break
		}

		openaiErr = wssRequest(c, ws, relayMode, channel)

		if openaiErr == nil {
			return // 成功处理请求，直接返回
		}

		canRetry := shouldRetry(c, openaiErr, common.RetryTimes-i)
		go processChannelError(requestId, group, channel.Id, channel.Type, channel.Name, originalModel, relayMode, channel.GetAutoBan(), openaiErr, !canRetry)

		if !canRetry {
			break
		}
	}
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		common.LogInfo(c, retryLogStr)
	}

	if openaiErr != nil {
		if openaiErr.StatusCode == http.StatusTooManyRequests {
			openaiErr.Error.Message = "当前分组上游负载已饱和，请稍后再试"
		}
		openaiErr.Error.Message = common.MessageWithRequestId(openaiErr.Error.Message, requestId)
		service.WssError(c, ws, openaiErr.Error)
	}
}

func relayRequest(c *gin.Context, relayMode int, channel *model.Channel) *dto.OpenAIErrorWithStatusCode {
	addUsedChannel(c, channel.Id)
	requestBody, _ := common.GetRequestBody(c)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return relayHandler(c, relayMode)
}

func wssRequest(c *gin.Context, ws *websocket.Conn, relayMode int, channel *model.Channel) *dto.OpenAIErrorWithStatusCode {
	addUsedChannel(c, channel.Id)
	requestBody, _ := common.GetRequestBody(c)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return relay.WssHelper(c, ws)
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

func getChannel(c *gin.Context, group, originalModel string, retryCount int) (*model.Channel, error) {
	if retryCount == 0 {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		return &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}, nil
	}
	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)
	var channel *model.Channel
	var err error
	if relayMode == relayconstant.RelayModeResponses {
		channel, err = model.GetRandomSatisfiedChannelByTypes(group, originalModel, retryCount, []int{common.ChannelTypeOpenAI})
	} else if relayMode == relayconstant.RelayModeClaudeMessages {
		// /v1/messages 兼容模式仅在 Claude Code/OpenRouter 渠道内重试，避免误选不兼容渠道。
		channel, err = model.GetRandomSatisfiedChannelByTypes(group, originalModel, retryCount, []int{common.ChannelTypeClaudeCode, common.ChannelTypeOpenRouter})
	} else if relayMode == relayconstant.RelayModeCodexCLI {
		// Codex CLI 只支持 Codex 渠道：重试时也只从 Codex 渠道中选择，避免选到不兼容渠道导致本地错误而中断重试。
		channel, err = model.GetRandomSatisfiedChannelByTypes(group, originalModel, retryCount, []int{common.ChannelTypeCodex})
	} else {
		channel, err = model.GetRandomSatisfiedChannel(group, originalModel, retryCount)
	}
	if err != nil {
		return nil, errors.New(fmt.Sprintf("获取重试渠道失败: %s", err.Error()))
	}
	middleware.SetupContextForSelectedChannel(c, channel, originalModel)
	return channel, nil
}

func shouldRetry(c *gin.Context, openaiErr *dto.OpenAIErrorWithStatusCode, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if openaiErr.LocalError {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if relayconstant.Path2RelayMode(c.Request.URL.Path) == relayconstant.RelayModeAudioTranscription {
		return false
	}
	if openaiErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if openaiErr.StatusCode == 307 {
		return true
	}
	if openaiErr.StatusCode/100 == 5 {
		// 超时不重试
		if openaiErr.StatusCode == 504 || openaiErr.StatusCode == 524 {
			return false
		}
		return true
	}
	if openaiErr.StatusCode == http.StatusBadRequest {
		channelType := c.GetInt("channel_type")
		if channelType == common.ChannelTypeAnthropic {
			return true
		}
		return false
	}
	if openaiErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if openaiErr.StatusCode/100 == 2 {
		return false
	}
	return true
}

func processChannelError(requestId string, group string, channelId int, channelType int, channelName string, originalModel string, relayMode int, autoBan bool, err *dto.OpenAIErrorWithStatusCode, shouldNotify bool) {
	if err == nil {
		return
	}
	ctx := context.WithValue(context.Background(), common.RequestIdKey, requestId)
	// 本地错误（参数校验/客户端中断等）不应视为“渠道异常”，避免误报与误禁用
	if err.LocalError || common.IsClientDisconnectMessage(err.Error.Message) {
		common.LogInfo(ctx, fmt.Sprintf("relay aborted (channel #%d, status code: %d): %s", channelId, err.StatusCode, err.Error.Message))
		return
	}
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously
	common.LogError(ctx, fmt.Sprintf("relay error (channel #%d, status code: %d): %s", channelId, err.StatusCode, err.Error.Message))
	if shouldNotify {
		subject := channelName + " 渠道调用异常!"
		content := fmt.Sprintf("通道 %s 调用失败，模型 %s，状态码 %d，错误信息 %s", channelName, originalModel, err.StatusCode, err.Error.Message)
		if sendErr := common.SendConfiguredEmailNotification(subject, content, group); sendErr != nil {
			common.SysError(fmt.Sprintf("failed to send notification email: %s", sendErr.Error()))
		}
		if sendErr := common.SendConfiguredFeishuNotification(subject, content, group); sendErr != nil {
			common.SysError(fmt.Sprintf("failed to send feishu notification: %s", sendErr.Error()))
		}
	}

	if service.ShouldDisableChannel(channelType, err) && autoBan {
		// 检查是否还有其他可用渠道支持相同的模型
		hasOtherChannels := model.HasOtherAvailableChannels(group, originalModel, channelId)
		if channelType == common.ChannelTypeCodex {
			// Codex CLI 场景：只认为同类型（Codex）渠道可替代，避免误把“最后一个 Codex 渠道”禁用掉。
			hasOtherChannels = model.HasOtherAvailableChannelsByTypes(group, originalModel, channelId, []int{common.ChannelTypeCodex})
		}
		if relayMode == relayconstant.RelayModeClaudeMessages &&
			(channelType == common.ChannelTypeClaudeCode || channelType == common.ChannelTypeOpenRouter) {
			// Claude Messages 兼容模式：仅 Claude Code / OpenRouter 视为可替代，避免误禁最后一个兼容渠道。
			hasOtherChannels = model.HasOtherAvailableChannelsByTypes(group, originalModel, channelId, []int{common.ChannelTypeClaudeCode, common.ChannelTypeOpenRouter})
		}

		if hasOtherChannels {
			// 还有其他可用渠道，正常禁用当前渠道
			service.DisableChannel(channelId, channelName, err.Error.Message, group)
		} else {
			// 这是最后一个可用渠道，不禁用，只记录警告
			common.LogWarn(ctx, fmt.Sprintf("channel #%d is the last available channel for model %s in group %s, skipping auto-disable", channelId, originalModel, group))
		}
	}
}

func RelayMidjourney(c *gin.Context) {
	relayMode := c.GetInt("relay_mode")
	var err *dto.MidjourneyResponse
	switch relayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		err = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		err = relay.RelayMidjourneyTask(c, relayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		err = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		err = relay.RelaySwapFace(c)
	default:
		err = relay.RelayMidjourneySubmit(c, relayMode)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(err)
	if err != nil {
		statusCode := http.StatusBadRequest
		if err.Code == 30 {
			err.Result = "当前分组负载已饱和，请稍后再试，或升级账户以提升服务质量。"
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", err.Description, err.Result),
			"type":        "upstream_error",
			"code":        err.Code,
		})
		channelId := c.GetInt("channel_id")
		common.LogError(c, fmt.Sprintf("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", err.Description, err.Result)))
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := dto.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := dto.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

func RelayTask(c *gin.Context) {
	retryTimes := common.RetryTimes
	channelId := c.GetInt("channel_id")
	relayMode := c.GetInt("relay_mode")
	group := c.GetString("group")
	originalModel := c.GetString("original_model")
	c.Set("use_channel", []string{fmt.Sprintf("%d", channelId)})
	taskErr := taskRelayHandler(c, relayMode)
	if taskErr == nil {
		retryTimes = 0
	}
	for i := 0; shouldRetryTaskRelay(c, channelId, taskErr, retryTimes) && i < retryTimes; i++ {
		channel, err := model.GetRandomSatisfiedChannel(group, originalModel, i)
		if err != nil {
			common.LogError(c, fmt.Sprintf("GetRandomSatisfiedChannel failed: %s", err.Error()))
			break
		}
		channelId = channel.Id
		useChannel := c.GetStringSlice("use_channel")
		useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
		c.Set("use_channel", useChannel)
		common.LogInfo(c, fmt.Sprintf("using channel #%d to retry (remain times %d)", channel.Id, i))
		middleware.SetupContextForSelectedChannel(c, channel, originalModel)

		requestBody, err := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		taskErr = taskRelayHandler(c, relayMode)
	}
	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		common.LogInfo(c, retryLogStr)
	}
	if taskErr != nil {
		if taskErr.StatusCode == http.StatusTooManyRequests {
			taskErr.Message = "当前分组上游负载已饱和，请稍后再试"
		}
		c.JSON(taskErr.StatusCode, taskErr)
	}
}

func taskRelayHandler(c *gin.Context, relayMode int) *dto.TaskError {
	var err *dto.TaskError
	switch relayMode {
	case relayconstant.RelayModeSunoFetch, relayconstant.RelayModeSunoFetchByID:
		err = relay.RelayTaskFetch(c, relayMode)
	default:
		err = relay.RelayTaskSubmit(c, relayMode)
	}
	return err
}

func shouldRetryTaskRelay(c *gin.Context, channelId int, taskErr *dto.TaskError, retryTimes int) bool {
	if taskErr == nil {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if taskErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if taskErr.StatusCode == 307 {
		return true
	}
	if taskErr.StatusCode/100 == 5 {
		// 超时不重试
		if taskErr.StatusCode == 504 || taskErr.StatusCode == 524 {
			return false
		}
		return true
	}
	if taskErr.StatusCode == http.StatusBadRequest {
		return false
	}
	if taskErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if taskErr.LocalError {
		return false
	}
	if taskErr.StatusCode/100 == 2 {
		return false
	}
	return true
}
