package controller

import (
	"encoding/json"
	"net/http"
	"one-api/common"
	"one-api/model"
	"one-api/setting"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func GetOptions(c *gin.Context) {
	var options []*model.Option
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		if strings.HasSuffix(k, "Token") || strings.HasSuffix(k, "Secret") || strings.HasSuffix(k, "Key") {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: common.Interface2String(v),
		})
	}
	common.OptionMapRWMutex.Unlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
	return
}

func UpdateOption(c *gin.Context) {
	var option model.Option
	err := json.NewDecoder(c.Request.Body).Decode(&option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	switch option.Key {
	case "GitHubOAuthEnabled":
		if option.Value == "true" && common.GitHubClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！",
			})
			return
		}
	case "LinuxDOOAuthEnabled":
		if option.Value == "true" && common.LinuxDOClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 LinuxDO OAuth，请先填入 LinuxDO Client Id 以及 LinuxDO Client Secret！",
			})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用邮箱域名限制，请先填入限制的邮箱域名！",
			})
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && common.WeChatServerAddress == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用微信登录，请先填入微信登录相关配置信息！",
			})
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && common.TurnstileSiteKey == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！",
			})
			return
		}
	case "GroupRatio":
		err = setting.CheckGroupRatio(option.Value)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "NotificationEmail":
		err = common.Validate.Var(strings.TrimSpace(option.Value), "omitempty,email")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "通知目标邮箱格式不正确",
			})
			return
		}
	case "NotificationCcEmails":
		for _, email := range common.SplitEmailRecipients(option.Value) {
			err = common.Validate.Var(email, "email")
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "抄送邮箱格式不正确",
				})
				return
			}
		}
	case "FeishuWebhookURL":
		err = common.Validate.Var(strings.TrimSpace(option.Value), "omitempty,url")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "飞书 Webhook 地址格式不正确",
			})
			return
		}
	case "DingTalkWebhookURL":
		err = common.Validate.Var(strings.TrimSpace(option.Value), "omitempty,url")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "钉钉 Webhook 地址格式不正确",
			})
			return
		}
	}
	err = model.UpdateOption(option.Key, option.Value)
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

type NotificationMailRequest struct {
	NotificationEmail    string `json:"notification_email"`
	NotificationCcEmails string `json:"notification_cc_emails"`
}

type NotificationFeishuRequest struct {
	FeishuWebhookURL string `json:"feishu_webhook_url"`
}

type NotificationDingTalkRequest struct {
	DingTalkWebhookURL    string  `json:"dingtalk_webhook_url"`
	DingTalkWebhookSecret *string `json:"dingtalk_webhook_secret"`
}

func SendNotificationMail(c *gin.Context) {
	var req NotificationMailRequest
	err := json.NewDecoder(c.Request.Body).Decode(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	req.NotificationEmail = strings.TrimSpace(req.NotificationEmail)
	if err = common.Validate.Var(req.NotificationEmail, "required,email"); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请填写正确的通知目标邮箱",
		})
		return
	}
	for _, email := range common.SplitEmailRecipients(req.NotificationCcEmails) {
		if err = common.Validate.Var(email, "email"); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "抄送邮箱格式不正确",
			})
			return
		}
	}

	subject := common.SystemName + "通知已送达"
	content := "<p>您好，当前系统的异常邮件通知链路已可正常投递。</p>" +
		"<p>系统名称：" + common.SystemName + "</p>" +
		"<p>送达时间：" + time.Now().Format("2006-01-02 15:04:05") + "</p>" +
		"<p>如您收到此邮件，说明通知邮箱配置已经生效。</p>"
	err = common.SendEmailWithCcNoCache(subject, req.NotificationEmail, req.NotificationCcEmails, content)
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
}

func SendNotificationFeishu(c *gin.Context) {
	var req NotificationFeishuRequest
	err := json.NewDecoder(c.Request.Body).Decode(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	req.FeishuWebhookURL = strings.TrimSpace(req.FeishuWebhookURL)
	if err = common.Validate.Var(req.FeishuWebhookURL, "required,url"); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请填写正确的飞书 Webhook 地址",
		})
		return
	}

	title := common.SystemName + "通知已送达"
	content := "当前系统的异常通知链路已可正常投递。\n" +
		"系统名称：" + common.SystemName + "\n" +
		"送达时间：" + time.Now().Format("2006-01-02 15:04:05")
	err = common.SendFeishuWebhook(req.FeishuWebhookURL, title, content)
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
}

func SendNotificationDingTalk(c *gin.Context) {
	var req NotificationDingTalkRequest
	err := json.NewDecoder(c.Request.Body).Decode(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	req.DingTalkWebhookURL = strings.TrimSpace(req.DingTalkWebhookURL)
	if err = common.Validate.Var(req.DingTalkWebhookURL, "required,url"); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请填写正确的钉钉 Webhook 地址",
		})
		return
	}

	title := common.SystemName + "通知已送达"
	content := "当前系统的异常通知链路已可正常投递。\n" +
		"系统名称：" + common.SystemName + "\n" +
		"送达时间：" + time.Now().Format("2006-01-02 15:04:05")
	secret := common.DingTalkWebhookSecret
	if req.DingTalkWebhookSecret != nil {
		secret = strings.TrimSpace(*req.DingTalkWebhookSecret)
	}
	err = common.SendDingTalkWebhook(req.DingTalkWebhookURL, secret, title, content)
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
}
