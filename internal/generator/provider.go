package generator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strconv"
	"strings"
	"time"

	"mirror-studio/internal/imageutil"
)

// Provider request/response plumbing. The wire payloads are byte-compatible
// with apps/generator/providers.py: tools/diff/fake_upstream.py records every
// request the two implementations send so the field sets can be compared.

type providerImage struct {
	content   []byte
	remoteURL string
	mimeType  string
}

type providerResult struct {
	requestID   string
	rawResponse any
	images      []providerImage
}

// providerRequestError mirrors the reference's ProviderRequestError: the
// upstream answered with an error status. A nil status means "no HTTP status at
// all", which the retry policy treats as retryable.
type providerRequestError struct {
	message    string
	raw        any
	requestID  string
	statusCode *int
}

func (e *providerRequestError) Error() string { return e.message }

func (e *providerRequestError) retryable() bool {
	if e.statusCode == nil {
		return true
	}
	if *e.statusCode >= 500 {
		return true
	}
	switch *e.statusCode {
	case 408, 409, 425, 429:
		return true
	}
	return false
}

// isRetryableError mirrors tasks.py `_is_retryable_error`: an upstream status
// that is missing, >= 500, or one of the transient 4xx codes; any transport
// error.
func isRetryableError(err error) bool {
	switch typed := err.(type) {
	case *providerRequestError:
		return typed.retryable()
	case *transportFailure, *timeoutError:
		return true
	}
	return false
}

// transportFailure marks a request that never produced an HTTP response. The
// reference retries requests.RequestException and TimeoutError.
type transportFailure struct {
	cause error
}

func (e *transportFailure) Error() string { return e.cause.Error() }
func (e *transportFailure) Unwrap() error { return e.cause }

type timeoutError struct{ cause error }

func (e *timeoutError) Error() string { return e.cause.Error() }
func (e *timeoutError) Unwrap() error { return e.cause }

func wrapTransportError(err error) error {
	if err == nil {
		return nil
	}
	if err == context.DeadlineExceeded {
		return &timeoutError{cause: err}
	}
	if typed, ok := err.(interface{ Timeout() bool }); ok && typed.Timeout() {
		return &timeoutError{cause: err}
	}
	return &transportFailure{cause: err}
}

type providerAdapter interface {
	generate(ctx context.Context, task *Task, channel *Channel, apiKey string) (*providerResult, error)
	name() string
}

func (s *Service) adapterFor(task *Task) providerAdapter {
	if s.cfg.MockProviderEnabled {
		return mockAdapter{}
	}
	if task.Channel != nil && task.Channel.ProviderVendor == VendorSiliconFlow {
		return siliconFlowAdapter{service: s}
	}
	return openAIAdapter{service: s}
}

// ── mock ──

type mockAdapter struct{}

func (a mockAdapter) name() string { return "MockImageProviderAdapter" }

// generate draws a deterministic PNG derived from the prompt hash: a solid
// background plus a rectangular motif at the requested size. Only the size and
// the MIME type are contractual; the reference rendered text with Pillow, which
// 0.0.1 deliberately does not port (review P2-8).
func (a mockAdapter) generate(ctx context.Context, task *Task, channel *Channel, apiKey string) (*providerResult, error) {
	width, height, err := parseSizeParts(task.SizeCode)
	if err != nil {
		return nil, err
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(task.Prompt))
	seed := hasher.Sum32()
	background := color.RGBA{
		R: uint8(20 + seed%60),
		G: uint8(32 + seed>>8%60),
		B: uint8(59 + seed>>16%60),
		A: 255,
	}
	accent := color.RGBA{
		R: uint8(80 + seed>>24%160),
		G: uint8(107 + seed%140),
		B: 255,
		A: 255,
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{background}, image.Point{}, draw.Src)

	insetX := maxInt(1, width/12)
	insetY := maxInt(1, height/12)
	motif := image.Rect(insetX, insetY, width-insetX, height-insetY)
	if motif.Dx() > 0 && motif.Dy() > 0 {
		draw.Draw(canvas, motif, &image.Uniform{accent}, image.Point{}, draw.Src)
		inner := image.Rect(
			motif.Min.X+motif.Dx()/6, motif.Min.Y+motif.Dy()/6,
			motif.Max.X-motif.Dx()/6, motif.Max.Y-motif.Dy()/6,
		)
		if inner.Dx() > 0 && inner.Dy() > 0 {
			draw.Draw(canvas, inner, &image.Uniform{background}, image.Point{}, draw.Src)
		}
	}

	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		return nil, err
	}
	raw := newOrderedObject()
	raw.set("mock", true)
	raw.set("task_no", task.TaskNo)
	return &providerResult{
		requestID:   "mock-" + task.TaskNo,
		rawResponse: raw,
		images:      []providerImage{{mimeType: "image/png", content: buffer.Bytes()}},
	}, nil
}

// ── OpenAI-compatible ──

type openAIAdapter struct{ service *Service }

func (a openAIAdapter) name() string { return "OpenAICompatibleImageProviderAdapter" }

func joinURL(baseURL, apiPath string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(apiPath, "/")
}

func (a openAIAdapter) generate(ctx context.Context, task *Task, channel *Channel, apiKey string) (*providerResult, error) {
	if apiKey == "" {
		// ValueError in the reference: a configuration problem, never retried.
		return nil, fmt.Errorf("缺少 OpenAI 兼容渠道 API Key")
	}
	prompt := buildProviderPrompt(task)
	quality := task.QualityCode
	if quality == "" {
		quality = "auto"
	}
	size := task.SizeCode
	if size == "" {
		size = "1024x1024"
	}

	var responseData any
	requestID := ""
	if task.GenerationMode == ModeImageToImage && task.ReferenceAttached() {
		referenceContent, mimeType, err := a.service.loadTaskReferenceImage(ctx, task)
		if err != nil {
			return nil, err
		}
		referenceContent, mimeType, suffix, err := normalizeReferenceForEdit(referenceContent, mimeType)
		if err != nil {
			return nil, err
		}
		editURL := joinURL(channel.BaseURL, resolveOpenAIEditPath(channel))
		editTimeout := openAIEditTimeout(channel, a.service.cfg.OpenAIImageEditTimeout)
		fields := map[string]string{
			"model": channel.ModelName, "prompt": prompt, "size": size, "quality": quality,
		}
		fileName := referenceFileStem(task.ReferenceImageName) + suffix
		a.service.log.Info("发送 OpenAI 兼容图生图请求",
			"task_no", task.TaskNo, "url", editURL, "size", size, "quality", quality,
			"transport", "multipart-image", "timeout", editTimeout,
			"bytes", len(referenceContent), "mime", mimeType)
		responseData, requestID, err = a.postMultipart(ctx, editURL, apiKey, fields,
			fileName, referenceContent, mimeType, editTimeout)
		if err != nil {
			requestError, ok := err.(*providerRequestError)
			if !ok || requestError.statusCode == nil || !degradedEditStatus(*requestError.statusCode) {
				return nil, err
			}
			// Review P1-19: the multipart form was rejected; retry once as JSON
			// with the reference inlined as a data URL, and keep the original
			// error when that fails too.
			a.service.log.Warn("OpenAI 兼容图生图 multipart 被拒绝，回退 JSON image_url",
				"task_no", task.TaskNo, "status", *requestError.statusCode)
			fallback := newOrderedObject()
			fallback.set("model", fields["model"])
			fallback.set("prompt", fields["prompt"])
			fallback.set("size", fields["size"])
			fallback.set("quality", fields["quality"])
			image := newOrderedObject()
			image.set("image_url", encodeDataURL(referenceContent, mimeType))
			fallback.set("images", []any{image})
			responseData, requestID, err = a.postJSON(ctx, editURL, apiKey, fallback, editTimeout)
			if err != nil {
				return nil, err
			}
		}
	} else {
		payload := newOrderedObject()
		payload.set("model", channel.ModelName)
		payload.set("prompt", prompt)
		payload.set("size", size)
		payload.set("quality", quality)
		generationURL := joinURL(channel.BaseURL, apiPathOrDefault(channel.APIPath, "/v1/images/generations"))
		generationTimeout := openAIGenerationTimeout(channel, a.service.cfg.ProviderTimeoutSeconds)
		a.service.log.Info("发送 OpenAI 兼容文生图请求",
			"task_no", task.TaskNo, "url", generationURL, "size", size, "quality", quality)
		var err error
		responseData, requestID, err = a.postJSON(ctx, generationURL, apiKey, payload, generationTimeout)
		if err != nil {
			return nil, err
		}
	}

	images, err := parseOpenAIImages(responseData)
	if err != nil {
		return nil, err
	}
	return &providerResult{
		requestID:   fallbackRequestID(requestID, responseData, task.TaskNo),
		rawResponse: trimOpenAIImageResponse(responseData, a.service.log),
		images:      images,
	}, nil
}

func (a openAIAdapter) postJSON(ctx context.Context, url, apiKey string, payload any, timeoutSeconds int) (any, string, error) {
	body := []byte(encodeCompactJSON(payload))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	return a.do(request, timeoutSeconds)
}

func (a openAIAdapter) postMultipart(
	ctx context.Context,
	url, apiKey string,
	fields map[string]string,
	fileName string,
	content []byte,
	mimeType string,
	timeoutSeconds int,
) (any, string, error) {
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	for _, key := range []string{"model", "prompt", "size", "quality"} {
		if err := writer.WriteField(key, fields[key]); err != nil {
			return nil, "", err
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="`+escapeQuotes(fileName)+`"`)
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(content); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buffer.Bytes()))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+apiKey)
	return a.do(request, timeoutSeconds)
}

// escapeQuotes renders a header parameter the way requests does: only the
// quote and backslash characters need escaping.
func escapeQuotes(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "")
	return replacer.Replace(value)
}

func (a openAIAdapter) do(request *http.Request, timeoutSeconds int) (any, string, error) {
	client := &http.Client{Timeout: time.Duration(normalizeTimeout(timeoutSeconds)) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", wrapTransportError(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, "", wrapTransportError(err)
	}
	data, err := decodeOrderedJSON(string(raw))
	if err != nil {
		return nil, "", fmt.Errorf("OpenAI 兼容渠道返回了不可解析的响应：HTTP %d", response.StatusCode)
	}
	requestID := response.Header.Get("x-request-id")
	if response.StatusCode >= 400 {
		return nil, "", newProviderRequestError(data, response.StatusCode, requestID)
	}
	return data, requestID, nil
}

// newProviderRequestError reproduces providers.py message selection: when the
// error field is an object its message (or code) wins, otherwise the first
// non-empty provider message field, otherwise the default.
func newProviderRequestError(data any, statusCode int, requestID string) error {
	status := statusCode
	defaultMessage := fmt.Sprintf("OpenAI 兼容渠道请求失败：HTTP %d", statusCode)
	object, isObject := data.(*orderedObject)
	if !isObject {
		return &providerRequestError{message: defaultMessage, requestID: requestID, statusCode: &status}
	}
	if errorValue, ok := object.values["error"]; ok {
		if errorObject, ok := errorValue.(*orderedObject); ok {
			message := scalarText(firstNonEmpty(errorObject.values["message"], errorObject.values["code"]))
			if message == "" {
				message = defaultMessage
			}
			return &providerRequestError{
				message: message, raw: data, requestID: requestID, statusCode: &status,
			}
		}
	}
	return &providerRequestError{
		message:    extractProviderMessage(data, defaultMessage),
		raw:        data,
		requestID:  requestID,
		statusCode: &status,
	}
}

func extractProviderMessage(data any, fallback string) string {
	object, ok := data.(*orderedObject)
	if !ok {
		return fallback
	}
	for _, key := range []string{"message", "msg", "error", "errorMessage", "reason", "code"} {
		item, ok := object.values[key]
		if !ok || item == nil {
			continue
		}
		if text := scalarText(item); text != "" {
			return text
		}
	}
	return fallback
}

func scalarText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	case *orderedObject:
		if len(typed.keys) == 0 {
			return ""
		}
		return encodeDjangoJSON(typed)
	case []any:
		if len(typed) == 0 {
			return ""
		}
		return encodeDjangoJSON(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if value != nil && scalarText(value) != "" {
			return value
		}
	}
	return nil
}

// parseOpenAIImages prefers data[0].b64_json and falls back to a remote URL.
func parseOpenAIImages(responseData any) ([]providerImage, error) {
	object, _ := responseData.(*orderedObject)
	if object == nil {
		return nil, fmt.Errorf("OpenAI 兼容渠道未返回可用图片")
	}
	items, _ := object.values["data"].([]any)
	images := []providerImage{}
	for _, item := range items {
		entry, ok := item.(*orderedObject)
		if !ok {
			continue
		}
		if b64, ok := entry.values["b64_json"]; ok && scalarText(b64) != "" {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(scalarText(b64)))
			if err != nil {
				return nil, fmt.Errorf("OpenAI 兼容渠道返回的图片数据无法解码")
			}
			images = append(images, providerImage{mimeType: "image/png", content: decoded})
			continue
		}
		for _, key := range []string{"url", "image_url"} {
			if value, ok := entry.values[key]; ok && scalarText(value) != "" {
				images = append(images, providerImage{mimeType: "image/png", remoteURL: scalarText(value)})
				break
			}
		}
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("OpenAI 兼容渠道未返回可用图片")
	}
	return images, nil
}

func fallbackRequestID(requestID string, responseData any, taskNo string) string {
	if requestID != "" {
		return requestID
	}
	if object, ok := responseData.(*orderedObject); ok {
		for _, key := range []string{"created", "id"} {
			if value, ok := object.values[key]; ok && value != nil {
				return scalarText(value)
			}
		}
	}
	return taskNo
}

// trimOpenAIImageResponse replaces every base64 blob with a placeholder before
// the payload is stored or logged. Review P1-18: a malformed entry is reported
// and then skipped, never silently swallowed.
func trimOpenAIImageResponse(responseData any, log *slog.Logger) any {
	object, ok := responseData.(*orderedObject)
	if !ok {
		return responseData
	}
	items, ok := object.values["data"].([]any)
	if !ok {
		return responseData
	}
	trimmedItems := []any{}
	for _, item := range items {
		entry, ok := item.(*orderedObject)
		if !ok {
			if log != nil {
				log.Warn("上游响应的 data 条目不是对象，已跳过", "entry_type", fmt.Sprintf("%T", item))
			}
			continue
		}
		copied := newOrderedObject()
		for _, key := range entry.keys {
			value := entry.values[key]
			if key == "b64_json" {
				if text := scalarText(value); text != "" {
					copied.set(key, fmt.Sprintf("<omitted:%d chars>", len(text)))
					continue
				}
			}
			copied.set(key, value)
		}
		trimmedItems = append(trimmedItems, copied)
	}
	if len(trimmedItems) == 0 {
		return responseData
	}
	// `data` keeps its original position: the reference's dict copy leaves the
	// key order untouched, and the payload is stored verbatim, so reordering it
	// here would change the bytes the console shows.
	trimmed := newOrderedObject()
	for _, key := range object.keys {
		if key == "data" {
			trimmed.set(key, trimmedItems)
			continue
		}
		trimmed.set(key, object.values[key])
	}
	return trimmed
}

// ── SiliconFlow ──

type siliconFlowAdapter struct{ service *Service }

func (a siliconFlowAdapter) name() string { return "SiliconFlowImageProviderAdapter" }

func (a siliconFlowAdapter) generate(ctx context.Context, task *Task, channel *Channel, apiKey string) (*providerResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("缺少生图渠道 API Key")
	}
	prompt := buildProviderPrompt(task)
	size := task.SizeCode
	if size == "" {
		size = "1024x1024"
	}
	payload := newOrderedObject()
	payload.set("model", channel.ModelName)
	payload.set("prompt", prompt)
	if task.GenerationMode == ModeTextToImage {
		payload.set("image_size", size)
	}
	if task.GenerationMode == ModeImageToImage {
		if !task.ReferenceAttached() {
			return nil, fmt.Errorf("%s", imageToImageNeedsPhoto)
		}
		content, mimeType, err := a.service.loadTaskReferenceImage(ctx, task)
		if err != nil {
			return nil, err
		}
		normalized, normalizedMime, _, err := normalizeReferenceForEdit(content, mimeType)
		if err != nil {
			return nil, err
		}
		payload.set("image", encodeDataURL(normalized, normalizedMime))
	}
	generationURL := joinURL(channel.BaseURL, apiPathOrDefault(channel.APIPath, "/v1/images/generations"))
	timeout := openAIGenerationTimeout(channel, a.service.cfg.ProviderTimeoutSeconds)
	a.service.log.Info("发送硅基流动生图请求",
		"task_no", task.TaskNo, "url", generationURL, "mode", task.GenerationMode,
		"size", size, "quality", task.QualityCode, "has_ref", task.ReferenceAttached())
	adapter := openAIAdapter{service: a.service}
	responseData, requestID, err := adapter.postJSON(ctx, generationURL, apiKey, payload, timeout)
	if err != nil {
		return nil, err
	}

	images := []providerImage{}
	if object, ok := responseData.(*orderedObject); ok {
		combined := []any{}
		if items, ok := object.values["images"].([]any); ok {
			combined = append(combined, items...)
		}
		if items, ok := object.values["data"].([]any); ok {
			combined = append(combined, items...)
		}
		for _, item := range combined {
			entry, ok := item.(*orderedObject)
			if !ok {
				continue
			}
			if value, ok := entry.values["b64_json"]; ok && scalarText(value) != "" {
				decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(scalarText(value)))
				if err != nil {
					continue
				}
				images = append(images, providerImage{mimeType: "image/png", content: decoded})
				break
			}
			for _, key := range []string{"url", "image_url"} {
				if value, ok := entry.values[key]; ok && scalarText(value) != "" {
					images = append(images, providerImage{mimeType: "image/png", remoteURL: scalarText(value)})
					break
				}
			}
			if len(images) > 0 {
				break
			}
		}
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("硅基流动渠道未返回可用图片")
	}
	return &providerResult{
		requestID:   fallbackRequestID(requestID, responseData, task.TaskNo),
		rawResponse: trimOpenAIImageResponse(responseData, a.service.log),
		images:      images,
	}, nil
}

// ── LLM channel (contract §5.10) ──

// ChatMessage is one OpenAI-compatible chat message.
type ChatMessage struct {
	Role    string
	Content string
}

// ChatCompletion posts a chat request to an LLM channel and returns
// choices[0].message.content. internal/novel and the /api/chat endpoint both
// talk to LLM channels through this helper.
func (s *Service) ChatCompletion(ctx context.Context, channel *Channel, messages []ChatMessage, temperature float64) (string, error) {
	apiKey, err := channel.APIKey(s.cipher)
	if err != nil {
		return "", err
	}
	return s.postChatCompletion(ctx, channel.BaseURL, channel.APIPath, channel.ModelName,
		channel.TimeoutSeconds, apiKey, messages, temperature)
}

// ChatChannel is a resolved LLM channel for sibling domains. The key is
// decrypted inside this package and stays unexported, so no sibling can hold,
// log or serialise it: they call Chat instead.
type ChatChannel struct {
	Name           string
	BaseURL        string
	APIPath        string
	ModelName      string
	TimeoutSeconds int

	service *Service
	apiKey  string
}

// Chat posts a chat completion through the channel.
func (c *ChatChannel) Chat(ctx context.Context, messages []ChatMessage, temperature float64) (string, error) {
	return c.service.postChatCompletion(ctx, c.BaseURL, c.APIPath, c.ModelName,
		c.TimeoutSeconds, c.apiKey, messages, temperature)
}

// ResolveLLMChannel resolves the LLM channel for `code` with its API key
// decrypted. A nil channel with a nil error means "no active channel", which
// callers fall back to their environment configuration for; a channel that
// exists but whose key cannot be decrypted is an error, never a silent
// plaintext fallback.
func (s *Service) ResolveLLMChannel(ctx context.Context, code string) (*ChatChannel, error) {
	channel, err := s.ResolveChannel(ctx, code)
	if err != nil || channel == nil {
		return nil, err
	}
	apiKey, err := channel.APIKey(s.cipher)
	if err != nil {
		s.log.Error("渠道密钥解密失败", "channel", channel.ChannelCode, "error", err)
		return nil, err
	}
	return &ChatChannel{
		Name: channel.Name, BaseURL: channel.BaseURL, APIPath: channel.APIPath,
		ModelName: channel.ModelName, TimeoutSeconds: channel.TimeoutSeconds,
		service: s, apiKey: apiKey,
	}, nil
}

func (s *Service) postChatCompletion(
	ctx context.Context,
	baseURL, apiPath, modelName string,
	timeoutSeconds int,
	apiKey string,
	messages []ChatMessage,
	temperature float64,
) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("对话模型缺少 API Key")
	}
	payload := newOrderedObject()
	payload.set("model", modelName)
	items := make([]any, 0, len(messages))
	for _, message := range messages {
		item := newOrderedObject()
		item.set("role", message.Role)
		item.set("content", message.Content)
		items = append(items, item)
	}
	payload.set("messages", items)
	payload.set("temperature", temperature)

	url := joinURL(baseURL, apiPathOrDefault(apiPath, "/chat/completions"))
	adapter := openAIAdapter{service: s}
	data, _, err := adapter.postJSON(ctx, url, apiKey, payload, timeoutSeconds)
	if err != nil {
		return "", err
	}
	object, ok := data.(*orderedObject)
	if !ok {
		return "", fmt.Errorf("对话 LLM 响应失败")
	}
	choices, _ := object.values["choices"].([]any)
	if len(choices) == 0 {
		return "", fmt.Errorf("对话 LLM 未返回内容")
	}
	choice, _ := choices[0].(*orderedObject)
	if choice == nil {
		return "", fmt.Errorf("对话 LLM 未返回内容")
	}
	message, _ := choice.values["message"].(*orderedObject)
	if message != nil {
		if content := scalarText(message.values["content"]); content != "" {
			return content, nil
		}
	}
	if text := scalarText(choice.values["text"]); text != "" {
		return text, nil
	}
	return "", fmt.Errorf("对话 LLM 未返回内容")
}

// ── shared helpers ──

func buildProviderPrompt(task *Task) string {
	prompt := strings.TrimSpace(task.Prompt)
	if negative := strings.TrimSpace(task.NegativePrompt); negative != "" {
		prompt = prompt + "\nAvoid: " + negative
	}
	return prompt
}

func apiPathOrDefault(apiPath, fallback string) string {
	if strings.TrimSpace(apiPath) == "" {
		return fallback
	}
	return apiPath
}

// resolveOpenAIEditPath prefers the channel's explicit edit path and otherwise
// derives it from the generation path.
func resolveOpenAIEditPath(channel *Channel) string {
	if custom := strings.TrimSpace(channel.EditAPIPath); custom != "" {
		return custom
	}
	apiPath := strings.TrimSpace(channel.APIPath)
	if apiPath == "" {
		apiPath = "/v1/images/generations"
	}
	switch {
	case strings.HasSuffix(apiPath, "/images/generations"):
		return strings.TrimSuffix(apiPath, "/images/generations") + "/images/edits"
	case strings.HasSuffix(apiPath, "/generations"):
		return strings.TrimSuffix(apiPath, "/generations") + "/edits"
	default:
		return strings.TrimRight(apiPath, "/") + "/edits"
	}
}

func referenceFileStem(name string) string {
	base := path.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == "/" {
		return "reference-image"
	}
	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "" {
		return "reference-image"
	}
	return stem
}

func degradedEditStatus(status int) bool {
	switch status {
	case 400, 404, 405, 415, 422:
		return true
	}
	return false
}

func encodeDataURL(content []byte, mimeType string) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(content)
}

func decodeReferenceDataURL(dataURL string) ([]byte, string) {
	raw := strings.TrimSpace(dataURL)
	index := strings.Index(raw, ",")
	if index < 0 {
		return nil, ""
	}
	header, encoded := raw[:index], raw[index+1:]
	if !strings.Contains(header, ";base64") {
		return nil, ""
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(strings.TrimPrefix(header, "data:"), ";")[0]))
	if mimeType == "" {
		mimeType = "image/png"
	}
	return []byte(encoded), mimeType
}

// normalizeReferenceForEdit re-encodes a reference upload into a provider
// friendly, size limited payload: PNG when the image carries alpha, JPEG
// otherwise.
func normalizeReferenceForEdit(content []byte, mimeType string) ([]byte, string, string, error) {
	source, _, err := imageutil.Decode(content)
	if err != nil {
		return nil, "", "", fmt.Errorf("参考图解析失败")
	}
	source = imageutil.ApplyEXIFOrientation(content, source)
	if imageutil.HasAlphaChannel(content, source) {
		payload, outMime, suffix := savePNGWithLimit(source, editReferenceMaxBytes, editReferenceMaxDimension)
		return payload, outMime, suffix, nil
	}
	payload, outMime, suffix := saveJPEGWithLimit(source, editReferenceMaxBytes, editReferenceMaxDimension)
	return payload, outMime, suffix, nil
}

func savePNGWithLimit(source image.Image, maxBytes, maxDimension int) ([]byte, string, string) {
	current := imageutil.Resize(source, maxDimension)
	payload := []byte{}
	for _, dimensionLimit := range []int{maxDimension, 1344, 1024, 768, 512} {
		encoded, err := imageutil.EncodePNG(current, true)
		if err != nil {
			return payload, "image/png", ".png"
		}
		payload = encoded
		if len(payload) <= maxBytes {
			return payload, "image/png", ".png"
		}
		current = imageutil.Resize(current, dimensionLimit)
	}
	return payload, "image/png", ".png"
}

func saveJPEGWithLimit(source image.Image, maxBytes, maxDimension int) ([]byte, string, string) {
	candidate := imageutil.Resize(source, maxDimension)
	smallest := []byte{}
	for _, dimensionLimit := range []int{maxDimension, 1344, 1152, 1024, 896, 768} {
		for _, quality := range []int{88, 82, 76, 70, 64} {
			encoded, err := imageutil.EncodeJPEG(candidate, quality)
			if err != nil {
				return smallest, "image/jpeg", ".jpg"
			}
			if len(smallest) == 0 || len(encoded) < len(smallest) {
				smallest = encoded
			}
			if len(encoded) <= maxBytes {
				return encoded, "image/jpeg", ".jpg"
			}
		}
		candidate = imageutil.Resize(candidate, dimensionLimit)
	}
	return smallest, "image/jpeg", ".jpg"
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func parseSizeParts(sizeCode string) (int, int, error) {
	match := sizeCodePattern.FindStringSubmatch(strings.TrimSpace(sizeCode))
	if match == nil {
		return 0, 0, fmt.Errorf("不支持的尺寸")
	}
	return atoi(match[1]), atoi(match[2]), nil
}

// decodeAndValidateImage reports whether the bytes are a fully decodable image
// of an accepted MIME type, with its dimensions. The reference decodes the
// whole file (Pillow Image.verify), so a truncated payload whose header parses
// is still rejected.
func decodeAndValidateImage(content []byte, mimeType string) (int, int, bool) {
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return 0, 0, false
	}
	decoded, _, err := imageutil.Decode(content)
	if err != nil || decoded == nil {
		return 0, 0, false
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return 0, 0, false
	}
	return width, height, true
}

const (
	editReferenceMaxDimension = 1600
	editReferenceMaxBytes     = 4 * 1024 * 1024
)

func normalizeTimeout(timeoutSeconds int) int {
	if timeoutSeconds <= 0 {
		return defaultTimeoutSecs
	}
	return timeoutSeconds
}

func openAIGenerationTimeout(channel *Channel, floor int) int {
	if floor <= 0 {
		floor = 300
	}
	timeout := channel.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultTimeoutSecs
	}
	if timeout > floor {
		return timeout
	}
	return floor
}

func openAIEditTimeout(channel *Channel, floor time.Duration) int {
	seconds := int(floor / time.Second)
	if seconds <= 0 {
		seconds = 420
	}
	timeout := channel.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultTimeoutSecs
	}
	if timeout > seconds {
		return timeout
	}
	return seconds
}
