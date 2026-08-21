package model

import (
	"math"
	"strconv"
	"sync"
	"time"

	"one-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	channelAvailabilityBucketSeconds = int64(5 * 60)
	channelAvailabilityFlushInterval = 5 * time.Second
	channelAvailabilityRetention     = 8 * 24 * time.Hour
	channelAvailabilityShardCount    = 32
)

type ChannelAvailability struct {
	ChannelId    int   `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	BucketTime   int64 `json:"bucket_time" gorm:"primaryKey;autoIncrement:false;index"`
	RequestCount int64 `json:"request_count" gorm:"not null;default:0"`
	ErrorCount   int64 `json:"error_count" gorm:"not null;default:0"`
}

func (ChannelAvailability) TableName() string {
	return "channel_availability_stats"
}

type ChannelAvailabilityPoint struct {
	BucketTime   int64    `json:"bucket_time"`
	RequestCount int64    `json:"request_count"`
	ErrorCount   int64    `json:"error_count"`
	Availability *float64 `json:"availability"`
}

type ChannelAvailabilityStat struct {
	ChannelId    int                        `json:"channel_id"`
	RequestCount int64                      `json:"request_count"`
	ErrorCount   int64                      `json:"error_count"`
	Availability *float64                   `json:"availability"`
	Trend        []ChannelAvailabilityPoint `json:"trend"`
}

type channelAvailabilityRow struct {
	ChannelId    int   `json:"channel_id"`
	BucketTime   int64 `json:"bucket_time"`
	RequestCount int64 `json:"request_count"`
	ErrorCount   int64 `json:"error_count"`
}

type channelAvailabilityKey struct {
	ChannelId  int
	BucketTime int64
}

type channelAvailabilityShard struct {
	sync.Mutex
	stats map[channelAvailabilityKey]*ChannelAvailability
}

var (
	channelAvailabilityShards    [channelAvailabilityShardCount]channelAvailabilityShard
	channelAvailabilityFlushLock sync.Mutex
)

func init() {
	for i := range channelAvailabilityShards {
		channelAvailabilityShards[i].stats = make(map[channelAvailabilityKey]*ChannelAvailability)
	}
}

func RecordChannelRequest(channelId int, isError bool) {
	if channelId <= 0 {
		return
	}
	bucketTime := time.Now().Unix()
	bucketTime -= bucketTime % channelAvailabilityBucketSeconds
	key := channelAvailabilityKey{
		ChannelId:  channelId,
		BucketTime: bucketTime,
	}
	shard := &channelAvailabilityShards[channelId%channelAvailabilityShardCount]

	shard.Lock()
	stat, ok := shard.stats[key]
	if !ok {
		stat = &ChannelAvailability{
			ChannelId:  channelId,
			BucketTime: bucketTime,
		}
		shard.stats[key] = stat
	}
	stat.RequestCount++
	if isError {
		stat.ErrorCount++
	}
	shard.Unlock()
}

func StartChannelAvailabilityStats() {
	go func() {
		flushTicker := time.NewTicker(channelAvailabilityFlushInterval)
		cleanupTicker := time.NewTicker(24 * time.Hour)
		defer flushTicker.Stop()
		defer cleanupTicker.Stop()

		if common.IsMasterNode {
			deleteExpiredChannelAvailabilityStats()
		}

		for {
			select {
			case <-flushTicker.C:
				FlushChannelAvailabilityStats()
			case <-cleanupTicker.C:
				if common.IsMasterNode {
					deleteExpiredChannelAvailabilityStats()
				}
			}
		}
	}()
}

func FlushChannelAvailabilityStats() {
	channelAvailabilityFlushLock.Lock()
	defer channelAvailabilityFlushLock.Unlock()

	stats := drainChannelAvailabilityStats()
	if len(stats) == 0 {
		return
	}
	if err := saveChannelAvailabilityStats(stats); err != nil {
		restoreChannelAvailabilityStats(stats)
		common.SysError("failed to save channel availability stats: " + err.Error())
	}
}

func drainChannelAvailabilityStats() []ChannelAvailability {
	stats := make([]ChannelAvailability, 0)
	for i := range channelAvailabilityShards {
		shard := &channelAvailabilityShards[i]
		shard.Lock()
		for _, stat := range shard.stats {
			stats = append(stats, *stat)
		}
		shard.stats = make(map[channelAvailabilityKey]*ChannelAvailability)
		shard.Unlock()
	}
	return stats
}

func restoreChannelAvailabilityStats(stats []ChannelAvailability) {
	for i := range stats {
		stat := &stats[i]
		key := channelAvailabilityKey{
			ChannelId:  stat.ChannelId,
			BucketTime: stat.BucketTime,
		}
		shard := &channelAvailabilityShards[stat.ChannelId%channelAvailabilityShardCount]

		shard.Lock()
		current, ok := shard.stats[key]
		if !ok {
			shard.stats[key] = stat
		} else {
			current.RequestCount += stat.RequestCount
			current.ErrorCount += stat.ErrorCount
		}
		shard.Unlock()
	}
}

func saveChannelAvailabilityStats(stats []ChannelAvailability) error {
	requestCountExpr := gorm.Expr("channel_availability_stats.request_count + excluded.request_count")
	errorCountExpr := gorm.Expr("channel_availability_stats.error_count + excluded.error_count")
	if DB.Dialector.Name() == "mysql" {
		requestCountExpr = gorm.Expr("request_count + VALUES(request_count)")
		errorCountExpr = gorm.Expr("error_count + VALUES(error_count)")
	}

	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "channel_id"},
			{Name: "bucket_time"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"request_count": requestCountExpr,
			"error_count":   errorCountExpr,
		}),
	}).CreateInBatches(&stats, 100).Error
}

func deleteExpiredChannelAvailabilityStats() {
	cutoff := time.Now().Add(-channelAvailabilityRetention).Unix()
	if err := DB.Where("bucket_time < ?", cutoff).Delete(&ChannelAvailability{}).Error; err != nil {
		common.SysError("failed to delete expired channel availability stats: " + err.Error())
	}
}

func GetChannelAvailabilityStats(channelIds []int, startTimestamp int64, endTimestamp int64, trendBucketSeconds int64) ([]ChannelAvailabilityStat, error) {
	if len(channelIds) == 0 {
		return []ChannelAvailabilityStat{}, nil
	}

	rows, err := getChannelAvailabilityRows(channelIds, startTimestamp, endTimestamp, trendBucketSeconds)
	if err != nil {
		return nil, err
	}

	firstBucket := startTimestamp - startTimestamp%trendBucketSeconds
	lastBucket := endTimestamp - endTimestamp%trendBucketSeconds
	pointCount := int((lastBucket-firstBucket)/trendBucketSeconds) + 1
	stats := make([]ChannelAvailabilityStat, len(channelIds))
	statIndexByChannel := make(map[int]int, len(channelIds))

	for i, channelId := range channelIds {
		trend := make([]ChannelAvailabilityPoint, pointCount)
		for pointIndex := range trend {
			trend[pointIndex].BucketTime = firstBucket + int64(pointIndex)*trendBucketSeconds
		}
		stats[i] = ChannelAvailabilityStat{
			ChannelId: channelId,
			Trend:     trend,
		}
		statIndexByChannel[channelId] = i
	}

	for _, row := range rows {
		statIndex, ok := statIndexByChannel[row.ChannelId]
		if !ok {
			continue
		}
		stat := &stats[statIndex]
		pointIndex := int((row.BucketTime - firstBucket) / trendBucketSeconds)
		if pointIndex < 0 || pointIndex >= len(stat.Trend) {
			continue
		}
		availability := calculateChannelAvailability(row.RequestCount, row.ErrorCount)
		stat.Trend[pointIndex].RequestCount = row.RequestCount
		stat.Trend[pointIndex].ErrorCount = row.ErrorCount
		stat.Trend[pointIndex].Availability = &availability
		stat.RequestCount += row.RequestCount
		stat.ErrorCount += row.ErrorCount
	}

	for i := range stats {
		if stats[i].RequestCount == 0 {
			continue
		}
		availability := calculateChannelAvailability(stats[i].RequestCount, stats[i].ErrorCount)
		stats[i].Availability = &availability
	}

	return stats, nil
}

func getChannelAvailabilityRows(channelIds []int, startTimestamp int64, endTimestamp int64, trendBucketSeconds int64) ([]channelAvailabilityRow, error) {
	bucketExpression := getChannelAvailabilityBucketExpression(trendBucketSeconds)
	rows := make([]channelAvailabilityRow, 0)
	const queryBatchSize = 200

	for start := 0; start < len(channelIds); start += queryBatchSize {
		end := start + queryBatchSize
		if end > len(channelIds) {
			end = len(channelIds)
		}
		var batchRows []channelAvailabilityRow
		err := DB.Table("channel_availability_stats").
			Select("channel_id, "+bucketExpression+" AS bucket_time, SUM(request_count) AS request_count, SUM(error_count) AS error_count").
			Where("channel_id IN ? AND bucket_time >= ? AND bucket_time <= ?", channelIds[start:end], startTimestamp, endTimestamp).
			Group("channel_id, " + bucketExpression).
			Scan(&batchRows).Error
		if err != nil {
			return nil, err
		}
		rows = append(rows, batchRows...)
	}

	return rows, nil
}

func getChannelAvailabilityBucketExpression(bucketSeconds int64) string {
	bucket := strconv.FormatInt(bucketSeconds, 10)
	if DB.Dialector.Name() == "mysql" {
		return "(bucket_time DIV " + bucket + ") * " + bucket
	}
	if DB.Dialector.Name() == "sqlite" {
		return "CAST(bucket_time / " + bucket + " AS INTEGER) * " + bucket
	}
	return "(bucket_time / " + bucket + ") * " + bucket
}

func calculateChannelAvailability(requestCount int64, errorCount int64) float64 {
	rate := float64(requestCount-errorCount) / float64(requestCount) * 100
	return math.Round(rate*100) / 100
}
