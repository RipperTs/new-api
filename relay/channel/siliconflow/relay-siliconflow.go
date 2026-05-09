package siliconflow

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
)

func siliconflowRerankHandler(c *gin.Context, resp *http.Response) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return service.OpenAIErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}
	var siliconflowResp SFRerankResponse
	err = json.Unmarshal(responseBody, &siliconflowResp)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError), nil
	}
	usage := &dto.Usage{
		PromptTokens:     siliconflowResp.Meta.Tokens.InputTokens,
		CompletionTokens: siliconflowResp.Meta.Tokens.OutputTokens,
		TotalTokens:      siliconflowResp.Meta.Tokens.InputTokens + siliconflowResp.Meta.Tokens.OutputTokens,
	}
	rerankResp := &dto.RerankResponse{
		Results: siliconflowResp.Results,
		Usage:   *usage,
	}

	jsonResponse, err := json.Marshal(rerankResp)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "marshal_response_body_failed", http.StatusInternalServerError), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	return nil, usage
}

func siliconflowAudioTranscriptionHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return service.OpenAIErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
	for k, v := range resp.Header {
		if len(v) > 0 {
			c.Writer.Header().Set(k, v[0])
		}
	}
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err = io.Copy(c.Writer, resp.Body); err != nil {
		return service.OpenAIErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError), nil
	}
	err = resp.Body.Close()
	if err != nil {
		return service.OpenAIErrorWrapper(err, "close_response_body_failed", http.StatusInternalServerError), nil
	}

	return nil, &dto.Usage{
		PromptTokens: info.PromptTokens,
		TotalTokens:  info.PromptTokens,
	}
}
