package common

import (
	"strings"
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
	return SendFeishuWebhook(FeishuWebhookURL, title, content)
}
