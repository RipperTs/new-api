package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type feishuWebhookRequest struct {
	MsgType string `json:"msg_type"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
}

func SendFeishuWebhook(webhookURL string, title string, content string) error {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return fmt.Errorf("飞书 Webhook 未配置")
	}

	reqBody := feishuWebhookRequest{MsgType: "text"}
	reqBody.Content.Text = strings.TrimSpace(title) + "\n" + strings.TrimSpace(content)

	rawBody, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBuffer(rawBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("飞书 Webhook 请求失败: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var respData map[string]interface{}
	if err = json.Unmarshal(respBody, &respData); err == nil {
		if code, ok := toInt(respData["code"]); ok && code != 0 {
			return fmt.Errorf("飞书 Webhook 返回异常: code=%d msg=%v", code, respData["msg"])
		}
		if statusCode, ok := toInt(respData["StatusCode"]); ok && statusCode != 0 {
			return fmt.Errorf("飞书 Webhook 返回异常: StatusCode=%d StatusMessage=%v", statusCode, respData["StatusMessage"])
		}
	}

	return nil
}

func toInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float32:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}
