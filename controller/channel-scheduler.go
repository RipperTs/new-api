package controller

import (
	"fmt"
	"one-api/common"
	"one-api/model"
	"one-api/service"
	"sync"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
)

var (
	channelCheckLock    sync.Mutex
	channelCheckRunning bool = false
)

// CheckDisabledChannels 检查已禁用的渠道是否恢复正常
func checkDisabledChannels() error {
	channelCheckLock.Lock()
	if channelCheckRunning {
		channelCheckLock.Unlock()
		return fmt.Errorf("渠道检查任务已在运行中")
	}
	channelCheckRunning = true
	channelCheckLock.Unlock()

	// 只获取自动禁用的渠道
	channels, err := model.GetChannelsByStatus(common.ChannelStatusAutoDisabled)
	if err != nil {
		channelCheckLock.Lock()
		channelCheckRunning = false
		channelCheckLock.Unlock()
		return err
	}

	if len(channels) == 0 {
		channelCheckLock.Lock()
		channelCheckRunning = false
		channelCheckLock.Unlock()
		common.SysLog("没有需要检查的已禁用渠道")
		return nil
	}

	common.SysLog(fmt.Sprintf("开始检查 %d 个已禁用渠道", len(channels)))

	gopool.Go(func() {
		defer func() {
			channelCheckLock.Lock()
			channelCheckRunning = false
			channelCheckLock.Unlock()
			common.SysLog("已禁用渠道检查任务完成")
		}()

		enabledCount := 0
		for _, channel := range channels {
			common.SysLog(fmt.Sprintf("检查渠道 #%d (%s)", channel.Id, channel.Name))

			err, openaiWithStatusErr := testChannel(channel, "")

			// 如果测试成功且满足启用条件，则启用渠道
			if service.ShouldEnableChannel(err, openaiWithStatusErr, channel.Status) {
				service.EnableChannel(channel.Id, channel.Name)
				enabledCount++
				common.SysLog(fmt.Sprintf("渠道 #%d (%s) 已自动启用", channel.Id, channel.Name))
			} else if err != nil {
				common.SysLog(fmt.Sprintf("渠道 #%d (%s) 仍然异常: %s", channel.Id, channel.Name, err.Error()))
			}

			// 间隔请求，避免过于频繁
			time.Sleep(common.RequestInterval)
		}

		if enabledCount > 0 {
			common.SysLog(fmt.Sprintf("本次检查共自动启用 %d 个渠道", enabledCount))
		}
	})

	return nil
}

// StartChannelScheduler 启动渠道检查定时任务
func StartChannelScheduler() {
	if !common.AutomaticEnableChannelEnabled {
		common.SysLog("自动启用渠道功能未开启，跳过启动渠道检查定时任务")
		return
	}

	common.SysLog("启动渠道检查定时任务，每30分钟执行一次")

	go func() {
		// 启动后延迟5分钟再开始第一次检查，避免系统启动时的干扰
		time.Sleep(5 * time.Minute)

		for {
			if common.AutomaticEnableChannelEnabled {
				common.SysLog("开始定时检查已禁用渠道")
				err := checkDisabledChannels()
				if err != nil {
					common.SysError(fmt.Sprintf("渠道检查任务执行失败: %s", err.Error()))
				}
			} else {
				common.SysLog("自动启用渠道功能已关闭，跳过本次检查")
			}

			// 每30分钟执行一次
			time.Sleep(30 * time.Minute)
		}
	}()
}
