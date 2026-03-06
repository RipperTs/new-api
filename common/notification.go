package common

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"time"
)

func ShouldNotifyByGroups(configuredGroups []string, currentGroup string) bool {
	if len(configuredGroups) == 0 {
		return true
	}
	for _, group := range configuredGroups {
		if group == currentGroup {
			return true
		}
	}
	return false
}

func SplitNotificationGroups(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	rawGroups := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
	groups := make([]string, 0, len(rawGroups))
	for _, group := range rawGroups {
		group = strings.TrimSpace(group)
		if group != "" {
			groups = append(groups, group)
		}
	}
	return groups
}

func ShouldNotifyByGroupExpression(configuredGroups []string, groupExpression string) bool {
	if len(configuredGroups) == 0 {
		return true
	}
	groups := SplitNotificationGroups(groupExpression)
	if len(groups) == 0 {
		return false
	}
	for _, group := range groups {
		if ShouldNotifyByGroups(configuredGroups, strings.TrimSpace(group)) {
			return true
		}
	}
	return false
}

func SendConfiguredEmailNotification(subject string, content string, groupExpression string) error {
	if !EmailNotificationEnabled {
		return nil
	}
	if NotificationEmail == "" {
		return nil
	}
	if !ShouldNotifyByGroupExpression(EmailNotificationGroups, groupExpression) {
		return nil
	}
	return SendEmailWithCc(subject, NotificationEmail, JoinEmailRecipients(NotificationCcEmails), content)
}

func SendConfiguredFeishuNotification(title string, content string, groupExpression string) error {
	if !FeishuNotificationEnabled {
		return nil
	}
	if FeishuWebhookURL == "" {
		return nil
	}
	if !ShouldNotifyByGroupExpression(FeishuNotificationGroups, groupExpression) {
		return nil
	}

	// 飞书通知去重：与邮件去重一致，先做内容归一化，避免随机 UUID/请求ID 导致缓存失效
	cacheKey := ""
	if RedisEnabled && RDB != nil {
		cacheText := strings.TrimSpace(groupExpression) + "\n" + strings.TrimSpace(title) + "\n" + strings.TrimSpace(content)
		normalized := normalizeContentForCache(cacheText)
		if len(normalized) > 100 {
			normalized = normalized[:100]
		}
		hash := md5.Sum([]byte(normalized))
		cacheKey = "feishu_cache:" + hex.EncodeToString(hash[:])
		redisValue, _ := RedisGet(cacheKey)
		if redisValue != "" {
			return nil
		}
	}

	err := SendFeishuWebhook(FeishuWebhookURL, title, content)
	if err != nil {
		return err
	}
	if cacheKey != "" {
		_ = RedisSet(cacheKey, "1", time.Duration(GetEnvOrDefault("INTERVAL_TIME", 60))*time.Second)
	}
	return nil
}
