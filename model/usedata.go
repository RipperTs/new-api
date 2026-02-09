package model

import (
	"fmt"
	"gorm.io/gorm"
	"one-api/common"
	"sync"
	"time"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id               int    `json:"id"`
	UserID           int    `json:"user_id" gorm:"index"`
	Username         string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName        string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt        int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	TokenUsed        int    `json:"token_used" gorm:"default:0"`
	Count            int    `json:"count" gorm:"default:0"`
	Quota            int    `json:"quota" gorm:"default:0"`
	Group            string `json:"group" gorm:"type:varchar(64);index;default:'default'"`
	PromptTokens     int    `json:"prompt_tokens" gorm:"column:prompt_tokens;->;-:migration"`
	CompletionTokens int    `json:"completion_tokens" gorm:"column:completion_tokens;->;-:migration"`
}

func UpdateQuotaData() {
	// recover
	defer func() {
		if r := recover(); r != nil {
			common.SysLog(fmt.Sprintf("UpdateQuotaData panic: %s", r))
		}
	}()
	for {
		if common.DataExportEnabled {
			common.SysLog("正在更新数据看板数据...")
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

func logQuotaDataCache(userId int, username string, modelName string, group string, quota int, createdAt int64, tokenUsed int) {
	if group == "" {
		group = "default"
	}
	key := fmt.Sprintf("%d-%s-%s-%s-%d", userId, username, modelName, group, createdAt)
	quotaData, ok := CacheQuotaData[key]
	if ok {
		quotaData.Count += 1
		quotaData.Quota += quota
		quotaData.TokenUsed += tokenUsed
	} else {
		quotaData = &QuotaData{
			UserID:    userId,
			Username:  username,
			ModelName: modelName,
			Group:     group,
			CreatedAt: createdAt,
			Count:     1,
			Quota:     quota,
			TokenUsed: tokenUsed,
		}
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(userId int, username string, modelName string, group string, quota int, createdAt int64, tokenUsed int) {
	// 只精确到小时
	createdAt = createdAt - (createdAt % 3600)

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(userId, username, modelName, group, quota, createdAt, tokenUsed)
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	size := len(CacheQuotaData)
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for _, quotaData := range CacheQuotaData {
		quotaDataDB := &QuotaData{}
		DB.Table("quota_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ? and "+groupCol+" = ?",
			quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt, quotaData.Group).First(quotaDataDB)
		if quotaDataDB.Id > 0 {
			//quotaDataDB.Count += quotaData.Count
			//quotaDataDB.Quota += quotaData.Quota
			//DB.Table("quota_data").Save(quotaDataDB)
			increaseQuotaData(quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.Group, quotaData.Count, quotaData.Quota, quotaData.CreatedAt, quotaData.TokenUsed)
		} else {
			DB.Table("quota_data").Create(quotaData)
		}
	}
	CacheQuotaData = make(map[string]*QuotaData)
	common.SysLog(fmt.Sprintf("保存数据看板数据成功，共保存%d条数据", size))
}

func increaseQuotaData(userId int, username string, modelName string, group string, count int, quota int, createdAt int64, tokenUsed int) {
	err := DB.Table("quota_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ? and "+groupCol+" = ?",
		userId, username, modelName, createdAt, group).Updates(map[string]interface{}{
		"count":      gorm.Expr("count + ?", count),
		"quota":      gorm.Expr("quota + ?", quota),
		"token_used": gorm.Expr("token_used + ?", tokenUsed),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("increaseQuotaData error: %s", err))
	}
}

func getLogQuotaBucketExpr() string {
	if common.UsingPostgreSQL {
		return "(logs.created_at / 3600) * 3600"
	}
	if common.UsingMySQL {
		return "(logs.created_at DIV 3600) * 3600"
	}
	return "CAST(logs.created_at / 3600 AS INTEGER) * 3600"
}

func applyModelFilter(tx *gorm.DB, modelNames []string) *gorm.DB {
	if len(modelNames) > 0 {
		tx = tx.Where("model_name IN ?", modelNames)
	}
	return tx
}

func getQuotaDataByLogs(startTime int64, endTime int64, group string, tokenId int, modelNames []string, appendCondition func(tx *gorm.DB) *gorm.DB) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	bucketExpr := getLogQuotaBucketExpr()
	tx := LOG_DB.Table("logs").Select("model_name, count(*) as count, sum(quota) as quota, sum(prompt_tokens + completion_tokens) as token_used, sum(prompt_tokens) as prompt_tokens, sum(completion_tokens) as completion_tokens, "+bucketExpr+" as created_at").
		Where("type = ? and created_at >= ? and created_at <= ?", LogTypeConsume, startTime, endTime)
	if appendCondition != nil {
		tx = appendCondition(tx)
	}
	if group != "" {
		tx = tx.Where(groupCol+" = ?", group)
	}
	if tokenId > 0 {
		tx = tx.Where("token_id = ?", tokenId)
	}
	tx = applyModelFilter(tx, modelNames)
	err = tx.Group("model_name, " + bucketExpr).Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64, group string, tokenId int, usePromptCompletion bool, modelNames []string) (quotaData []*QuotaData, err error) {
	if tokenId > 0 || usePromptCompletion {
		return getQuotaDataByLogs(startTime, endTime, group, tokenId, modelNames, func(tx *gorm.DB) *gorm.DB {
			return tx.Where("username = ?", username)
		})
	}
	var quotaDatas []*QuotaData
	tx := DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, 0 as prompt_tokens, 0 as completion_tokens, created_at").
		Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime)
	if group != "" {
		tx = tx.Where(groupCol+" = ?", group)
	}
	tx = applyModelFilter(tx, modelNames)
	err = tx.Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64, group string, tokenId int, usePromptCompletion bool, modelNames []string) (quotaData []*QuotaData, err error) {
	if tokenId > 0 || usePromptCompletion {
		return getQuotaDataByLogs(startTime, endTime, group, tokenId, modelNames, func(tx *gorm.DB) *gorm.DB {
			return tx.Where("user_id = ?", userId)
		})
	}
	var quotaDatas []*QuotaData
	tx := DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, 0 as prompt_tokens, 0 as completion_tokens, created_at").
		Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime)
	if group != "" {
		tx = tx.Where(groupCol+" = ?", group)
	}
	tx = applyModelFilter(tx, modelNames)
	err = tx.Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string, group string, tokenId int, usePromptCompletion bool, modelNames []string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime, group, tokenId, usePromptCompletion, modelNames)
	}
	if tokenId > 0 || usePromptCompletion {
		return getQuotaDataByLogs(startTime, endTime, group, tokenId, modelNames, nil)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	tx := DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, 0 as prompt_tokens, 0 as completion_tokens, created_at").
		Where("created_at >= ? and created_at <= ?", startTime, endTime)
	if group != "" {
		tx = tx.Where(groupCol+" = ?", group)
	}
	tx = applyModelFilter(tx, modelNames)
	err = tx.Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}
