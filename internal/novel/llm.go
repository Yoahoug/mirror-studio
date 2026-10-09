package novel

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// maxProviderResponseBytes bounds how much of an upstream response is read
// before parsing; providers answer with a single JSON body.
const maxProviderResponseBytes = 64 << 20

// Channel is the part of a generator LLM channel the novel domain consumes.
type Channel struct {
	BaseURL   string
	APIPath   string
	ModelName string
	Name      string
	APIKey    string
}

// ChannelResolver resolves the LLM channel for scene analysis. It is the novel
// domain's view of generator.Service.ResolveChannel: a nil channel with a nil
// error means "no active channel", which falls back to the environment
// configuration exactly like the reference does.
type ChannelResolver interface {
	ResolveChannel(ctx context.Context, code string) (*Channel, error)
}

type sceneLLMConfig struct {
	url        string
	apiKey     string
	modelName  string
	sourceName string
}

// resolveSceneLLMConfig mirrors services.resolve_scene_llm_config.
func (s *Service) resolveSceneLLMConfig(ctx context.Context, channelCode string) (sceneLLMConfig, error) {
	if s.generator != nil {
		channel, err := s.generator.ResolveChannel(ctx, channelCode)
		if err != nil {
			s.log.Warn("novelviz llm channel lookup failed", "channel_code", channelCode, "error", err)
		} else if channel != nil && channel.APIKey != "" {
			apiPath := channel.APIPath
			if apiPath == "" {
				apiPath = s.settings.textAPIPath
			}
			return sceneLLMConfig{
				url:        joinProviderURL(channel.BaseURL, apiPath),
				apiKey:     channel.APIKey,
				modelName:  channel.ModelName,
				sourceName: channel.Name,
			}, nil
		}
	}
	if s.settings.textAPIKey != "" {
		return sceneLLMConfig{
			url:        joinProviderURL(s.settings.apiBaseURL, s.settings.textAPIPath),
			apiKey:     s.settings.textAPIKey,
			modelName:  s.settings.textModel,
			sourceName: "NOVELVIZ_TEXT_API_KEY",
		}, nil
	}
	return sceneLLMConfig{}, &VisualizationError{Message: "未配置可用小说分析 LLM"}
}

// requestScenePlan mirrors services.request_scene_plan: it retries the whole
// request/parse cycle NOVELVIZ_TEXT_MAX_RETRIES times with a min(2*attempt, 6)
// second backoff and reports the last failure.
func (s *Service) requestScenePlan(ctx context.Context, novelText, channelCode, targetPlot string) ([]*pyObj, error) {
	config, err := s.resolveSceneLLMConfig(ctx, channelCode)
	if err != nil {
		return nil, err
	}
	targetPlot = strings.TrimSpace(targetPlot)
	maxScenes := s.settings.maxScenes
	payload := autoSceneLLMPayload(novelText, config.modelName, s.settings.maxScenes)
	if targetPlot != "" {
		maxScenes = 1
		payload = targetedSceneLLMPayload(novelText, config.modelName, targetPlot)
	}
	var lastError error
	for attempt := 1; attempt <= s.settings.textRetries; attempt++ {
		scenes, err := s.postScenePlan(ctx, config, payload, maxScenes)
		if err == nil {
			return scenes, nil
		}
		lastError = err
		s.log.Warn("小说场景分析失败", "attempt", attempt, "error", err)
		if attempt < s.settings.textRetries {
			sleepContext(ctx, time.Duration(minInt(2*attempt, 6))*time.Second)
		}
	}
	if lastError == nil {
		lastError = errors.New("unknown error")
	}
	return nil, &VisualizationError{Message: "AI 场景分析失败：" + lastError.Error()}
}

func (s *Service) postScenePlan(ctx context.Context, config sceneLLMConfig, payload *pyObj, maxScenes int) ([]*pyObj, error) {
	body, err := marshalPython(payload, false)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.settings.textTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, config.url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+config.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.textClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, firstRunes(string(raw), 500))
	}
	decoded, err := decodePyJSON(raw)
	if err != nil {
		return nil, err
	}
	root, ok := decoded.(*pyObj)
	if !ok {
		return nil, errors.New("响应不是 JSON 对象")
	}
	content, err := chatCompletionContent(root)
	if err != nil {
		return nil, err
	}
	scenes, err := parseSceneJSON(content, maxScenes)
	if err != nil {
		return nil, err
	}
	if len(scenes) == 0 {
		return nil, &VisualizationError{Message: "AI 没有返回可用场景"}
	}
	return scenes, nil
}

// chatCompletionContent reads choices[0].message.content, the shape both the
// reference and the generator domain expect from an OpenAI-compatible channel.
func chatCompletionContent(root *pyObj) (string, error) {
	choices, ok := root.Get("choices")
	if !ok {
		return "", errors.New("响应缺少 choices")
	}
	items, ok := choices.([]any)
	if !ok || len(items) == 0 {
		return "", errors.New("响应缺少 choices")
	}
	first, ok := items[0].(*pyObj)
	if !ok {
		return "", errors.New("响应缺少 choices")
	}
	message := first.Child("message")
	if message == nil {
		return "", errors.New("响应缺少 message")
	}
	content, ok := message.Get("content")
	if !ok {
		return "", errors.New("响应缺少 content")
	}
	text, _ := content.(string)
	return text, nil
}

// providerError carries the HTTP status of a failed provider call so the retry
// rule of the generator domain can be applied.
type providerError struct {
	Status  int
	Message string
}

func (e *providerError) Error() string { return e.Message }

// retryable mirrors generator._is_retryable_error: no HTTP status, 5xx, or one
// of 408/409/425/429.
func (e *providerError) retryable() bool {
	if e.Status == 0 || e.Status >= 500 {
		return true
	}
	switch e.Status {
	case 408, 409, 425, 429:
		return true
	}
	return false
}

func isRetryableProviderError(err error) bool {
	var provider *providerError
	if errors.As(err, &provider) {
		return provider.retryable()
	}
	return true
}

// postImageGeneration mirrors services._post_image_generation.
func (s *Service) postImageGeneration(ctx context.Context, prompt string) ([]byte, error) {
	url := joinProviderURL(s.settings.apiBaseURL, s.settings.imageAPIPath)
	payload := newPyObj().
		Set("model", s.settings.imageModel).
		Set("prompt", prompt).
		Set("size", s.settings.imageSize).
		Set("quality", s.settings.imageQuality).
		Set("response_format", "b64_json")
	body, err := marshalPython(payload, false)
	if err != nil {
		return nil, &providerError{Message: err.Error()}
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.settings.imageTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, &providerError{Message: err.Error()}
	}
	request.Header.Set("Authorization", "Bearer "+s.settings.imageAPIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.imageClient.Do(request)
	if err != nil {
		return nil, &providerError{Message: normalizeTransportError(err)}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes))
	if err != nil {
		return nil, &providerError{Status: response.StatusCode, Message: err.Error()}
	}
	if response.StatusCode >= 400 {
		return nil, &providerError{
			Status:  response.StatusCode,
			Message: fmt.Sprintf("图片接口失败 HTTP %d: %s", response.StatusCode, firstRunes(string(raw), 500)),
		}
	}
	decoded, err := decodePyJSON(raw)
	if err != nil {
		return nil, &providerError{Message: "图片接口返回格式异常"}
	}
	root, ok := decoded.(*pyObj)
	if !ok {
		return nil, &providerError{Message: "图片接口返回格式异常"}
	}
	content, err := imageB64(root)
	if err != nil {
		return nil, &providerError{Message: "图片接口返回格式异常"}
	}
	return content, nil
}

func imageB64(root *pyObj) ([]byte, error) {
	items, ok := root.Get("data")
	if !ok {
		return nil, errors.New("missing data")
	}
	list, ok := items.([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New("missing data")
	}
	first, ok := list[0].(*pyObj)
	if !ok {
		return nil, errors.New("missing data")
	}
	value, ok := first.Get("b64_json")
	if !ok {
		return nil, errors.New("missing b64_json")
	}
	encoded, ok := value.(string)
	if !ok {
		return nil, errors.New("missing b64_json")
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// normalizeTransportError keeps a timeout-looking failure recognisable without
// leaking the provider URL or key into the stored error message.
func normalizeTransportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "请求超时"
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return "请求超时"
	}
	return err.Error()
}

func newProviderClients() (*http.Client, *http.Client) {
	textTransport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
	}
	imageTransport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: textTransport}, &http.Client{Transport: imageTransport}
}

func sleepContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func firstRunes(value string, limit int) string { return truncateRunes(value, limit) }
