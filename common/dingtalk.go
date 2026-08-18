package common

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type dingTalkWebhookRequest struct {
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
}

type dingTalkWebhookResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func SendDingTalkWebhook(webhookURL string, secret string, title string, content string) error {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return fmt.Errorf("钉钉 Webhook 未配置")
	}

	signedURL, err := buildDingTalkWebhookURL(webhookURL, strings.TrimSpace(secret))
	if err != nil {
		return err
	}

	reqBody := dingTalkWebhookRequest{MsgType: "text"}
	reqBody.Text.Content = strings.TrimSpace(title) + "\n" + strings.TrimSpace(content)
	rawBody, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, signedURL, bytes.NewBuffer(rawBody))
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
		return fmt.Errorf("钉钉 Webhook 请求失败: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var respData dingTalkWebhookResponse
	if err = json.Unmarshal(respBody, &respData); err != nil {
		return fmt.Errorf("钉钉 Webhook 响应解析失败: %w", err)
	}
	if respData.ErrCode != 0 {
		return fmt.Errorf("钉钉 Webhook 返回异常: errcode=%d errmsg=%s", respData.ErrCode, respData.ErrMsg)
	}
	return nil
}

func buildDingTalkWebhookURL(webhookURL string, secret string) (string, error) {
	if secret == "" {
		return webhookURL, nil
	}

	parsedURL, err := url.Parse(webhookURL)
	if err != nil {
		return "", fmt.Errorf("钉钉 Webhook 地址解析失败: %w", err)
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	query := parsedURL.Query()
	query.Set("timestamp", timestamp)
	query.Set("sign", sign)
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String(), nil
}
