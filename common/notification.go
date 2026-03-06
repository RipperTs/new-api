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
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
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
