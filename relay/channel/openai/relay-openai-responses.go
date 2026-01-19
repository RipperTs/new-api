package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"one-api/common"
	"one-api/constant"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

func OpenaiResponsesHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	if resp == nil || resp.Body == nil {
		return service.OpenAIErrorWrapper(fmt.Errorf("invalid response"), "invalid_response", http.StatusInternalServerError), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "read_response_failed", http.StatusInternalServerError), nil
	}

	var resource dto.ResponsesResponseResourceLite
	if err := json.Unmarshal(body, &resource); err == nil && resource.Model != "" {
		info.UpstreamModelName = resource.Model
	}

	c.Data(http.StatusOK, "application/json", body)
	return nil, dto.ResponsesUsageToUsage(resource.Usage)
}

func OpenaiResponsesStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	if resp == nil || resp.Body == nil {
		return service.OpenAIErrorWrapper(fmt.Errorf("invalid response"), "invalid_response", http.StatusInternalServerError), nil
	}
	defer resp.Body.Close()

	var (
		usage    *dto.Usage
		doneSent bool
		mu       sync.Mutex
	)

	scanner := bufio.NewScanner(resp.Body)
	// Responses streaming 可能包含较大的 JSON（例如图片分片），需要提升 buffer 上限
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	scanner.Split(bufio.ScanLines)

	service.SetEventStreamHeaders(c)
	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second
	ticker := time.NewTicker(streamingTimeout)
	defer ticker.Stop()

	stopChan := make(chan bool)
	defer close(stopChan)

	gopool.Go(func() {
		for scanner.Scan() {
			info.SetFirstResponseTime()
			ticker.Reset(streamingTimeout)

			line := scanner.Text()
			if len(line) == 0 {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				service.Done(c)
				mu.Lock()
				doneSent = true
				mu.Unlock()
				continue
			}

			_ = service.StringData(c, data)

			var ev dto.ResponsesStreamEventLite
			if err := json.Unmarshal(common.StringToByteSlice(data), &ev); err != nil {
				continue
			}
			if ev.Type == "response.completed" && ev.Response != nil {
				if ev.Response.Model != "" {
					info.UpstreamModelName = ev.Response.Model
				}
				mu.Lock()
				usage = dto.ResponsesUsageToUsage(ev.Response.Usage)
				mu.Unlock()
			}
		}
		common.SafeSendBool(stopChan, true)
	})

	select {
	case <-ticker.C:
		common.LogError(c, "streaming timeout")
	case <-stopChan:
	}

	mu.Lock()
	shouldSendDone := !doneSent
	finalUsage := usage
	mu.Unlock()

	if shouldSendDone {
		service.Done(c)
	}
	return nil, finalUsage
}
