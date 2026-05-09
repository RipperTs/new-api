package siliconflow

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"one-api/dto"
	"one-api/relay/channel"
	"one-api/relay/channel/openai"
	relaycommon "one-api/relay/common"
	"one-api/relay/constant"
	"strings"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if info.RelayMode != constant.RelayModeAudioTranscription {
		return nil, errors.New("unsupported audio relay mode")
	}
	if c.Query("model") != "" || c.GetHeader("X-Model") != "" {
		return buildStreamingAudioRequest(c, request)
	}

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		return nil, err
	}
	if err := writer.WriteField("model", request.Model); err != nil {
		return nil, err
	}
	formData := c.Request.PostForm
	for key, values := range formData {
		if key == "model" {
			continue
		}
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				return nil, err
			}
		}
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		return nil, errors.New("file is required")
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		return nil, errors.New("create form file failed")
	}
	if _, err = io.Copy(part, file); err != nil {
		return nil, errors.New("copy file failed")
	}

	if err = writer.Close(); err != nil {
		return nil, err
	}
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return &requestBody, nil
}

func buildStreamingAudioRequest(c *gin.Context, request dto.AudioRequest) (io.Reader, error) {
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return nil, err
	}

	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	go func() {
		closeErr := streamAudioMultipart(reader, writer, request)
		if closeErr != nil {
			_ = pipeWriter.CloseWithError(closeErr)
			return
		}
		_ = pipeWriter.Close()
	}()

	return pipeReader, nil
}

func streamAudioMultipart(reader *multipart.Reader, writer *multipart.Writer, request dto.AudioRequest) error {
	if err := writer.WriteField("model", request.Model); err != nil {
		return err
	}

	hasResponseFormat := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if part.FormName() == "" || part.FormName() == "model" {
			_ = part.Close()
			continue
		}
		if part.FormName() == "response_format" {
			hasResponseFormat = true
		}

		target, err := createPart(writer, part)
		if err != nil {
			_ = part.Close()
			return err
		}
		if _, err = io.Copy(target, part); err != nil {
			_ = part.Close()
			return err
		}
		if err = part.Close(); err != nil {
			return err
		}
	}

	if !hasResponseFormat && request.ResponseFormat != "" {
		if err := writer.WriteField("response_format", request.ResponseFormat); err != nil {
			return err
		}
	}

	return writer.Close()
}

func createPart(writer *multipart.Writer, part *multipart.Part) (io.Writer, error) {
	if part.FileName() == "" {
		return writer.CreateFormField(part.FormName())
	}

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(part.FormName()), escapeQuotes(part.FileName())))
	contentType := part.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	return writer.CreatePart(header)
}

func escapeQuotes(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(value)
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode == constant.RelayModeRerank {
		return fmt.Sprintf("%s/v1/rerank", info.BaseUrl), nil
	} else if info.RelayMode == constant.RelayModeEmbeddings {
		return fmt.Sprintf("%s/v1/embeddings", info.BaseUrl), nil
	} else if info.RelayMode == constant.RelayModeChatCompletions {
		return fmt.Sprintf("%s/v1/chat/completions", info.BaseUrl), nil
	} else if info.RelayMode == constant.RelayModeAudioTranscription {
		return fmt.Sprintf("%s/v1/audio/transcriptions", info.BaseUrl), nil
	}
	return "", errors.New("invalid relay mode")
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
	return nil
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info.RelayMode == constant.RelayModeAudioTranscription {
		return channel.DoFormRequest(a, c, info, requestBody)
	}
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	switch info.RelayMode {
	case constant.RelayModeRerank:
		err, usage = siliconflowRerankHandler(c, resp)
	case constant.RelayModeChatCompletions:
		if info.IsStream {
			err, usage = openai.OaiStreamHandler(c, resp, info)
		} else {
			err, usage = openai.OpenaiHandler(c, resp, info.PromptTokens, info.UpstreamModelName, info.ThinkingEnabled)
		}
	case constant.RelayModeEmbeddings:
		err, usage = openai.OpenaiHandler(c, resp, info.PromptTokens, info.UpstreamModelName, info.ThinkingEnabled)
	case constant.RelayModeAudioTranscription:
		if c.Query("model") != "" || c.GetHeader("X-Model") != "" {
			err, usage = siliconflowAudioTranscriptionHandler(c, resp, info)
		} else {
			err, usage = openai.OpenaiSTTHandler(c, resp, info, "")
		}
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
