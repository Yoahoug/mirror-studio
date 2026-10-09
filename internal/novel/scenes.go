package novel

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// sceneKeys is the input normalisation table for the scene objects an external
// LLM returns: Chinese and English spellings are equally likely, so both are
// accepted (review P1-30 keeps this behaviour).
var sceneKeys = map[string][]string{
	"number":             {"number", "编号", "scene_number", "sceneNumber", "id"},
	"title":              {"title", "中文标题", "标题"},
	"chapter":            {"chapter", "所在章节", "章节"},
	"story_significance": {"story_significance", "storySignificance", "故事意义", "significance"},
	"excerpt":            {"excerpt", "原文摘录", "quote"},
	"visual_prompt":      {"visual_prompt", "visualPrompt", "详细英文视觉描述", "english_visual_description", "prompt"},
}

var auditKeywords = []string{
	"content_policy",
	"safety",
	"moderation",
	"policy",
	"unsafe",
	"sensitive",
	"blocked",
	"rejected",
	"审核",
	"安全",
	"拒绝",
	"违规",
}

var codeFencePattern = regexp.MustCompile("(?is)```(?:json)?\\s*(.*?)\\s*```")

const (
	autoSystemPrompt = "You are a senior film concept art director and literary analyst. " +
		"Read the full Chinese novel deeply, identify the most visually shocking scenes in plot order, " +
		"and return only valid JSON."

	targetedSystemPrompt = "You are a senior film concept art director and literary analyst. " +
		"Read the full Chinese novel deeply, locate and interpret the user's requested plot beat in context, " +
		"then return only valid JSON for one cinematic image."
)

// autoSceneLLMPayload mirrors services._auto_scene_llm_payload. The prompt text
// is compared field by field against the recorded upstream request.
func autoSceneLLMPayload(novelText, modelName string, maxScenes int) *pyObj {
	userPrompt := strings.TrimSpace(fmt.Sprintf(`
请深度理解以下小说全文，按情节顺序挑选不超过 %d 个最震撼、最适合电影级概念图的视觉场景。

要求：
1. 返回 JSON 数组，不要输出解释文字。
2. 每个对象必须包含：编号、中文标题、所在章节、故事意义、原文摘录、详细英文视觉描述。
3. 详细英文视觉描述必须 200-300 words，写实、电影级、动态、生动、真实摄影质感，不要卡通、不要动漫、不要插画感。
4. 若涉及危险、战争、灾难等内容，描述为宏大电影场面和情绪张力，避免血腥细节。
5. 场景数量最多 %d 个。

小说全文：
%s
`, maxScenes, maxScenes, novelText))
	return sceneLLMPayload(modelName, autoSystemPrompt, userPrompt, 0.7)
}

// targetedSceneLLMPayload mirrors services._targeted_scene_llm_payload.
func targetedSceneLLMPayload(novelText, modelName, targetPlot string) *pyObj {
	userPrompt := strings.TrimSpace(fmt.Sprintf(`
请深度理解以下小说全文，并围绕用户指定的剧情只生成 1 个最适合画成电影概念图的镜头。

用户想呈现的剧情：
%s

要求：
1. 必须先结合全文判断这段剧情在小说中的上下文、人物关系、情绪和故事意义，再提炼一个画面瞬间。
2. 只返回 JSON 数组，数组里只能有 1 个对象，不要输出解释文字。
3. 对象必须包含：编号、中文标题、所在章节、故事意义、原文摘录、详细英文视觉描述。
4. 如果用户描述比较模糊，请在全文里寻找最匹配的剧情，不要拒绝；如果没有逐字匹配，也要基于全书语境选择最接近的一幕。
5. 详细英文视觉描述必须 200-300 words，写实、电影级、动态、生动、真实摄影质感，不要卡通、不要动漫、不要插画感。
6. 若涉及危险、战争、灾难等内容，描述为宏大电影场面和情绪张力，避免血腥细节。

小说全文：
%s
`, targetPlot, novelText))
	return sceneLLMPayload(modelName, targetedSystemPrompt, userPrompt, 0.65)
}

func sceneLLMPayload(modelName, systemPrompt, userPrompt string, temperature float64) *pyObj {
	messages := []any{
		newPyObj().Set("role", "system").Set("content", systemPrompt),
		newPyObj().Set("role", "user").Set("content", userPrompt),
	}
	return newPyObj().Set("model", modelName).Set("messages", messages).Set("temperature", temperature)
}

// stripJSONCodeFence mirrors services._strip_json_code_fence.
func stripJSONCodeFence(content string) string {
	text := strings.TrimSpace(content)
	if match := codeFencePattern.FindStringSubmatch(text); match != nil {
		return strings.TrimSpace(match[1])
	}
	return text
}

// parseSceneJSON mirrors services.parse_scene_json.
func parseSceneJSON(content string, maxScenes int) ([]*pyObj, error) {
	text := stripJSONCodeFence(content)
	parsed, err := decodePyJSON([]byte(text))
	if err != nil {
		start := strings.Index(text, "[")
		end := strings.LastIndex(text, "]")
		if start < 0 || end <= start {
			return nil, err
		}
		parsed, err = decodePyJSON([]byte(text[start : end+1]))
		if err != nil {
			return nil, err
		}
	}
	if object, ok := parsed.(*pyObj); ok {
		parsed = firstTruthy(object, "scenes", "场景", "items")
	}
	items, ok := parsed.([]any)
	if !ok {
		return nil, &VisualizationError{Message: "AI 返回的场景 JSON 不是数组"}
	}
	limit := maxScenes
	if limit < 1 {
		limit = 1
	}
	if len(items) > limit {
		items = items[:limit]
	}
	scenes := []*pyObj{}
	for index, item := range items {
		scenes = append(scenes, normalizeScene(item, index+1))
	}
	filtered := []*pyObj{}
	for _, scene := range scenes {
		title, _ := scene.Get("title")
		prompt, _ := scene.Get("visual_prompt")
		if pyString(title) != "" && pyString(prompt) != "" {
			filtered = append(filtered, scene)
		}
	}
	return filtered, nil
}

// firstTruthy reproduces `a or b or c` for the wrapper shapes the LLM may use.
func firstTruthy(object *pyObj, keys ...string) any {
	for _, key := range keys {
		value, ok := object.Get(key)
		if !ok || !pyTruthy(value) {
			continue
		}
		return value
	}
	return nil
}

func pyTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case json.Number:
		return typed != "0" && typed != "0.0"
	case []any:
		return len(typed) > 0
	case *pyObj:
		return typed.Len() > 0
	}
	return true
}

// normalizeScene mirrors services._normalize_scene.
func normalizeScene(raw any, fallbackNumber int) *pyObj {
	object, ok := raw.(*pyObj)
	if !ok {
		return newPyObj().
			Set("number", fallbackNumber).
			Set("title", fmt.Sprintf("场景 %d", fallbackNumber)).
			Set("chapter", "").
			Set("story_significance", "").
			Set("excerpt", "").
			Set("visual_prompt", strings.TrimSpace(pythonStr(raw)))
	}
	number, ok := pythonInt(firstValue(object, sceneKeys["number"], fallbackNumber))
	if !ok {
		number = fallbackNumber
	}
	if number == 0 {
		number = fallbackNumber
	}
	title := truncateRunes(strings.TrimSpace(pythonStr(firstValue(object, sceneKeys["title"], fmt.Sprintf("场景 %d", fallbackNumber)))), 160)
	chapter := truncateRunes(strings.TrimSpace(pythonStr(firstValue(object, sceneKeys["chapter"], ""))), 160)
	return newPyObj().
		Set("number", number).
		Set("title", title).
		Set("chapter", chapter).
		Set("story_significance", strings.TrimSpace(pythonStr(firstValue(object, sceneKeys["story_significance"], "")))).
		Set("excerpt", strings.TrimSpace(pythonStr(firstValue(object, sceneKeys["excerpt"], "")))).
		Set("visual_prompt", strings.TrimSpace(pythonStr(firstValue(object, sceneKeys["visual_prompt"], ""))))
}

// firstValue mirrors services._first_value: the first key whose value is
// neither None nor the empty string.
func firstValue(object *pyObj, keys []string, fallback any) any {
	for _, key := range keys {
		value, ok := object.Get(key)
		if !ok {
			continue
		}
		if value == nil {
			continue
		}
		if text, isText := value.(string); isText && text == "" {
			continue
		}
		return value
	}
	return fallback
}

// sceneFromPlan mirrors services._scene_from_plan.
func sceneFromPlan(scene *pyObj, index int) (title, chapter, storySignificance, excerpt, visualPrompt string) {
	title = truncateRunes(firstStringOr(scene, "title", fmt.Sprintf("场景 %d", index)), 160)
	chapter = truncateRunes(firstStringOr(scene, "chapter", ""), 160)
	storySignificance = firstStringOr(scene, "story_significance", "")
	excerpt = firstStringOr(scene, "excerpt", "")
	visualPrompt = firstStringOr(scene, "visual_prompt", "")
	return
}

func firstStringOr(object *pyObj, key, fallback string) string {
	value, ok := object.Get(key)
	if !ok || !pyTruthy(value) {
		return fallback
	}
	return pythonStr(value)
}

// prepareScenePrompt mirrors services._prepare_scene_prompt.
func prepareScenePrompt(image SceneImage) string {
	return strings.TrimSpace(fmt.Sprintf(`
Create a cinematic, photorealistic concept image for this novel scene.
Style: live-action film still, realistic human scale, dramatic natural lighting, tactile materials, atmospheric depth, physically plausible camera lens, high-end production design, emotionally intense, vivid and dynamic.
Avoid: cartoon, anime, manga, illustration, painterly style, flat colors, childish proportions, over-stylized fantasy art.

Scene title: %s
Chapter: %s
Story meaning: %s
Visual description:
%s
`, image.Title, image.Chapter, image.StorySignificance, image.VisualPrompt))
}

// rewritePromptForSafety mirrors services._rewrite_prompt_for_safety.
func rewritePromptForSafety(prompt string) string {
	return strings.TrimSpace(fmt.Sprintf(`
Rewrite this image prompt to be acceptable for a strict safety filter while preserving a realistic cinematic concept-art look and the story's awe, scale, tension, and emotional impact.
Use non-graphic language. Avoid explicit injury, gore, cruelty, identifiable public figures, hateful symbols, and sexual content.
Keep it photorealistic, live-action, dramatic, and vivid.

Original prompt:
%s
`, prompt))
}

// rewritePromptForRetry mirrors services._rewrite_prompt_for_retry.
func rewritePromptForRetry(prompt, errorMessage string) string {
	return strings.TrimSpace(fmt.Sprintf(`
Rewrite this image prompt for a second generation attempt.
Keep the same story beat and cinematic realism, but make the composition clearer, reduce unnecessary detail, and focus on one unmistakable visual moment.
Use concise English. Avoid overly long nested clauses.
Do not mention the failure directly.

Original prompt:
%s

Recent failure context:
%s
`, prompt, errorMessage))
}

func isAuditError(message string) bool {
	lowered := strings.ToLower(message)
	for _, keyword := range auditKeywords {
		if strings.Contains(lowered, keyword) {
			return true
		}
	}
	return false
}

// pythonStr reproduces Python's str() for the JSON value shapes an LLM can
// return; the reference builds every scene field with str(...).
func pythonStr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case json.Number:
		return string(typed)
	case float64:
		return pyFloat(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case []any:
		return pythonRepr(typed)
	case *pyObj:
		return pythonRepr(typed)
	}
	return fmt.Sprintf("%v", value)
}

// pythonRepr approximates repr() for the rare case of a container where the
// prompt expects a scalar, which the reference stringifies through str().
func pythonRepr(value any) string {
	switch typed := value.(type) {
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, pythonRepr(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *pyObj:
		parts := make([]string, 0, typed.Len())
		for index, key := range typed.Keys() {
			item, _ := typed.Get(key)
			parts = append(parts, pythonStr(key)+": "+pythonRepr(item))
			_ = index
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case string:
		quoted := strings.ReplaceAll(typed, `\`, `\\`)
		quoted = strings.ReplaceAll(quoted, `'`, `\'`)
		quoted = strings.ReplaceAll(quoted, "\n", `\n`)
		return "'" + quoted + "'"
	}
	return pythonStr(value)
}

// pythonInt mirrors int(value), reporting ok=false where Python raises
// TypeError/ValueError so the caller can fall back.
func pythonInt(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		if parsed, err := strconv.ParseInt(string(typed), 10, 64); err == nil {
			return int(parsed), true
		}
		if parsed, err := strconv.ParseFloat(string(typed), 64); err == nil {
			return int(math.Trunc(parsed)), true
		}
		return 0, false
	case float64:
		return int(math.Trunc(typed)), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return int(parsed), true
		}
		return 0, false
	}
	return 0, false
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// pyString is a convenience for a value that is expected to be text.
func pyString(value any) string {
	text, _ := value.(string)
	return text
}
