package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"one-api/common"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var notificationValuePlaceholderRegex = regexp.MustCompile(`\{\{\s*value\s*\}\}`)

type newAPIWebhookNotificationRequest struct {
	Type      string        `json:"type"`
	Title     string        `json:"title"`
	Content   string        `json:"content"`
	Values    []interface{} `json:"values"`
	Timestamp interface{}   `json:"timestamp"`
}

type notificationPayload struct {
	Title        string
	TextContent  string
	EmailContent string
}

func SendWebhookNotification(c *gin.Context) {
	channel := parseNotificationChannel(c)
	if channel == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "请通过 channel 参数指定通知渠道：email、feishu 或 dingtalk",
		})
		return
	}

	format := strings.ToLower(strings.TrimSpace(c.Query("format")))
	if format != "" && format != "new-api" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "仅支持空 format 或 format=new-api",
		})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "读取请求体失败",
		})
		return
	}

	payload, err := parseNotificationPayload(format, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if err = dispatchNotification(channel, payload); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "通知发送成功",
		"data": gin.H{
			"channel": channel,
			"format":  format,
		},
	})
}

func parseNotificationChannel(c *gin.Context) string {
	channel := strings.TrimSpace(c.Query("channel"))
	if channel == "" {
		channel = strings.TrimSpace(c.Query("notify_type"))
	}
	channel = strings.ToLower(channel)
	switch channel {
	case "email", "mail":
		return "email"
	case "feishu", "lark":
		return "feishu"
	case "dingtalk":
		return "dingtalk"
	default:
		return ""
	}
}

func parseNotificationPayload(format string, body []byte) (*notificationPayload, error) {
	trimmedBody := bytes.TrimSpace(body)
	if len(trimmedBody) == 0 {
		return nil, fmt.Errorf("请求体不能为空")
	}
	if format == "new-api" {
		return parseNewAPINotificationPayload(trimmedBody)
	}
	return parsePlainNotificationPayload(trimmedBody)
}

func parseNewAPINotificationPayload(body []byte) (*notificationPayload, error) {
	var req newAPIWebhookNotificationRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&req); err != nil {
		return nil, fmt.Errorf("new-api 格式解析失败：%s", err.Error())
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = common.SystemName + "通知"
	}
	contentTemplate := strings.TrimSpace(req.Content)
	if contentTemplate == "" {
		return nil, fmt.Errorf("new-api 格式缺少 content")
	}

	parsedContent := replaceValuePlaceholder(contentTemplate, req.Values)
	timestamp, hasTimestamp := parseUnixTimestamp(req.Timestamp)

	lines := make([]string, 0, 3)
	if strings.TrimSpace(req.Type) != "" {
		lines = append(lines, "类型："+strings.TrimSpace(req.Type))
	}
	if hasTimestamp && timestamp > 0 {
		lines = append(lines, "时间："+time.Unix(timestamp, 0).Format("2006-01-02 15:04:05"))
	}
	lines = append(lines, "内容："+parsedContent)

	return &notificationPayload{
		Title:        title,
		TextContent:  strings.Join(lines, "\n"),
		EmailContent: buildEmailHTML(lines),
	}, nil
}

func parsePlainNotificationPayload(body []byte) (*notificationPayload, error) {
	title := ""
	content := ""

	var data interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err == nil {
		switch v := data.(type) {
		case string:
			content = strings.TrimSpace(v)
		case map[string]interface{}:
			title = firstNonEmptyString(v["title"], v["subject"])
			content = firstNonEmptyString(v["content"], v["message"], v["text"], v["body"])
		}
	}

	if content == "" {
		content = strings.TrimSpace(string(body))
	}
	if content == "" {
		return nil, fmt.Errorf("纯文本通知内容为空")
	}
	if title == "" {
		title = common.SystemName + "通知"
	}

	lines := []string{content}
	return &notificationPayload{
		Title:        title,
		TextContent:  content,
		EmailContent: buildEmailHTML(lines),
	}, nil
}

func replaceValuePlaceholder(content string, values []interface{}) string {
	index := 0
	return notificationValuePlaceholderRegex.ReplaceAllStringFunc(content, func(_ string) string {
		if index >= len(values) {
			return "{{value}}"
		}
		if values[index] == nil {
			index++
			return ""
		}
		value := strings.TrimSpace(fmt.Sprintf("%v", values[index]))
		index++
		return value
	})
}

func parseUnixTimestamp(timestamp interface{}) (int64, bool) {
	switch v := timestamp.(type) {
	case nil:
		return 0, false
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		if ts, err := v.Int64(); err == nil {
			return ts, true
		}
		if f, err := v.Float64(); err == nil {
			return int64(f), true
		}
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
			return ts, true
		}
	}
	return 0, false
}

func firstNonEmptyString(values ...interface{}) string {
	for _, value := range values {
		switch v := value.(type) {
		case nil:
			continue
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				return trimmed
			}
		default:
			converted := strings.TrimSpace(fmt.Sprintf("%v", v))
			if converted != "" && converted != "<nil>" {
				return converted
			}
		}
	}
	return ""
}

func buildEmailHTML(lines []string) string {
	var builder strings.Builder
	for _, line := range lines {
		escaped := html.EscapeString(strings.TrimSpace(line))
		if escaped == "" {
			continue
		}
		escaped = strings.ReplaceAll(escaped, "\n", "<br/>")
		builder.WriteString("<p>")
		builder.WriteString(escaped)
		builder.WriteString("</p>")
	}
	return builder.String()
}

func dispatchNotification(channel string, payload *notificationPayload) error {
	switch channel {
	case "email":
		if strings.TrimSpace(common.NotificationEmail) == "" {
			return fmt.Errorf("系统未配置 NOTIFICATION_EMAIL")
		}
		cc := common.JoinEmailRecipients(common.NotificationCcEmails)
		return common.SendEmailWithCcNoCache(payload.Title, common.NotificationEmail, cc, payload.EmailContent)
	case "feishu":
		if strings.TrimSpace(common.FeishuWebhookURL) == "" {
			return fmt.Errorf("系统未配置 FEISHU_WEBHOOK_URL")
		}
		return common.SendFeishuWebhook(common.FeishuWebhookURL, payload.Title, payload.TextContent)
	case "dingtalk":
		if strings.TrimSpace(common.DingTalkWebhookURL) == "" {
			return fmt.Errorf("系统未配置 DINGTALK_WEBHOOK_URL")
		}
		return common.SendDingTalkWebhook(common.DingTalkWebhookURL, common.DingTalkWebhookSecret, payload.Title, payload.TextContent)
	default:
		return fmt.Errorf("不支持的通知渠道：%s", channel)
	}
}
