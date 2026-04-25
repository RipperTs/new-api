package model

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"one-api/common"
)

const defaultModelHealthSlowThreshold = 10000

type ModelHealthStat struct {
	Count     int     `json:"count"`
	AvgFrt    float64 `json:"avg_frt"`
	P50Frt    float64 `json:"p50_frt"`
	P90Frt    float64 `json:"p90_frt"`
	P95Frt    float64 `json:"p95_frt"`
	MaxFrt    float64 `json:"max_frt"`
	SlowCount int     `json:"slow_count"`
	SlowRate  float64 `json:"slow_rate"`
}

type ModelHealthRecord struct {
	ModelName   string  `json:"model_name"`
	ChannelId   int     `json:"channel"`
	ChannelName string  `json:"channel_name"`
	Count       int     `json:"count"`
	AvgFrt      float64 `json:"avg_frt"`
	P50Frt      float64 `json:"p50_frt"`
	P90Frt      float64 `json:"p90_frt"`
	P95Frt      float64 `json:"p95_frt"`
	MaxFrt      float64 `json:"max_frt"`
	SlowCount   int     `json:"slow_count"`
	SlowRate    float64 `json:"slow_rate"`
}

type ModelHealthTrend struct {
	ModelName string  `json:"model_name"`
	CreatedAt int64   `json:"created_at"`
	Count     int     `json:"count"`
	AvgFrt    float64 `json:"avg_frt"`
	P95Frt    float64 `json:"p95_frt"`
	MaxFrt    float64 `json:"max_frt"`
	SlowRate  float64 `json:"slow_rate"`
}

type ModelHealthData struct {
	Summary  ModelHealthStat     `json:"summary"`
	Models   []ModelHealthRecord `json:"models"`
	Channels []ModelHealthRecord `json:"channels"`
	Trends   []ModelHealthTrend  `json:"trends"`
}

type modelHealthLogRow struct {
	ModelName   string `json:"model_name"`
	ChannelId   int    `json:"channel"`
	ChannelName string `json:"channel_name"`
	CreatedAt   int64  `json:"created_at"`
	Other       string `json:"other"`
}

type modelHealthBucket struct {
	ModelName   string
	ChannelId   int
	ChannelName string
	CreatedAt   int64
	Values      []float64
}

func GetModelHealthData(startTimestamp int64, endTimestamp int64, modelName string, channel int, group string) (*ModelHealthData, error) {
	var rows []modelHealthLogRow
	tx := LOG_DB.Table("logs").
		Select("logs.model_name, logs.channel_id, channels.name as channel_name, logs.created_at, logs.other").
		Joins("LEFT JOIN channels ON logs.channel_id = channels.id").
		Where("logs.type = ? and logs.is_stream = ?", LogTypeConsume, true).
		Where("logs.other LIKE ?", "%\"frt\"%")

	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("logs.model_name like ?", modelName)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+groupCol+" = ?", group)
	}
	if err := tx.Scan(&rows).Error; err != nil {
		return nil, err
	}

	modelBuckets := make(map[string]*modelHealthBucket)
	channelBuckets := make(map[string]*modelHealthBucket)
	trendBuckets := make(map[string]*modelHealthBucket)
	allValues := make([]float64, 0, len(rows))
	bucketSeconds := getModelHealthBucketSeconds(startTimestamp, endTimestamp)

	for _, row := range rows {
		frt, ok := getFirstResponseTime(row.Other)
		if !ok || frt <= 0 {
			continue
		}

		allValues = append(allValues, frt)
		modelBucket := getOrCreateHealthBucket(modelBuckets, row.ModelName, 0, "", 0)
		modelBucket.Values = append(modelBucket.Values, frt)

		channelKey := fmt.Sprintf("%s-%d", row.ModelName, row.ChannelId)
		channelBucket := getOrCreateHealthBucket(channelBuckets, channelKey, row.ChannelId, row.ChannelName, 0)
		channelBucket.ModelName = row.ModelName
		channelBucket.Values = append(channelBucket.Values, frt)

		trendCreatedAt := row.CreatedAt - row.CreatedAt%bucketSeconds
		trendKey := fmt.Sprintf("%s-%d", row.ModelName, trendCreatedAt)
		trendBucket := getOrCreateHealthBucket(trendBuckets, trendKey, 0, "", trendCreatedAt)
		trendBucket.ModelName = row.ModelName
		trendBucket.Values = append(trendBucket.Values, frt)
	}

	models := buildHealthRecords(modelBuckets)
	channels := buildHealthRecords(channelBuckets)
	trends := buildHealthTrends(trendBuckets)

	sort.Slice(models, func(i, j int) bool {
		return models[i].P95Frt > models[j].P95Frt
	})
	sort.Slice(channels, func(i, j int) bool {
		return channels[i].P95Frt > channels[j].P95Frt
	})
	sort.Slice(trends, func(i, j int) bool {
		if trends[i].CreatedAt == trends[j].CreatedAt {
			return trends[i].ModelName < trends[j].ModelName
		}
		return trends[i].CreatedAt < trends[j].CreatedAt
	})

	return &ModelHealthData{
		Summary:  calculateHealthStat(allValues),
		Models:   models,
		Channels: channels,
		Trends:   trends,
	}, nil
}

func getOrCreateHealthBucket(buckets map[string]*modelHealthBucket, key string, channelId int, channelName string, createdAt int64) *modelHealthBucket {
	bucket, ok := buckets[key]
	if ok {
		return bucket
	}
	bucket = &modelHealthBucket{
		ModelName:   key,
		ChannelId:   channelId,
		ChannelName: channelName,
		CreatedAt:   createdAt,
		Values:      make([]float64, 0),
	}
	buckets[key] = bucket
	return bucket
}

func buildHealthRecords(buckets map[string]*modelHealthBucket) []ModelHealthRecord {
	records := make([]ModelHealthRecord, 0, len(buckets))
	for _, bucket := range buckets {
		stat := calculateHealthStat(bucket.Values)
		records = append(records, ModelHealthRecord{
			ModelName:   bucket.ModelName,
			ChannelId:   bucket.ChannelId,
			ChannelName: bucket.ChannelName,
			Count:       stat.Count,
			AvgFrt:      stat.AvgFrt,
			P50Frt:      stat.P50Frt,
			P90Frt:      stat.P90Frt,
			P95Frt:      stat.P95Frt,
			MaxFrt:      stat.MaxFrt,
			SlowCount:   stat.SlowCount,
			SlowRate:    stat.SlowRate,
		})
	}
	return records
}

func buildHealthTrends(buckets map[string]*modelHealthBucket) []ModelHealthTrend {
	trends := make([]ModelHealthTrend, 0, len(buckets))
	for _, bucket := range buckets {
		stat := calculateHealthStat(bucket.Values)
		trends = append(trends, ModelHealthTrend{
			ModelName: bucket.ModelName,
			CreatedAt: bucket.CreatedAt,
			Count:     stat.Count,
			AvgFrt:    stat.AvgFrt,
			P95Frt:    stat.P95Frt,
			MaxFrt:    stat.MaxFrt,
			SlowRate:  stat.SlowRate,
		})
	}
	return trends
}

func calculateHealthStat(values []float64) ModelHealthStat {
	if len(values) == 0 {
		return ModelHealthStat{}
	}
	sortedValues := append([]float64(nil), values...)
	sort.Float64s(sortedValues)

	var total float64
	slowCount := 0
	for _, value := range sortedValues {
		total += value
		if value > defaultModelHealthSlowThreshold {
			slowCount++
		}
	}

	count := len(sortedValues)
	return ModelHealthStat{
		Count:     count,
		AvgFrt:    roundMilliseconds(total / float64(count)),
		P50Frt:    roundMilliseconds(percentile(sortedValues, 50)),
		P90Frt:    roundMilliseconds(percentile(sortedValues, 90)),
		P95Frt:    roundMilliseconds(percentile(sortedValues, 95)),
		MaxFrt:    roundMilliseconds(sortedValues[count-1]),
		SlowCount: slowCount,
		SlowRate:  roundRate(float64(slowCount) / float64(count)),
	}
}

func percentile(sortedValues []float64, p float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100*float64(len(sortedValues)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sortedValues) {
		rank = len(sortedValues) - 1
	}
	return sortedValues[rank]
}

func roundMilliseconds(value float64) float64 {
	return math.Round(value*10) / 10
}

func roundRate(value float64) float64 {
	return math.Round(value*10000) / 100
}

func getFirstResponseTime(other string) (float64, bool) {
	otherMap := common.StrToMap(other)
	if otherMap == nil {
		return 0, false
	}
	value, ok := otherMap["frt"]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		floatValue, err := strconv.ParseFloat(v, 64)
		return floatValue, err == nil
	default:
		return 0, false
	}
}

func getModelHealthBucketSeconds(startTimestamp int64, endTimestamp int64) int64 {
	if startTimestamp == 0 || endTimestamp == 0 || endTimestamp <= startTimestamp {
		return 3600
	}
	duration := endTimestamp - startTimestamp
	if duration <= 2*86400 {
		return 900
	}
	if duration <= 14*86400 {
		return 3600
	}
	if duration <= 90*86400 {
		return 86400
	}
	return 7 * 86400
}
