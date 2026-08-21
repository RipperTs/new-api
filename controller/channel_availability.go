package controller

import (
	"fmt"
	"net/http"
	"time"

	"one-api/model"

	"github.com/gin-gonic/gin"
)

const (
	channelAvailabilityPeriod24Hours = "24h"
	channelAvailabilityPeriodToday   = "today"
	channelAvailabilityPeriod7Days   = "7d"
)

type channelAvailabilityRequest struct {
	ChannelIds []int  `json:"channel_ids"`
	Period     string `json:"period"`
}

func GetChannelAvailability(c *gin.Context) {
	request := channelAvailabilityRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	channelIds, err := normalizeChannelIds(request.ChannelIds)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	startTimestamp, endTimestamp, bucketSeconds, err := getChannelAvailabilityRange(request.Period, time.Now())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	stats, err := model.GetChannelAvailabilityStats(channelIds, startTimestamp, endTimestamp, bucketSeconds)
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
		"data":    stats,
	})
}

func normalizeChannelIds(channelIds []int) ([]int, error) {
	seen := make(map[int]struct{}, len(channelIds))
	normalized := make([]int, 0, len(channelIds))
	for _, channelId := range channelIds {
		if channelId <= 0 {
			return nil, fmt.Errorf("渠道 ID 无效：%d", channelId)
		}
		if _, ok := seen[channelId]; ok {
			continue
		}
		seen[channelId] = struct{}{}
		normalized = append(normalized, channelId)
	}
	return normalized, nil
}

func getChannelAvailabilityRange(period string, now time.Time) (int64, int64, int64, error) {
	endTimestamp := now.Unix()
	switch period {
	case "", channelAvailabilityPeriod24Hours:
		return now.Add(-24 * time.Hour).Unix(), endTimestamp, 30 * 60, nil
	case channelAvailabilityPeriodToday:
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return start.Unix(), endTimestamp, 30 * 60, nil
	case channelAvailabilityPeriod7Days:
		return now.Add(-7 * 24 * time.Hour).Unix(), endTimestamp, 3 * 60 * 60, nil
	default:
		return 0, 0, 0, fmt.Errorf("不支持的时间段：%s", period)
	}
}
