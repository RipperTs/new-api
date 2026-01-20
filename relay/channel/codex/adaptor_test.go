package codex

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"one-api/dto"
	relaycommon "one-api/relay/common"
)

func TestConvertRequest_FiltersOptionalParamsForAPIKeyMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = &http.Request{Header: make(http.Header)}

	temp := 0.7
	msg := dto.Message{Role: "user"}
	msg.SetStringContent("hi")
	req := &dto.GeneralOpenAIRequest{
		Model:       "gpt-5",
		Temperature: &temp,
		TopP:        0.9,
		Seed:        123,
		MaxTokens:   100,
		Messages:    []dto.Message{msg},
	}

	// /v1 upstream + API key 模式：应过滤 temperature/top_p/seed/max_output_tokens 等可选字段
	info := &relaycommon.RelayInfo{
		BaseUrl:        "https://example.com/v1",
		ChannelSetting: map[string]interface{}{"auth_mode": "api_key"},
	}

	a := &Adaptor{}
	outAny, err := a.ConvertRequest(c, info, req)
	if err != nil {
		t.Fatalf("ConvertRequest error: %v", err)
	}

	out, ok := outAny.(codexRequest)
	if !ok {
		t.Fatalf("unexpected type: %T", outAny)
	}
	if out.Temperature != nil {
		t.Fatalf("expected temperature removed for api_key mode, got %v", *out.Temperature)
	}
	if out.TopP != 0 {
		t.Fatalf("expected top_p removed for api_key mode, got %v", out.TopP)
	}
	if out.Seed != 0 {
		t.Fatalf("expected seed removed for api_key mode, got %v", out.Seed)
	}
	if out.MaxOutputTokens != 0 {
		t.Fatalf("expected max_output_tokens removed for api_key mode, got %v", out.MaxOutputTokens)
	}
}

func TestConvertRequest_AllowsOptionalParamsForOAuthV1Upstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = &http.Request{Header: make(http.Header)}

	temp := 0.2
	msg := dto.Message{Role: "user"}
	msg.SetStringContent("hi")
	req := &dto.GeneralOpenAIRequest{
		Model:       "gpt-5",
		Temperature: &temp,
		TopP:        0.8,
		Seed:        42,
		MaxTokens:   100,
		Messages:    []dto.Message{msg},
	}

	info := &relaycommon.RelayInfo{
		BaseUrl:        "https://example.com/v1",
		ChannelSetting: map[string]interface{}{"auth_mode": "oauth"},
	}

	a := &Adaptor{}
	outAny, err := a.ConvertRequest(c, info, req)
	if err != nil {
		t.Fatalf("ConvertRequest error: %v", err)
	}

	out, ok := outAny.(codexRequest)
	if !ok {
		t.Fatalf("unexpected type: %T", outAny)
	}
	if out.Temperature == nil {
		t.Fatalf("expected temperature kept for oauth mode")
	}
	if *out.Temperature != temp {
		t.Fatalf("expected temperature=%v, got %v", temp, *out.Temperature)
	}
	if out.TopP != req.TopP {
		t.Fatalf("expected top_p=%v, got %v", req.TopP, out.TopP)
	}
	if out.Seed != req.Seed {
		t.Fatalf("expected seed=%v, got %v", req.Seed, out.Seed)
	}
	// ConvertRequest 的 max_output_tokens 由 MaxCompletionTokens/MaxTokens 透传（仅 /v1 upstream）
	if out.MaxOutputTokens == 0 {
		t.Fatalf("expected max_output_tokens set for oauth v1 upstream")
	}
}
