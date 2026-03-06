package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"one-api/common"
	"one-api/dto"
	"one-api/middleware"
	"one-api/model"
	"one-api/relay"
	relaycommon "one-api/relay/common"
	"one-api/relay/constant"
	"one-api/service"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

func testChannel(channel *model.Channel, testModel string) (err error, openAIErrorWithStatusCode *dto.OpenAIErrorWithStatusCode) {
	tik := time.Now()
	if channel.Type == common.ChannelTypeMidjourney {
		return errors.New("midjourney channel test is not supported"), nil
	}
	if channel.Type == common.ChannelTypeMidjourneyPlus {
		return errors.New("midjourney plus channel test is not supported!!!"), nil
	}
	if channel.Type == common.ChannelTypeSunoAPI {
		return errors.New("suno channel test is not supported"), nil
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// 根据模型类型确定正确的URL路径
	requestPath := "/v1/chat/completions"
	if testModel == "" {
		if channel.TestModel != nil && *channel.TestModel != "" {
			testModel = *channel.TestModel
		} else {
			if len(channel.GetModels()) > 0 {
				testModel = channel.GetModels()[0]
			} else {
				testModel = "gpt-3.5-turbo"
			}
		}
	}

	// Codex（OAuth）优先挑选更可能可用的模型，避免默认 gpt-3.5 导致上游 400
	if channel.Type == common.ChannelTypeCodex {
		if st := channel.GetSetting(); st != nil {
			if m, ok := st["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
				if testModel == "" || strings.Contains(strings.ToLower(testModel), "3.5") {
					testModel = pickCodexOAuthTestModel(channel.GetModels())
				}
			}
		}
	}

	// 判断是否为 Embedding 模型
	if isEmbeddingModel(testModel) {
		requestPath = "/v1/embeddings"
	}

	c.Request = &http.Request{
		Method: "POST",
		URL:    &url.URL{Path: requestPath},
		Body:   nil,
		Header: make(http.Header),
	}

	// 如果指定了testModel，处理模型映射
	if testModel != "" {
		modelMapping := channel.GetModelMapping()
		if modelMapping != "" && modelMapping != "{}" {
			modelMap := make(map[string]string)
			err := json.Unmarshal([]byte(modelMapping), &modelMap)
			if err != nil {
				return err, service.OpenAIErrorWrapperLocal(err, "unmarshal_model_mapping_failed", http.StatusInternalServerError)
			}
			if modelMap[testModel] != "" {
				testModel = modelMap[testModel]
			}
		}
	}

	c.Request.Header.Set("Authorization", "Bearer "+channel.Key)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	c.Set("proxy_url", channel.GetProxyURL())

	middleware.SetupContextForSelectedChannel(c, channel, testModel)

	meta := relaycommon.GenRelayInfo(c)
	apiType, _ := constant.ChannelType2APIType(channel.Type)
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return fmt.Errorf("invalid api type: %d, adaptor is nil", apiType), nil
	}

	request := buildTestRequest(testModel)
	meta.UpstreamModelName = testModel
	common.SysLog(fmt.Sprintf("testing channel %d with model %s", channel.Id, testModel))

	adaptor.Init(meta)

	convertedRequest, err := adaptor.ConvertRequest(c, meta, request)
	if err != nil {
		return err, nil
	}
	if u, err := adaptor.GetRequestURL(meta); err == nil {
		common.SysLog(fmt.Sprintf("testing channel %d upstream url: %s", channel.Id, u))
	}
	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		return err, nil
	}
	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(requestBody)
	resp, err := adaptor.DoRequest(c, meta, requestBody)
	if err != nil {
		return err, nil
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(httpResp.Body)
			_ = httpResp.Body.Close()
			msg, errType, errCode := parseUpstreamTestErrorMessage(raw)
			if msg == "" {
				msg = fmt.Sprintf("bad response status code %d", httpResp.StatusCode)
			}
			t := strings.TrimSpace(errType)
			if t == "" {
				t = "upstream_error"
			}
			code := any("bad_response_status_code")
			if errCode != nil {
				code = errCode
			}
			return fmt.Errorf("status code %d: %s", httpResp.StatusCode, msg), &dto.OpenAIErrorWithStatusCode{
				StatusCode: httpResp.StatusCode,
				Error: dto.OpenAIError{
					Message: msg,
					Type:    t,
					Code:    code,
				},
			}
		}
	}
	usageA, respErr := adaptor.DoResponse(c, httpResp, meta)
	if respErr != nil {
		return fmt.Errorf("%s", respErr.Error.Message), respErr
	}
	if usageA == nil {
		return errors.New("usage is nil"), nil
	}
	usage := usageA.(*dto.Usage)
	result := w.Result()
	respBody, err := io.ReadAll(result.Body)
	if err != nil {
		return err, nil
	}
	modelPrice, usePrice := common.GetModelPrice(testModel, false)
	modelRatio := common.GetModelRatio(testModel)
	completionRatio := common.GetCompletionRatio(testModel)
	ratio := modelRatio
	quota := 0
	if !usePrice {
		quota = usage.PromptTokens + int(math.Round(float64(usage.CompletionTokens)*completionRatio))
		quota = int(math.Round(float64(quota) * ratio))
		if ratio != 0 && quota <= 0 {
			quota = 1
		}
	} else {
		quota = int(modelPrice * common.QuotaPerUnit)
	}
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	consumedTime := float64(milliseconds) / 1000.0
	other := service.GenerateTextOtherInfo(c, meta, modelRatio, 1, completionRatio, modelPrice)
	model.RecordConsumeLog(c, 1, channel.Id, usage.PromptTokens, usage.CompletionTokens, testModel, "模型测试",
		quota, "模型测试", 0, quota, int(consumedTime), false, "default", other)
	common.SysLog(fmt.Sprintf("testing channel #%d, response: \n%s", channel.Id, string(respBody)))
	return nil, nil
}

func pickCodexOAuthTestModel(models []string) string {
	if len(models) == 0 {
		return "gpt-4o"
	}
	prefer := []string{
		"gpt-5-codex",
		"gpt-5",
		"gpt-4o",
		"gpt-4",
		"gpt-4o-mini",
		"gpt-4.1",
		"gpt-4.1-mini",
	}
	for _, p := range prefer {
		for _, m := range models {
			if m == p || strings.HasPrefix(m, p+"-") {
				return m
			}
		}
	}
	for _, m := range models {
		if strings.HasPrefix(m, "gpt-") && !strings.Contains(m, "3.5") {
			return m
		}
	}
	return models[0]
}

func parseUpstreamTestErrorMessage(raw []byte) (msg string, errType string, errCode any) {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return "", "", nil
	}
	var errResp dto.GeneralErrorResponse
	if json.Unmarshal(b, &errResp) == nil {
		errType = strings.TrimSpace(errResp.Error.Type)
		errCode = errResp.Error.Code
		if errResp.Error.Message != "" {
			msg = errResp.Error.Message
		} else if m := strings.TrimSpace(errResp.ToMessage()); m != "" {
			msg = m
		}
		if msg != "" {
			msg = relay.AugmentUsageLimitReachedMessage(b, errType, msg)
			return msg, errType, errCode
		}
	}
	// 兜底：返回原始字符串（截断，避免太长）
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300], "", nil
	}
	return s, "", nil
}

// isEmbeddingModel 判断是否为 Embedding 模型
func isEmbeddingModel(model string) bool {
	// model 转为小写
	model = strings.ToLower(model)
	return strings.Contains(strings.ToLower(model), "embedding") ||
		strings.HasPrefix(model, "m3e") ||
		strings.Contains(model, "bge-") ||
		strings.Contains(model, "embedding")
}

func buildTestRequest(model string) *dto.GeneralOpenAIRequest {
	testRequest := &dto.GeneralOpenAIRequest{
		Model:  "", // this will be set later
		Stream: false,
	}
	// 先判断是否为 Embedding 模型
	if isEmbeddingModel(model) {
		testRequest.Model = model
		testRequest.Input = []any{"hello world"}
		return testRequest
	}
	// 判断需要特殊处理的模型
	if strings.HasPrefix(model, "o") || strings.HasPrefix(model, "gpt-5") {
		testRequest.MaxCompletionTokens = 10
	} else if strings.Contains(model, "thinking") {
		if !strings.Contains(model, "claude") {
			testRequest.MaxTokens = 50
		}
	} else if strings.Contains(model, "gemini") {
		testRequest.MaxTokens = 3000
	} else {
		testRequest.MaxTokens = 10
	}
	content, _ := json.Marshal("hi")
	testMessage := dto.Message{
		Role:    "user",
		Content: content,
	}
	testRequest.Model = model
	testRequest.Messages = append(testRequest.Messages, testMessage)
	return testRequest
}

func TestChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	testModel := c.Query("model")
	tik := time.Now()
	err, _ = testChannel(channel, testModel)
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	go channel.UpdateResponseTime(milliseconds)
	consumedTime := float64(milliseconds) / 1000.0
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
			"time":    consumedTime,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"time":    consumedTime,
	})
	return
}

var testAllChannelsLock sync.Mutex
var testAllChannelsRunning bool = false

func testAllChannels(notify bool) error {
	testAllChannelsLock.Lock()
	if testAllChannelsRunning {
		testAllChannelsLock.Unlock()
		return errors.New("测试已在运行中")
	}
	testAllChannelsRunning = true
	testAllChannelsLock.Unlock()
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		return err
	}
	var disableThreshold = int64(common.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000 // a impossible value
	}
	gopool.Go(func() {
		for _, channel := range channels {
			isChannelEnabled := channel.Status == common.ChannelStatusEnabled
			tik := time.Now()
			err, openaiWithStatusErr := testChannel(channel, "")
			tok := time.Now()
			milliseconds := tok.Sub(tik).Milliseconds()

			shouldBanChannel := false

			// request error disables the channel
			if openaiWithStatusErr != nil {
				oaiErr := openaiWithStatusErr.Error
				err = errors.New(fmt.Sprintf("type %s, httpCode %d, code %v, message %s", oaiErr.Type, openaiWithStatusErr.StatusCode, oaiErr.Code, oaiErr.Message))
				shouldBanChannel = service.ShouldDisableChannel(channel.Type, openaiWithStatusErr)
			}

			if milliseconds > disableThreshold {
				err = errors.New(fmt.Sprintf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0))
				shouldBanChannel = true
			}

			// disable channel
			if isChannelEnabled && shouldBanChannel && channel.GetAutoBan() {
				service.DisableChannel(channel.Id, channel.Name, err.Error(), channel.Group)
			}

			// enable channel
			if !isChannelEnabled && service.ShouldEnableChannel(err, openaiWithStatusErr, channel.Status) {
				service.EnableChannel(channel.Id, channel.Name, channel.Group)
			}

			channel.UpdateResponseTime(milliseconds)
			time.Sleep(common.RequestInterval)
		}
		testAllChannelsLock.Lock()
		testAllChannelsRunning = false
		testAllChannelsLock.Unlock()
		if notify {
			subject := "通道测试完成"
			content := "通道测试完成，如果没有收到禁用通知，说明所有通道都正常"
			if common.EmailNotificationEnabled && common.NotificationEmail != "" {
				err := common.SendEmailWithCc(
					subject,
					common.NotificationEmail,
					common.JoinEmailRecipients(common.NotificationCcEmails),
					content,
				)
				if err != nil {
					common.SysError(fmt.Sprintf("failed to send email: %s", err.Error()))
				}
			}
			if common.FeishuNotificationEnabled && common.FeishuWebhookURL != "" {
				err := common.SendFeishuWebhook(common.FeishuWebhookURL, subject, content)
				if err != nil {
					common.SysError(fmt.Sprintf("failed to send feishu notification: %s", err.Error()))
				}
			}
		}
	})
	return nil
}

func TestAllChannels(c *gin.Context) {
	err := testAllChannels(true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func AutomaticallyTestChannels(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Minute)
		common.SysLog("testing all channels")
		_ = testAllChannels(false)
		common.SysLog("channel test finished")
	}
}
