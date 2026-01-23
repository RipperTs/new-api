package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"one-api/common"
	"one-api/model"
	"one-api/service"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type OpenAIModel struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Created    int64  `json:"created"`
	OwnedBy    string `json:"owned_by"`
	Permission []struct {
		ID                 string `json:"id"`
		Object             string `json:"object"`
		Created            int64  `json:"created"`
		AllowCreateEngine  bool   `json:"allow_create_engine"`
		AllowSampling      bool   `json:"allow_sampling"`
		AllowLogprobs      bool   `json:"allow_logprobs"`
		AllowSearchIndices bool   `json:"allow_search_indices"`
		AllowView          bool   `json:"allow_view"`
		AllowFineTuning    bool   `json:"allow_fine_tuning"`
		Organization       string `json:"organization"`
		Group              string `json:"group"`
		IsBlocking         bool   `json:"is_blocking"`
	} `json:"permission"`
	Root   string `json:"root"`
	Parent string `json:"parent"`
}

type OpenAIModelsResponse struct {
	Data    []OpenAIModel `json:"data"`
	Success bool          `json:"success"`
}

func GetAllChannels(c *gin.Context) {
	p, _ := strconv.Atoi(c.Query("p"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if p < 0 {
		p = 0
	}
	if pageSize < 0 {
		pageSize = common.ItemsPerPage
	}
	channelData := make([]*model.Channel, 0)
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	if enableTagMode {
		tags, err := model.GetPaginatedTags(p*pageSize, pageSize)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		for _, tag := range tags {
			if tag != nil && *tag != "" {
				tagChannel, err := model.GetChannelsByTag(*tag, idSort)
				if err == nil {
					channelData = append(channelData, tagChannel...)
				}
			}
		}
	} else {
		channels, err := model.GetAllChannels(p*pageSize, pageSize, false, idSort)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		channelData = channels
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channelData,
	})
	return
}

func FetchUpstreamModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	channel, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if channel.Type == common.ChannelTypeCodex {
		st := channel.GetSetting()
		if m, ok := st["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Codex Auth 模式暂不支持自动获取模型列表，请手动维护模型列表",
			})
			return
		}
	}

	baseURL := common.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}
	url := fmt.Sprintf("%s/v1/models", baseURL)
	body, err := GetResponseBody("GET", url, channel, GetAuthHeader(channel.Key))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	var result OpenAIModelsResponse
	if err = json.Unmarshal(body, &result); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("解析响应失败: %s", err.Error()),
		})
		return
	}

	var ids []string
	for _, model := range result.Data {
		ids = append(ids, model.ID)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    ids,
	})
}

func FixChannelsAbilities(c *gin.Context) {
	count, err := model.FixAbility()
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
		"data":    count,
	})
}

func SearchChannels(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	modelKeyword := c.Query("model")
	idSort, _ := strconv.ParseBool(c.Query("id_sort"))
	enableTagMode, _ := strconv.ParseBool(c.Query("tag_mode"))
	channelData := make([]*model.Channel, 0)
	if enableTagMode {
		tags, err := model.SearchTags(keyword, group, modelKeyword, idSort)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		for _, tag := range tags {
			if tag != nil && *tag != "" {
				tagChannel, err := model.GetChannelsByTag(*tag, idSort)
				if err == nil {
					channelData = append(channelData, tagChannel...)
				}
			}
		}
	} else {
		channels, err := model.SearchChannels(keyword, group, modelKeyword, idSort)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		channelData = channels
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    channelData,
	})
	return
}

func GetChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel, err := model.GetChannelById(id, false)
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
		"data":    channel,
	})
	return
}

func AddChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	channel.CreatedTime = common.GetTimestamp()
	var codexSessionToken *service.CodexTokenData
	codexSessionID := ""
	var claudeSessionToken *service.ClaudeTokenData
	claudeSessionID := ""
	if channel.Type == common.ChannelTypeCodex {
		st := channel.GetSetting()
		if m, ok := st["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
			if sid, ok := st["codex_oauth_session_id"].(string); ok && strings.TrimSpace(sid) != "" {
				codexSessionID = strings.TrimSpace(sid)
				td, err := service.CodexLoadOAuthSession(codexSessionID)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"success": false, "message": "Codex 授权信息已过期，请重新授权"})
					return
				}
				if strings.TrimSpace(td.RefreshToken) == "" {
					c.JSON(http.StatusOK, gin.H{"success": false, "message": "Codex 授权信息无效，请重新授权"})
					return
				}
				codexSessionToken = td
				channel.Key = td.RefreshToken
				delete(st, "codex_oauth_session_id")
				st["auth_mode"] = "oauth"
				if strings.TrimSpace(td.Email) != "" {
					st["codex_email"] = td.Email
				} else {
					delete(st, "codex_email")
				}
				if strings.TrimSpace(td.AccountID) != "" {
					st["chatgpt_account_id"] = td.AccountID
				} else {
					delete(st, "chatgpt_account_id")
				}
				channel.SetSetting(st)
				// 默认走官方 backend-api
				if channel.GetBaseURL() == "" {
					base := "https://chatgpt.com/backend-api"
					channel.BaseURL = &base
				}
			} else {
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "请选择 Codex 授权方式后完成登录授权"})
				return
			}
		}
	}
	if channel.Type == common.ChannelTypeClaudeCode {
		st := channel.GetSetting()
		if m, ok := st["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
			if sid, ok := st["claude_oauth_session_id"].(string); ok && strings.TrimSpace(sid) != "" {
				claudeSessionID = strings.TrimSpace(sid)
				td, err := service.ClaudeLoadOAuthSession(claudeSessionID)
				if err != nil {
					c.JSON(http.StatusOK, gin.H{"success": false, "message": "Claude 授权信息已过期，请重新授权"})
					return
				}
				if strings.TrimSpace(td.RefreshToken) == "" {
					c.JSON(http.StatusOK, gin.H{"success": false, "message": "Claude 授权信息无效，请重新授权"})
					return
				}
				claudeSessionToken = td
				channel.Key = td.RefreshToken
				delete(st, "claude_oauth_session_id")
				st["auth_mode"] = "oauth"
				if strings.TrimSpace(td.Email) != "" {
					st["claude_email"] = td.Email
				} else {
					delete(st, "claude_email")
				}
				channel.SetSetting(st)
				if channel.GetBaseURL() == "" {
					base := "https://claude.ai"
					channel.BaseURL = &base
				}
			} else {
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "请选择 Claude 授权方式后完成登录授权"})
				return
			}
		}
	}

	keys := strings.Split(channel.Key, "\n")
	if channel.Type == common.ChannelTypeVertexAi {
		if channel.Other == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "部署地区不能为空",
			})
			return
		} else {
			if common.IsJsonStr(channel.Other) {
				// must have default
				regionMap := common.StrToMap(channel.Other)
				if regionMap["default"] == nil {
					c.JSON(http.StatusOK, gin.H{
						"success": false,
						"message": "部署地区必须包含default字段",
					})
					return
				}
			}
		}
		keys = []string{channel.Key}
	}

	// Codex OAuth 创建：仅创建单条（避免多 key 拆分，并确保能拿到 channel_id 做缓存绑定）
	if channel.Type == common.ChannelTypeCodex && codexSessionToken != nil {
		// Validate the length of the model name
		models := strings.Split(channel.Models, ",")
		for _, model := range models {
			if len(model) > 255 {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": fmt.Sprintf("模型名称过长: %s", model),
				})
				return
			}
		}
		if err := channel.Insert(); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		_ = service.CodexCacheChannelToken(channel.Id, codexSessionToken)
		// session_id 由前端随 setting 传入，这里不再保留
		if codexSessionID != "" {
			service.CodexDeleteOAuthSession(codexSessionID)
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
		return
	}
	// Claude OAuth 创建：仅创建单条（避免多 key 拆分，并确保能拿到 channel_id 做缓存绑定）
	if channel.Type == common.ChannelTypeClaudeCode && claudeSessionToken != nil {
		models := strings.Split(channel.Models, ",")
		for _, model := range models {
			if len(model) > 255 {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": fmt.Sprintf("模型名称过长: %s", model),
				})
				return
			}
		}
		if err := channel.Insert(); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		_ = service.ClaudeCacheChannelToken(channel.Id, claudeSessionToken)
		if claudeSessionID != "" {
			service.ClaudeDeleteOAuthSession(claudeSessionID)
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
		return
	}

	channels := make([]model.Channel, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		localChannel := channel
		localChannel.Key = key
		// Validate the length of the model name
		models := strings.Split(localChannel.Models, ",")
		for _, model := range models {
			if len(model) > 255 {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": fmt.Sprintf("模型名称过长: %s", model),
				})
				return
			}
		}
		channels = append(channels, localChannel)
	}
	err = model.BatchInsertChannels(channels)
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

func DeleteChannel(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	channel := model.Channel{Id: id}
	err := channel.Delete()
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

func DeleteDisabledChannel(c *gin.Context) {
	rows, err := model.DeleteDisabledChannel()
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
		"data":    rows,
	})
	return
}

type ChannelTag struct {
	Tag          string  `json:"tag"`
	NewTag       *string `json:"new_tag"`
	Priority     *int64  `json:"priority"`
	Weight       *uint   `json:"weight"`
	ModelMapping *string `json:"model_mapping"`
	Models       *string `json:"models"`
	Groups       *string `json:"groups"`
}

func DisableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil || channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	err = model.DisableChannelByTag(channelTag.Tag)
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

func EnableTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil || channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	err = model.EnableChannelByTag(channelTag.Tag)
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

func EditTagChannels(c *gin.Context) {
	channelTag := ChannelTag{}
	err := c.ShouldBindJSON(&channelTag)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	if channelTag.Tag == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "tag不能为空",
		})
		return
	}
	err = model.EditChannelByTag(channelTag.Tag, channelTag.NewTag, channelTag.ModelMapping, channelTag.Models, channelTag.Groups, channelTag.Priority, channelTag.Weight)
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

type ChannelBatch struct {
	Ids []int   `json:"ids"`
	Tag *string `json:"tag"`
}

func DeleteChannelBatch(c *gin.Context) {
	channelBatch := ChannelBatch{}
	err := c.ShouldBindJSON(&channelBatch)
	if err != nil || len(channelBatch.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	err = model.BatchDeleteChannels(channelBatch.Ids)
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
		"data":    len(channelBatch.Ids),
	})
	return
}

func UpdateChannel(c *gin.Context) {
	channel := model.Channel{}
	err := c.ShouldBindJSON(&channel)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	if channel.Type == common.ChannelTypeVertexAi {
		if channel.Other == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "部署地区不能为空",
			})
			return
		} else {
			if common.IsJsonStr(channel.Other) {
				// must have default
				regionMap := common.StrToMap(channel.Other)
				if regionMap["default"] == nil {
					c.JSON(http.StatusOK, gin.H{
						"success": false,
						"message": "部署地区必须包含default字段",
					})
					return
				}
			}
		}
	}
	err = channel.Update()
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
		"data":    channel,
	})
	return
}

func FetchModels(c *gin.Context) {
	var req struct {
		BaseURL  string `json:"base_url"`
		Type     int    `json:"type"`
		Key      string `json:"key"`
		ProxyURL string `json:"proxy_url"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	baseURL := req.BaseURL
	if baseURL == "" {
		baseURL = common.ChannelBaseURLs[req.Type]
	}

	client := service.GetHttpClient()
	if req.ProxyURL != "" {
		client = service.GetHttpClientWithProxy(req.ProxyURL)
	}
	url := fmt.Sprintf("%s/v1/models", baseURL)

	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// remove line breaks and extra spaces.
	key := strings.TrimSpace(req.Key)
	// If the key contains a line break, only take the first part.
	key = strings.Split(key, "\n")[0]
	request.Header.Set("Authorization", "Bearer "+key)

	response, err := client.Do(request)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	//check status code
	if response.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to fetch models",
		})
		return
	}
	defer response.Body.Close()

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	var models []string
	for _, model := range result.Data {
		models = append(models, model.ID)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    models,
	})
}

func BatchSetChannelTag(c *gin.Context) {
	channelBatch := ChannelBatch{}
	err := c.ShouldBindJSON(&channelBatch)
	if err != nil || len(channelBatch.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	err = model.BatchSetChannelTag(channelBatch.Ids, channelBatch.Tag)
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
		"data":    len(channelBatch.Ids),
	})
	return
}
