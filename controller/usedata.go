package controller

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"one-api/model"
	"strconv"
	"strings"
)

func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	group := c.Query("group")
	tokenId, _ := strconv.Atoi(c.Query("token_id"))
	usePromptCompletion, _ := strconv.ParseBool(c.DefaultQuery("use_prompt_completion", "false"))
	modelNames := parseModelNames(c.Query("model_names"))
	dates, err := model.GetAllQuotaDates(startTimestamp, endTimestamp, username, group, tokenId, usePromptCompletion, modelNames)
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
		"data":    dates,
	})
	return
}

func GetUserQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 判断时间跨度是否超过 1 个月
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	group := c.Query("group")
	tokenId, _ := strconv.Atoi(c.Query("token_id"))
	usePromptCompletion, _ := strconv.ParseBool(c.DefaultQuery("use_prompt_completion", "false"))
	modelNames := parseModelNames(c.Query("model_names"))
	dates, err := model.GetQuotaDataByUserId(userId, startTimestamp, endTimestamp, group, tokenId, usePromptCompletion, modelNames)
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
		"data":    dates,
	})
	return
}

func parseModelNames(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	modelNames := make([]string, 0, len(parts))
	for _, item := range parts {
		modelName := strings.TrimSpace(item)
		if modelName == "" {
			continue
		}
		modelNames = append(modelNames, modelName)
	}
	return modelNames
}
