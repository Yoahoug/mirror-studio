package novel

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
)

// ── fixtures ──

// pythonFixtureBody is the default body of tools/diff/harness.py epub_bytes().
const pythonFixtureBody = "第一章 星港\n舱门缓缓打开，星光涌入。\n第二章 归途\n他转身走向长廊。"

// expectedFixtureText is what the reference extraction produces for the
// fixture: ebooklib rebuilds each document and copies only the body, so the
// <title> in the head never reaches the text (35 characters, the value the
// recorded trace logs).
const expectedFixtureText = "第一章 星港\n舱门缓缓打开，星光涌入。\n第二章 归途\n他转身走向长廊。"

const scenePlanJSON = `[{"编号": 1, "中文标题": "星港初见", "所在章节": "第一章", "故事意义": "主角第一次见到星港，世界观在此展开。", "原文摘录": "舱门缓缓打开，星光涌入。", "详细英文视觉描述": "A vast orbital star port opens before the hero; volumetric starlight spills through the docking bay, industrial gantries receding into haze, live-action film still, dramatic natural lighting, photorealistic concept art."}, {"编号": 2, "中文标题": "长廊独行", "所在章节": "第二章", "故事意义": "主角做出离开的决定。", "原文摘录": "他转身走向长廊。", "详细英文视觉描述": "A lone figure walks down an endless corridor of polished alloy, reflections stretching behind him, cinematic wide lens, moody practical lighting, photorealistic live-action film still."}]`

// autoNovelPrompt is the exact user prompt the reference sends for the fixture.
func autoNovelPrompt(maxScenes int) string {
	return fmt.Sprintf(`请深度理解以下小说全文，按情节顺序挑选不超过 %d 个最震撼、最适合电影级概念图的视觉场景。

要求：
1. 返回 JSON 数组，不要输出解释文字。
2. 每个对象必须包含：编号、中文标题、所在章节、故事意义、原文摘录、详细英文视觉描述。
3. 详细英文视觉描述必须 200-300 words，写实、电影级、动态、生动、真实摄影质感，不要卡通、不要动漫、不要插画感。
4. 若涉及危险、战争、灾难等内容，描述为宏大电影场面和情绪张力，避免血腥细节。
5. 场景数量最多 %d 个。

小说全文：
%s`, maxScenes, maxScenes, expectedFixtureText)
}

const expectedScenePrompt = `Create a cinematic, photorealistic concept image for this novel scene.
Style: live-action film still, realistic human scale, dramatic natural lighting, tactile materials, atmospheric depth, physically plausible camera lens, high-end production design, emotionally intense, vivid and dynamic.
Avoid: cartoon, anime, manga, illustration, painterly style, flat colors, childish proportions, over-stylized fantasy art.

Scene title: 星港初见
Chapter: 第一章
Story meaning: 主角第一次见到星港，世界观在此展开。
Visual description:
A vast orbital star port opens before the hero; volumetric starlight spills through the docking bay, industrial gantries receding into haze, live-action film still, dramatic natural lighting, photorealistic concept art.`

// writeFixtureEPUB mirrors tools/diff/harness.py epub_bytes(): the same five
// entries with the same contents.
func writeFixtureEPUB(t *testing.T, dir, body string) string {
	t.Helper()
	paragraphs := ""
	for _, line := range strings.Split(body, "\n") {
		paragraphs += "<p>" + line + "</p>"
	}
	entries := []struct{ name, content string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` +
			`<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">` +
			`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Diff Fixture</dc:title>` +
			`<dc:identifier id="id">diff-fixture</dc:identifier><dc:language>zh</dc:language></metadata>` +
			`<manifest><item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>` +
			`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>` +
			`<spine toc="ncx"><itemref idref="c1"/></spine></package>`},
		{"OEBPS/chapter1.xhtml", `<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title>` +
			`<style>p{color:red}</style></head><body>` + paragraphs + `</body></html>`},
		{"OEBPS/toc.ncx", `<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">` +
			`<head><meta name="dtb:uid" content="diff-fixture"/></head><docTitle><text>Diff</text></docTitle>` +
			`<navMap><navPoint id="n1"><navLabel><text>Chapter</text></navLabel>` +
			`<content src="chapter1.xhtml"/></navPoint></navMap></ncx>`},
	}
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	for _, entry := range entries {
		writer, err := archive.Create(entry.name)
		if err != nil {
			t.Fatalf("zip entry %s: %v", entry.name, err)
		}
		if _, err := writer.Write([]byte(entry.content)); err != nil {
			t.Fatalf("zip write %s: %v", entry.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	target := filepath.Join(dir, "diff-fixture.epub")
	if err := os.WriteFile(target, buffer.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return target
}

func writeZipEntries(t *testing.T, entries map[string]string) string {
	t.Helper()
	buffer := &bytes.Buffer{}
	archive := zip.NewWriter(buffer)
	for name, content := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatalf("zip entry %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	target := filepath.Join(t.TempDir(), "broken.epub")
	if err := os.WriteFile(target, buffer.Bytes(), 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	return target
}

const containerXML = `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

// ── EPUB ──

func TestEPUBFixtureExtraction(t *testing.T) {
	path := writeFixtureEPUB(t, t.TempDir(), pythonFixtureBody)
	text, err := extractEPUBText(path)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if text != expectedFixtureText {
		t.Fatalf("extracted text = %q, want %q", text, expectedFixtureText)
	}
	if len([]rune(text)) != 35 {
		t.Fatalf("extracted length = %d, want 35 (the recorded trace value)", len([]rune(text)))
	}
}

func TestEPUBFixtureTextNormalisation(t *testing.T) {
	entries := map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf": `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0">` +
			`<manifest><item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest></package>`,
		"OEBPS/chapter1.xhtml": "<html><head><title>不该出现</title><style>p{color:red}</style></head><body>" +
			"<p>第一行\n\n\n\n第二行</p><p>   </p><p>第三行</p><script>alert(1)</script><nav>目录</nav><b>加粗</b>" +
			"</body></html>",
	}
	text, err := extractEPUBText(writeZipEntries(t, entries))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := "第一行\n\n第二行\n第三行\n加粗"
	if text != want {
		t.Fatalf("extracted text = %q, want %q", text, want)
	}
}

func TestEPUBManifestOrder(t *testing.T) {
	entries := map[string]string{
		"META-INF/container.xml": containerXML,
		"OEBPS/content.opf": `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0">` +
			`<manifest><item id="b" href="b.xhtml" media-type="application/xhtml+xml"/>` +
			`<item id="a" href="a.xhtml" media-type="application/xhtml+xml"/>` +
			`<item id="s" href="style.css" media-type="text/css"/></manifest>` +
			`<spine><itemref idref="a"/><itemref idref="b"/></spine></package>`,
		"OEBPS/a.xhtml": `<html><body><p>A</p></body></html>`,
		"OEBPS/b.xhtml": `<html><body><p>B</p></body></html>`,
	}
	path := writeZipEntries(t, entries)
	text, err := extractEPUBText(path)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if text != "B\n\nA" {
		t.Fatalf("extraction must follow manifest order, got %q", text)
	}
}

func TestEPUBMalformedInputs(t *testing.T) {
	dir := t.TempDir()
	notAZip := filepath.Join(dir, "not-a-zip.epub")
	if err := os.WriteFile(notAZip, []byte("this is not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.epub")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		want string
	}{
		{"not-a-zip", notAZip, "'Bad Zip file'"},
		{"empty", empty, "'Bad Zip file'"},
		{"zip-without-container", writeZipEntries(t, map[string]string{"hello.txt": "hi"}),
			"There is no item named 'META-INF/container.xml' in the archive"},
		{"container-without-rootfile", writeZipEntries(t, map[string]string{
			"META-INF/container.xml": `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles/></container>`,
		}), "Can not find container file"},
		{"opf-missing", writeZipEntries(t, map[string]string{"META-INF/container.xml": containerXML}),
			"Can not find container file"},
		{"document-missing", writeZipEntries(t, map[string]string{
			"META-INF/container.xml": containerXML,
			"OEBPS/content.opf": `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0">` +
				`<manifest><item id="c1" href="gone.xhtml" media-type="application/xhtml+xml"/></manifest></package>`,
		}), "There is no item named 'OEBPS/gone.xhtml' in the archive"},
		{"no-documents", writeZipEntries(t, map[string]string{
			"META-INF/container.xml": containerXML,
			"OEBPS/content.opf": `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0">` +
				`<manifest></manifest><spine/></package>`,
		}), "EPUB 未提取到正文内容"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := extractEPUBText(testCase.path)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if err.Error() != testCase.want {
				t.Fatalf("error = %q, want %q", err.Error(), testCase.want)
			}
		})
	}
}

// ── scene parsing ──

func TestParseSceneJSONCodeFenceAndKeys(t *testing.T) {
	scenes, err := parseSceneJSON("```json\n"+scenePlanJSON+"\n```", 10)
	if err != nil {
		t.Fatalf("fenced parse: %v", err)
	}
	if len(scenes) != 2 {
		t.Fatalf("scenes = %d, want 2", len(scenes))
	}
	if title, _ := scenes[0].Get("title"); title != "星港初见" {
		t.Fatalf("title = %v", title)
	}
	if chapter, _ := scenes[0].Get("chapter"); chapter != "第一章" {
		t.Fatalf("chapter = %v", chapter)
	}

	english := `[{"id": 7, "title": "Starport", "chapter": "Ch 1", "storySignificance": "sig", "quote": "quote", "visualPrompt": "prompt", "extra": "ignored"}]`
	scenes, err = parseSceneJSON(english, 10)
	if err != nil {
		t.Fatalf("english parse: %v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("scenes = %d, want 1", len(scenes))
	}
	if number, _ := scenes[0].Get("number"); number != 7 {
		t.Fatalf("number = %v, want 7", number)
	}
	if significance, _ := scenes[0].Get("story_significance"); significance != "sig" {
		t.Fatalf("story_significance = %v", significance)
	}
	if _, ok := scenes[0].Get("extra"); ok {
		t.Fatalf("normalized scenes only carry the six contract fields")
	}
}

func TestParseSceneJSONWrapperAndFallbacks(t *testing.T) {
	scenes, err := parseSceneJSON(`前缀 {"场景": [{"编号": "abc", "中文标题": "", "详细英文视觉描述": "only prompt"}]} 后缀`, 10)
	if err != nil {
		t.Fatalf("wrapper parse: %v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("scenes = %d, want 1", len(scenes))
	}
	if title, _ := scenes[0].Get("title"); title != "场景 1" {
		t.Fatalf("fallback title = %v", title)
	}
	if number, _ := scenes[0].Get("number"); number != 1 {
		t.Fatalf("fallback number = %v", number)
	}

	scenes, err = parseSceneJSON(`[{"title": "A", "prompt": "P"}, {"title": "B"}]`, 10)
	if err != nil {
		t.Fatalf("filter parse: %v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("scenes without a prompt must be dropped, got %d", len(scenes))
	}

	scenes, err = parseSceneJSON(scenePlanJSON, 1)
	if err != nil {
		t.Fatalf("cap parse: %v", err)
	}
	if len(scenes) != 1 {
		t.Fatalf("max scenes cap not applied: %d", len(scenes))
	}

	scenes, err = parseSceneJSON(`[{"title": "`+strings.Repeat("标", 200)+`", "prompt": "p"}]`, 10)
	if err != nil {
		t.Fatalf("truncation parse: %v", err)
	}
	if title, _ := scenes[0].Get("title"); len([]rune(title.(string))) != 160 {
		t.Fatalf("title length = %d, want 160", len([]rune(title.(string))))
	}
}

func TestParseSceneJSONMalformed(t *testing.T) {
	if _, err := parseSceneJSON(`{"message": "no array here"}`, 10); err == nil {
		t.Fatalf("expected an error for a non-array payload")
	}
	if _, err := parseSceneJSON(`not json at all`, 10); err == nil {
		t.Fatalf("expected an error for invalid JSON")
	}
	_, err := parseSceneJSON(`{"scenes": "text"}`, 10)
	if err == nil || err.Error() != "AI 返回的场景 JSON 不是数组" {
		t.Fatalf("error = %v, want the reference message", err)
	}
	if _, err := parseSceneJSON("", 10); err == nil {
		t.Fatalf("expected an error for an empty reply")
	}
}

// ── events ──

func TestEventLogTrimmingAndSanitisation(t *testing.T) {
	env := newTestEnvironmentWithLimits(t, "1000", "200")
	taskID := env.insertTask(StatusQueued, nil)
	ctx := context.Background()

	payload := newPyObj().
		Set("message", "message").
		Set("logs", []any{newPyObj().Set("x", 1)}).
		Set("empty_logs", nil).
		Set(strings.Repeat("k", 130), "value").
		Set("items", arrayOfN(60)).
		Set("long", strings.Repeat("字", 5000))
	event, err := env.service.appendEvent(ctx, taskID, "log", payload)
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	if event.Payload.Has("logs") {
		t.Fatalf("a non-empty logs key must be dropped")
	}
	if empty, ok := event.Payload.Get("empty_logs"); !ok || empty != nil {
		t.Fatalf("null values survive sanitisation, got %v", empty)
	}
	if _, ok := event.Payload.Get(strings.Repeat("k", 121)); ok {
		t.Fatalf("dict keys must be truncated to 120 characters")
	}
	if _, ok := event.Payload.Get(strings.Repeat("k", 120)); !ok {
		t.Fatalf("keys of exactly 120 characters survive")
	}
	items, _ := event.Payload.Get("items")
	if len(items.([]any)) != 50 {
		t.Fatalf("lists must be truncated to 50 items, got %d", len(items.([]any)))
	}
	truncated, _ := event.Payload.Get("long")
	if !strings.HasSuffix(truncated.(string), "...[truncated 4800 chars]") {
		t.Fatalf("strings must be truncated with the reference marker, got %v", truncated)
	}

	for index := 0; index < 125; index++ {
		if _, err := env.service.appendEvent(ctx, taskID, "log", newPyObj().Set("message", fmt.Sprintf("e%d", index))); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	task, err := env.service.loadTask(ctx, taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if len(task.Logs) != 120 {
		t.Fatalf("log cap = %d, want 120", len(task.Logs))
	}
	if task.Logs[0].ID != 7 || task.Logs[len(task.Logs)-1].ID != task.LastEventID {
		t.Fatalf("surviving ids = %d..%d, last_event_id = %d", task.Logs[0].ID, task.Logs[len(task.Logs)-1].ID, task.LastEventID)
	}
}

func TestFitEventPayloadOversize(t *testing.T) {
	env := newTestEnvironmentWithLimits(t, "1000", "200")
	payload := newPyObj().Set("message", "事件过大")
	for index := 0; index < 40; index++ {
		payload.Set(fmt.Sprintf("key-%d", index), strings.Repeat("x", 40))
	}
	fitted := env.service.fitEventPayload(payload)
	if truncated, _ := fitted.Get("truncated"); truncated != true {
		t.Fatalf("oversize payloads must collapse to the truncated envelope, keys=%v", fitted.Keys())
	}
	if message, _ := fitted.Get("message"); message != "事件过大" {
		t.Fatalf("truncated envelope message = %v", message)
	}
	original, _ := fitted.Get("original_chars")
	if parsed, _ := pythonInt(original); parsed <= 1000 {
		t.Fatalf("original_chars = %v, want the pre-fit size", original)
	}
}

func arrayOfN(count int) []any {
	items := make([]any, 0, count)
	for index := 0; index < count; index++ {
		items = append(items, index)
	}
	return items
}

// ── stalled rescue ──

func TestStalledTaskRescue(t *testing.T) {
	env := newTestEnvironment(t)
	ctx := context.Background()
	now := time.Now().UTC()
	env.service.now = func() time.Time { return now }
	taskID := env.insertTask(StatusRunning, map[string]any{
		"started_at":    db.FormatTime(now.Add(-120 * time.Second)),
		"last_event_id": 0,
		"progress":      3,
	})
	if _, err := env.db.Exec(`INSERT INTO novelviz_novelsceneimage (created_at, updated_at, scene_number, title, chapter,
		story_significance, excerpt, visual_prompt, status, error_message, image_path, image_url, preview_image_path,
		attempts, finished_at, task_id) VALUES (?, ?, 1, 't', '', '', '', '', 'pending', '', '', '', '', 0, NULL, ?)`,
		db.FormatTime(now), db.FormatTime(now), taskID); err != nil {
		t.Fatalf("insert scene: %v", err)
	}

	env.service.rescueStalledTasks(ctx)
	task, err := env.service.loadTask(ctx, taskID)
	if err != nil {
		t.Fatalf("load rescued task: %v", err)
	}
	if task.Status != StatusQueued || task.Progress != 1 {
		t.Fatalf("rescued task = %s/%d, want queued/1", task.Status, task.Progress)
	}
	if task.StartedAt != nil || task.FinishedAt != nil || task.LastEventID != 0 || len(task.Logs) != 0 {
		t.Fatalf("rescue did not clear the runtime state")
	}
	if len(task.Images) != 0 {
		t.Fatalf("rescue must delete the scene rows")
	}

	fresh := env.insertTask(StatusRunning, map[string]any{
		"started_at":    db.FormatTime(now.Add(-120 * time.Second)),
		"last_event_id": 4,
	})
	freshTask, err := env.service.loadTask(ctx, fresh)
	if err != nil {
		t.Fatalf("load fresh: %v", err)
	}
	if freshTask.Status != StatusRunning {
		t.Fatalf("a task with events is not stalled, got %s", freshTask.Status)
	}

	recent := env.insertTask(StatusRunning, map[string]any{
		"started_at": db.FormatTime(now.Add(-5 * time.Second)),
	})
	recentTask, _ := env.service.loadTask(ctx, recent)
	if recentTask.Status != StatusRunning {
		t.Fatalf("a recently started task is not stalled, got %s", recentTask.Status)
	}
}

// ── SSE ──

func TestSSEFramingExactBytes(t *testing.T) {
	env := newTestEnvironment(t)
	now := time.Now().UTC().Truncate(time.Second)
	created := isoformat(now)
	logs := `[{"id": 1, "type": "log", "payload": {"message": "<b>&</b> ` + "\u2028" + ` x", "progress_percent": 3}, "created_at": "` + created + `"}, ` +
		`{"id": 2, "type": "task_done", "payload": {"message": "完成", "progress_percent": 100}, "created_at": "` + created + `"}]`
	taskID := env.insertTask(StatusSucceeded, map[string]any{
		"logs":          logs,
		"last_event_id": 2,
		"progress":      100,
		"finished_at":   db.FormatTime(now),
	})
	response := env.do(env.app.URL, http.MethodGet, fmt.Sprintf("/api/novelviz/tasks/%d/events?last_event_id=0", taskID), nil, "")
	if response.Status != 200 {
		t.Fatalf("status = %d", response.Status)
	}
	if header := response.Header.Get("Content-Type"); header != "text/event-stream" {
		t.Fatalf("content-type = %q", header)
	}
	if header := response.Header.Get("Cache-Control"); header != "no-cache" {
		t.Fatalf("cache-control = %q", header)
	}
	if header := response.Header.Get("X-Accel-Buffering"); header != "no" {
		t.Fatalf("x-accel-buffering = %q", header)
	}
	want := "id: 1\nevent: log\ndata: {\"message\": \"<b>&</b> " + "\u2028" + " x\", \"progress_percent\": 3}\n\n" +
		"id: 2\nevent: task_done\ndata: {\"message\": \"完成\", \"progress_percent\": 100}\n\n"
	if string(response.Body) != want {
		t.Fatalf("stream bytes =\n%q\nwant\n%q", string(response.Body), want)
	}

	empty := env.do(env.app.URL, http.MethodGet, fmt.Sprintf("/api/novelviz/tasks/%d/events?last_event_id=999", taskID), nil, "")
	if empty.Status != 200 || len(empty.Body) != 0 {
		t.Fatalf("finished task with last_event_id=999 returned %d %q", empty.Status, string(empty.Body))
	}
	resumed := env.do(env.app.URL, http.MethodGet, fmt.Sprintf("/api/novelviz/tasks/%d/events?last_event_id=1", taskID), nil, "")
	wantResumed := "id: 2\nevent: task_done\ndata: {\"message\": \"完成\", \"progress_percent\": 100}\n\n"
	if string(resumed.Body) != wantResumed {
		t.Fatalf("resumed stream = %q", string(resumed.Body))
	}
	unknown := env.do(env.app.URL, http.MethodGet, "/api/novelviz/tasks/999999/events", nil, "")
	if unknown.Status != 404 || !strings.HasPrefix(unknown.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("unknown task events = %d %q", unknown.Status, unknown.Header.Get("Content-Type"))
	}
}

func TestSSEWarmAndColdStreamsMatch(t *testing.T) {
	env := newTestEnvironment(t)
	env.startWorker()
	taskID := env.submitTask(t)
	env.waitForTerminal(t, taskID)

	warm := env.do(env.app.URL, http.MethodGet, fmt.Sprintf("/api/novelviz/tasks/%d/events?last_event_id=0", taskID), nil, "")
	if warm.Status != 200 || !bytes.Contains(warm.Body, []byte("event: task_done")) {
		t.Fatalf("warm stream = %d %q", warm.Status, string(warm.Body))
	}
	warmSnapshot := env.service.snapshotForStream(context.Background(), taskID)
	if !warmSnapshot.Loaded || warmSnapshot.LastID != 9 {
		t.Fatalf("warm snapshot = %+v", warmSnapshot)
	}

	// A second service over the same database has a cold hub: its snapshot has
	// to come from the stored row and serialize to the same payloads.
	cold := env.freshService()
	coldSnapshot := cold.snapshotForStream(context.Background(), taskID)
	if !coldSnapshot.Loaded || coldSnapshot.Missing {
		t.Fatalf("cold snapshot was not filled from the database")
	}
	if len(coldSnapshot.Events) != len(warmSnapshot.Events) {
		t.Fatalf("cold events = %d, warm events = %d", len(coldSnapshot.Events), len(warmSnapshot.Events))
	}
	for index, event := range coldSnapshot.Events {
		coldPayload, err := marshalPython(event.Payload, false)
		if err != nil {
			t.Fatalf("cold payload: %v", err)
		}
		warmPayload, err := marshalPython(warmSnapshot.Events[index].Payload, false)
		if err != nil {
			t.Fatalf("warm payload: %v", err)
		}
		if coldPayload != warmPayload {
			t.Fatalf("event %d payload differs:\ncold=%s\nwarm=%s", index, coldPayload, warmPayload)
		}
	}

	coldServer := env.serveFor(cold)
	coldResponse := env.do(coldServer.URL, http.MethodGet, fmt.Sprintf("/api/novelviz/tasks/%d/events?last_event_id=0", taskID), nil, "")
	if coldResponse.Status != 200 {
		t.Fatalf("cold stream status = %d", coldResponse.Status)
	}
	if string(coldResponse.Body) != string(warm.Body) {
		t.Fatalf("cold stream differs from the warm stream:\ncold=%q\nwarm=%q", string(coldResponse.Body), string(warm.Body))
	}
}

func TestSSEMissingTaskFrame(t *testing.T) {
	env := newTestEnvironment(t)
	env.service.hub.markMissing(7)
	snapshot := env.service.snapshotForStream(context.Background(), 7)
	if !snapshot.Missing {
		t.Fatalf("a missing row must surface as missing to the stream")
	}
}

// ── pipeline ──

func TestPipelineMatchesReference(t *testing.T) {
	env := newTestEnvironment(t)
	env.startWorker()
	taskID := env.submitTask(t)
	env.waitForTerminal(t, taskID)

	task, err := env.service.loadTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.Status != StatusSucceeded || task.Progress != 100 {
		t.Fatalf("task = %s/%d, want succeeded/100", task.Status, task.Progress)
	}
	types := make([]string, 0, len(task.Logs))
	progressValues := []int{}
	for _, event := range task.Logs {
		types = append(types, event.Type)
		if value, ok := event.Payload.Get("progress_percent"); ok {
			parsed, _ := pythonInt(value)
			progressValues = append(progressValues, parsed)
		}
	}
	// Every scene is announced in scene order before the concurrent fan-out, then
	// the completion events follow in commit order — the sequence the reference
	// records (see .reftmp/run-ref/main/trace.json step 147).
	wantTypes := []string{"task_started", "log", "log", "scene_plan_ready", "image_started", "image_started", "image_done", "image_done", "task_done"}
	if strings.Join(types, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("event types = %v", types)
	}
	wantProgress := []int{3, 8, 18, 36, 66, 96, 100}
	if fmt.Sprint(progressValues) != fmt.Sprint(wantProgress) {
		t.Fatalf("progress milestones = %v, want %v", progressValues, wantProgress)
	}
	if message, _ := task.Logs[2].Payload.Get("message"); message != "正文提取完成，共 35 字，开始使用 默认 LLM 渠道 深度分析" {
		t.Fatalf("analysis message = %q", message)
	}
	if length, _ := task.Logs[2].Payload.Get("text_length"); fmt.Sprint(length) != "35" {
		t.Fatalf("text_length = %v", length)
	}
	if len(task.Images) != 2 {
		t.Fatalf("images = %d", len(task.Images))
	}
	for _, image := range task.Images {
		if image.Status != SceneStatusSucceeded || image.Attempts != 1 {
			t.Fatalf("scene %d = %s/attempts %d", image.SceneNumber, image.Status, image.Attempts)
		}
		if _, err := os.Stat(filepath.Join(env.mediaRoot, filepath.FromSlash(image.ImagePath))); err != nil {
			t.Fatalf("scene file missing: %v", err)
		}
		if !strings.HasSuffix(image.PreviewImagePath, "-preview.jpg") {
			t.Fatalf("preview path = %q, want a jpeg thumbnail", image.PreviewImagePath)
		}
		if _, err := os.Stat(filepath.Join(env.mediaRoot, filepath.FromSlash(image.PreviewImagePath))); err != nil {
			t.Fatalf("preview file missing: %v", err)
		}
	}

	// Stored log and scene plan keep Python's json.dumps shape: ensure_ascii=True
	// with ", " / ": " separators.
	var logsRaw, scenesRaw string
	if err := env.db.QueryRow(`SELECT logs, scenes_json FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID).Scan(&logsRaw, &scenesRaw); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !strings.Contains(logsRaw, `[{"id": 1, "type": "task_started", "payload": {"message": "\u5c0f\u8bf4\u53ef\u89c6\u5316\u4efb\u52a1\u5df2\u542f\u52a8", "progress_percent": 3}, "created_at": "`) {
		t.Fatalf("stored log is not the reference serialisation: %.200s", logsRaw)
	}
	if !strings.HasPrefix(scenesRaw, `[{"number": 1, "title": "\u661f\u6e2f\u521d\u89c1"`) {
		t.Fatalf("stored scene plan is not the reference serialisation: %.120s", scenesRaw)
	}

	requests := env.upstream.requests()
	if len(requests) != 3 {
		t.Fatalf("upstream requests = %d, want 1 llm + 2 images", len(requests))
	}
	llm := requests[0]
	if llm.path != "/v1/chat/completions" || llm.authorization != "Bearer diff-llm-key" {
		t.Fatalf("llm request = %+v", llm)
	}
	var llmBody map[string]any
	if err := json.Unmarshal(llm.body, &llmBody); err != nil {
		t.Fatalf("llm body: %v", err)
	}
	if llmBody["model"] != "diff-llm-model" || llmBody["temperature"] != 0.7 {
		t.Fatalf("llm payload = %v", llmBody)
	}
	messages := llmBody["messages"].([]any)
	if got := messages[0].(map[string]any)["content"]; got != autoSystemPrompt {
		t.Fatalf("system prompt = %q", got)
	}
	if got := messages[1].(map[string]any)["content"]; got != autoNovelPrompt(10) {
		t.Fatalf("user prompt = %q", got)
	}

	// Scenes render concurrently, so the recorder's ordering of the two image
	// calls is not part of the contract; the payload carrying the first scene's
	// prompt is.
	var imageBody map[string]any
	for _, candidate := range requests[1:] {
		if candidate.path != "/v1/images/generations" || candidate.authorization != "Bearer diff-image-key" {
			t.Fatalf("image request = %+v", candidate)
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(candidate.body, &decoded); err != nil {
			t.Fatalf("image body: %v", err)
		}
		if decoded["prompt"] == expectedScenePrompt {
			imageBody = decoded
		}
	}
	if imageBody == nil {
		t.Fatalf("no image request carried the first scene prompt (%d requests)", len(requests))
	}
	if imageBody["model"] != "diff-image-model" || imageBody["size"] != "1024x1024" ||
		imageBody["quality"] != "high" || imageBody["response_format"] != "b64_json" {
		t.Fatalf("image payload = %v", imageBody)
	}
}

func TestTargetedPipeline(t *testing.T) {
	env := newTestEnvironment(t)
	env.startWorker()
	created := env.postJSON("/api/novelviz/tasks",
		`{"book_filename": "diff-fixture.epub", "scene_mode": "targeted", "target_plot": "主角第一次进入星港"}`)
	if created.Status != 201 {
		t.Fatalf("targeted create = %d %s", created.Status, string(created.Body))
	}
	taskID := createdTaskID(t, created.Body)
	env.waitForTerminal(t, taskID)
	task, err := env.service.loadTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.Status != StatusSucceeded || len(task.Images) != 1 {
		t.Fatalf("targeted task = %s with %d images", task.Status, len(task.Images))
	}
	types := []string{}
	for _, event := range task.Logs {
		types = append(types, event.Type)
	}
	want := "task_started,log,log,scene_plan_ready,image_started,image_done,task_done"
	if strings.Join(types, ",") != want {
		t.Fatalf("targeted event types = %v", types)
	}
	if message, _ := task.Logs[0].Payload.Get("message"); message != "指定剧情单图任务已启动" {
		t.Fatalf("targeted start message = %q", message)
	}
	if message, _ := task.Logs[2].Payload.Get("message"); message != "正文提取完成，共 35 字，开始使用 默认 LLM 渠道 分析指定剧情" {
		t.Fatalf("targeted analysis message = %q", message)
	}
	if mode, _ := task.Logs[2].Payload.Get("scene_mode"); mode != "targeted" {
		t.Fatalf("scene_mode payload = %v", mode)
	}
	if message, _ := task.Logs[3].Payload.Get("message"); message != "已定位指定剧情镜头，开始生成单张图片" {
		t.Fatalf("targeted plan message = %q", message)
	}

	requests := env.upstream.requests()
	if len(requests) != 2 {
		t.Fatalf("targeted upstream requests = %d", len(requests))
	}
	var llmBody map[string]any
	if err := json.Unmarshal(requests[0].body, &llmBody); err != nil {
		t.Fatalf("llm body: %v", err)
	}
	if llmBody["temperature"] != 0.65 {
		t.Fatalf("targeted temperature = %v", llmBody["temperature"])
	}
	if !strings.Contains(llmBody["messages"].([]any)[1].(map[string]any)["content"].(string), "用户想呈现的剧情：\n主角第一次进入星港") {
		t.Fatalf("targeted prompt does not embed the requested plot")
	}
}

// ── endpoints ──

func TestEndpointsMatchContract(t *testing.T) {
	env := newTestEnvironment(t)
	env.startWorker()

	books := env.get("/api/novelviz/books")
	if books.Status != 200 {
		t.Fatalf("books status = %d", books.Status)
	}
	booksData := decodeDataList(t, books.Body)
	if len(booksData) != 1 || booksData[0]["id"] != "builtin:diff-fixture.epub" || booksData[0]["created_at"] != nil {
		t.Fatalf("books = %v", booksData)
	}
	if booksData[0]["source"] != "builtin" || booksData[0]["uploaded_book_id"] != nil {
		t.Fatalf("book fields = %v", booksData[0])
	}

	missing := env.postJSON("/api/novelviz/tasks", `{}`)
	if missing.Status != 400 || !strings.Contains(string(missing.Body), `"code":400100`) ||
		!strings.Contains(string(missing.Body), "请选择 EPUB 小说文件") {
		t.Fatalf("missing book = %d %s", missing.Status, string(missing.Body))
	}
	traversal := env.postJSON("/api/novelviz/tasks", `{"book_filename": "../etc/passwd"}`)
	if traversal.Status != 400 || !strings.Contains(string(traversal.Body), "请选择 EPUB 小说文件") {
		t.Fatalf("traversal = %d %s", traversal.Status, string(traversal.Body))
	}
	shortPlot := env.postJSON("/api/novelviz/tasks", `{"book_filename": "diff-fixture.epub", "scene_mode": "targeted", "target_plot": "短"}`)
	if shortPlot.Status != 400 || !strings.Contains(string(shortPlot.Body), `"code":400103`) {
		t.Fatalf("short plot = %d %s", shortPlot.Status, string(shortPlot.Body))
	}
	unknownBook := env.postJSON("/api/novelviz/tasks", `{"book_filename": "missing.epub"}`)
	if unknownBook.Status != 400 || !strings.Contains(string(unknownBook.Body), `"code":400100`) ||
		!strings.Contains(string(unknownBook.Body), "EPUB 文件不存在") {
		t.Fatalf("missing file = %d %s", unknownBook.Status, string(unknownBook.Body))
	}
	unknownUpload := env.postJSON("/api/novelviz/tasks", `{"book_source": "uploaded", "uploaded_book_id": 999}`)
	if unknownUpload.Status != 400 || !strings.Contains(string(unknownUpload.Body), "上传的 EPUB 文件不存在") {
		t.Fatalf("missing upload = %d %s", unknownUpload.Status, string(unknownUpload.Body))
	}

	created := env.postJSON("/api/novelviz/tasks", `{"book_filename": "diff-fixture.epub", "scene_mode": "auto"}`)
	if created.Status != 201 {
		t.Fatalf("create = %d %s", created.Status, string(created.Body))
	}
	taskID := createdTaskID(t, created.Body)
	env.waitForTerminal(t, taskID)

	detail := env.get(fmt.Sprintf("/api/novelviz/tasks/%d", taskID))
	detailData := decodeDataMap(t, detail.Body)
	if detailData["status"] != "succeeded" || detailData["book_source"] != "builtin" || detailData["scene_mode"] != "auto" {
		t.Fatalf("detail = %v", detailData)
	}
	if len(detailData["logs"].([]any)) != 9 {
		t.Fatalf("detail logs = %v", detailData["logs"])
	}
	if detailData["progress_percent"].(float64) != 100 {
		t.Fatalf("detail progress = %v", detailData["progress_percent"])
	}

	list := env.get("/api/novelviz/tasks")
	listData := decodeDataList(t, list.Body)
	if len(listData) != 1 || len(listData[0]["logs"].([]any)) != 0 {
		t.Fatalf("list must omit logs: %v", listData)
	}

	notFound := env.get("/api/novelviz/tasks/999999")
	if notFound.Status != 404 || !strings.HasPrefix(notFound.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("unknown task = %d %q", notFound.Status, notFound.Header.Get("Content-Type"))
	}
	if !strings.Contains(strings.ToLower(string(notFound.Body)), "csrf") {
		t.Fatalf("the reference 404 fingerprint must be present")
	}
	retry := env.postJSON(fmt.Sprintf("/api/novelviz/tasks/%d/retry", taskID), "")
	if retry.Status != 400 || !strings.Contains(string(retry.Body), "只有失败或已取消的小说可视化任务可以重试") {
		t.Fatalf("retry on success = %d %s", retry.Status, string(retry.Body))
	}
	if env.postJSON("/api/novelviz/tasks/999999/retry", "").Status != 404 {
		t.Fatalf("retry unknown must be a 404 page")
	}

	images := detailData["images"].([]any)
	imageID := int64(images[0].(map[string]any)["id"].(float64))
	full := env.get(fmt.Sprintf("/api/novelviz/tasks/%d/images/%d.png", taskID, imageID))
	if full.Status != 200 || full.Header.Get("Content-Type") != "image/png" ||
		full.Header.Get("Cache-Control") != "private, max-age=3600" {
		t.Fatalf("image = %d %q %q", full.Status, full.Header.Get("Content-Type"), full.Header.Get("Cache-Control"))
	}
	wantDisposition := `inline; filename="novel-scene.png"; filename*=UTF-8''%E6%98%9F%E6%B8%AF%E5%88%9D%E8%A7%81.png`
	if got := full.Header.Get("Content-Disposition"); got != wantDisposition {
		t.Fatalf("content-disposition = %q, want %q", got, wantDisposition)
	}
	if width, height := imageDimensions(t, full.Body); width != 64 || height != 64 {
		t.Fatalf("image size = %dx%d", width, height)
	}
	preview := env.get(fmt.Sprintf("/api/novelviz/tasks/%d/images/%d.png?preview=1", taskID, imageID))
	if preview.Status != 200 || preview.Header.Get("Content-Type") != "image/jpeg" ||
		preview.Header.Get("Cache-Control") != "private, max-age=86400, immutable" {
		t.Fatalf("preview = %d %q %q", preview.Status, preview.Header.Get("Content-Type"), preview.Header.Get("Cache-Control"))
	}
	download := env.get(fmt.Sprintf("/api/novelviz/tasks/%d/images/%d.png?download=1", taskID, imageID))
	if !strings.HasPrefix(download.Header.Get("Content-Disposition"), "attachment;") ||
		download.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("download = %q %q", download.Header.Get("Content-Disposition"), download.Header.Get("Content-Type"))
	}
	inline := env.get(fmt.Sprintf("/api/novelviz/tasks/%d/images/%d.png?download=1&disposition=inline", taskID, imageID))
	if !strings.HasPrefix(inline.Header.Get("Content-Disposition"), "inline;") {
		t.Fatalf("disposition=inline = %q", inline.Header.Get("Content-Disposition"))
	}
	if env.get("/api/novelviz/tasks/999999/images/1.png").Status != 404 {
		t.Fatalf("cross-task image access must be a 404 page")
	}

	deleted := env.delete(fmt.Sprintf("/api/novelviz/tasks/%d", taskID))
	if deleted.Status != 200 || !strings.Contains(string(deleted.Body), `"deleted":true`) {
		t.Fatalf("delete = %d %s", deleted.Status, string(deleted.Body))
	}
	if env.get(fmt.Sprintf("/api/novelviz/tasks/%d", taskID)).Status != 404 {
		t.Fatalf("deleted task must be gone")
	}
	if env.get("/api/novelviz/tasks/999999").Status != 404 {
		t.Fatalf("deleting an unknown task must stay a 404")
	}
}

func TestUploadAndUploadedBookTask(t *testing.T) {
	env := newTestEnvironment(t)
	missing := env.postMultipart("/api/novelviz/books/upload", "", "", nil)
	if missing.Status != 400 || !strings.Contains(string(missing.Body), "请选择 EPUB 文件") ||
		!strings.Contains(string(missing.Body), `"code":400101`) {
		t.Fatalf("missing file = %d %s", missing.Status, string(missing.Body))
	}
	badName := env.postMultipart("/api/novelviz/books/upload", "file", "story.txt", []byte("not a zip"))
	if badName.Status != 400 || !strings.Contains(string(badName.Body), `"code":400102`) ||
		!strings.Contains(string(badName.Body), "请上传 EPUB 小说文件") {
		t.Fatalf("bad name = %d %s", badName.Status, string(badName.Body))
	}
	tooBig := env.postMultipart("/api/novelviz/books/upload", "file", "huge.epub", bytes.Repeat([]byte("x"), 2*1024*1024))
	if tooBig.Status != 400 || !strings.Contains(string(tooBig.Body), "EPUB 文件不能超过 1 MB") {
		t.Fatalf("oversize upload = %d %s", tooBig.Status, string(tooBig.Body))
	}
	emptyUpload := env.postMultipart("/api/novelviz/books/upload", "file", "empty.epub", nil)
	if emptyUpload.Status != 400 || !strings.Contains(string(emptyUpload.Body), "上传的 EPUB 文件为空") {
		t.Fatalf("empty upload = %d %s", emptyUpload.Status, string(emptyUpload.Body))
	}

	content := fixtureEPUBBytes(t)
	good := env.postMultipart("/api/novelviz/books/upload", "file", "uploaded-story.epub", content)
	if good.Status != 201 {
		t.Fatalf("upload = %d %s", good.Status, string(good.Body))
	}
	uploaded := decodeDataMap(t, good.Body)
	if uploaded["source"] != "uploaded" || uploaded["display_name"] != "uploaded-story.epub" {
		t.Fatalf("uploaded book = %v", uploaded)
	}
	if int(uploaded["size"].(float64)) != len(content) {
		t.Fatalf("uploaded size = %v, want %d", uploaded["size"], len(content))
	}
	uploadedID := int64(uploaded["uploaded_book_id"].(float64))

	booksData := decodeDataList(t, env.get("/api/novelviz/books").Body)
	if len(booksData) != 2 || booksData[1]["id"] != fmt.Sprintf("uploaded:%d", uploadedID) {
		t.Fatalf("books = %v", booksData)
	}

	env.startWorker()
	created := env.postJSON("/api/novelviz/tasks", fmt.Sprintf(
		`{"book_source": "uploaded", "book_filename": "uploaded-story.epub", "uploaded_book_id": %d, "scene_mode": "targeted", "target_plot": "主角第一次进入星港"}`,
		uploadedID))
	if created.Status != 201 {
		t.Fatalf("uploaded task = %d %s", created.Status, string(created.Body))
	}
	taskID := createdTaskID(t, created.Body)
	env.waitForTerminal(t, taskID)
	task, err := env.service.loadTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	if task.Status != StatusSucceeded || len(task.Images) != 1 {
		t.Fatalf("uploaded task = %s with %d images", task.Status, len(task.Images))
	}
}

func TestStoreUploadedEPUBPath(t *testing.T) {
	env := newTestEnvironment(t)
	content := fixtureEPUBBytes(t)
	source := filepath.Join(t.TempDir(), "assembled.epub")
	if err := os.WriteFile(source, content, 0o644); err != nil {
		t.Fatal(err)
	}
	book, err := env.service.StoreUploadedEPUBPath(context.Background(), source, "session-story.epub", env.userID, "")
	if err != nil {
		t.Fatalf("store path variant: %v", err)
	}
	if book.Size != int64(len(content)) {
		t.Fatalf("stored size = %d, want %d", book.Size, len(content))
	}
	if !strings.HasPrefix(filepath.ToSlash(book.Path), "novelviz/uploads/users/") {
		t.Fatalf("storage path = %q", book.Path)
	}
	if _, err := env.service.resolveUploadedBookPath(context.Background(), *book.UploadedBookID, env.userID); err != nil {
		t.Fatalf("resolve uploaded book: %v", err)
	}
	if _, err := env.service.resolveUploadedBookPath(context.Background(), *book.UploadedBookID, env.userID+1); err == nil {
		t.Fatalf("another user must not resolve the book")
	}
	same := filepath.Join(t.TempDir(), "again.epub")
	if err := os.WriteFile(same, content, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := env.service.StoreUploadedEPUBPath(context.Background(), same, "session-story.epub", env.userID, "")
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if second.Path == book.Path {
		t.Fatalf("stored files must not collide")
	}
}

// ── test environment ──

type testResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

type upstreamRequest struct {
	path          string
	authorization string
	body          []byte
}

type testUpstream struct {
	mu       sync.Mutex
	recorded []upstreamRequest
}

func (u *testUpstream) requests() []upstreamRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]upstreamRequest(nil), u.recorded...)
}

func startFakeUpstream(t *testing.T, plan string) (*httptest.Server, *testUpstream) {
	t.Helper()
	upstream := &testUpstream{}
	payload := fixturePNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstream.mu.Lock()
		upstream.recorded = append(upstream.recorded, upstreamRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			body:          body,
		})
		upstream.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = io.WriteString(w, `{"choices": [{"message": {"role": "assistant", "content": `+jsonString(plan)+`}}]}`)
		case strings.HasSuffix(r.URL.Path, "/images/generations"):
			_, _ = io.WriteString(w, `{"created": 1777220495, "data": [{"b64_json": `+jsonString(base64.StdEncoding.EncodeToString(payload))+`}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, upstream
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// fixturePNG builds an opaque 64x64 truecolour PNG, the shape of the harness
// fixture (colour type 2, so previews render as JPEG).
func fixturePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 47, G: 107, B: 255, A: 255})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

func fixtureEPUBBytes(t *testing.T) []byte {
	t.Helper()
	path := writeFixtureEPUB(t, t.TempDir(), pythonFixtureBody)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return content
}

func imageDimensions(t *testing.T, content []byte) (int, int) {
	t.Helper()
	if len(content) > 24 && string(content[:8]) == "\x89PNG\r\n\x1a\n" {
		return int(content[16])<<24 | int(content[17])<<16 | int(content[18])<<8 | int(content[19]),
			int(content[20])<<24 | int(content[21])<<16 | int(content[22])<<8 | int(content[23])
	}
	if len(content) > 3 && content[0] == 0xFF && content[1] == 0xD8 {
		index := 2
		for index+9 < len(content) {
			if content[index] != 0xFF {
				index++
				continue
			}
			marker := content[index+1]
			length := int(content[index+2])<<8 | int(content[index+3])
			if marker >= 0xC0 && marker <= 0xC3 {
				return int(content[index+7])<<8 | int(content[index+8]),
					int(content[index+5])<<8 | int(content[index+6])
			}
			index += 2 + length
		}
	}
	t.Fatalf("unrecognised image payload")
	return 0, 0
}

func decodeDataMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	return envelope.Data
}

func decodeDataList(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, body)
	}
	return envelope.Data
}

func createdTaskID(t *testing.T, body []byte) int64 {
	t.Helper()
	return int64(decodeDataMap(t, body)["id"].(float64))
}

type testPrincipal struct{ id int64 }

func (p testPrincipal) ID() int64            { return p.id }
func (p testPrincipal) Username() string     { return "diff-user" }
func (p testPrincipal) IsOperator() bool     { return false }
func (p testPrincipal) IsActiveStatus() bool { return true }
func (p testPrincipal) Email() string        { return "diff@example.com" }

type testEnvironment struct {
	t         *testing.T
	db        *db.DB
	cfg       *config.Config
	service   *Service
	app       *httptest.Server
	client    *http.Client
	userID    int64
	csrf      string
	sessionID string
	mediaRoot string
	bookDir   string
	upstream  *testUpstream
	store     *httpapi.SessionStore
}

func newTestEnvironment(t *testing.T) *testEnvironment {
	return newTestEnvironmentWithLimits(t, "120000", "4000")
}

func newTestEnvironmentWithLimits(t *testing.T, payloadMaxChars, fieldMaxChars string) *testEnvironment {
	t.Helper()
	t.Setenv("NOVELVIZ_EVENT_LOG_LIMIT", "120")
	t.Setenv("NOVELVIZ_EVENT_PAYLOAD_MAX_CHARS", payloadMaxChars)
	t.Setenv("NOVELVIZ_EVENT_FIELD_MAX_CHARS", fieldMaxChars)
	t.Setenv("NOVELVIZ_UPLOAD_DIR", "novelviz/uploads")

	root := t.TempDir()
	handle, err := db.Open(filepath.Join(root, "test.sqlite3"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mediaRoot := filepath.Join(root, "media")
	bookDir := filepath.Join(root, "novel")
	for _, dir := range []string{mediaRoot, bookDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	writeFixtureEPUB(t, bookDir, pythonFixtureBody)

	upstreamServer, upstreamRecorder := startFakeUpstream(t, scenePlanJSON)
	cfg := &config.Config{
		MediaRoot:                mediaRoot,
		NovelvizBookDir:          bookDir,
		NovelvizMaxEpubBytes:     1024 * 1024,
		NovelvizMaxScenes:        10,
		NovelvizImageConcurrency: 1,
		NovelvizImageMaxRetries:  2,
		NovelvizImageTimeout:     30 * time.Second,
		NovelvizTextMaxRetries:   2,
		NovelvizTextReadTimeout:  30 * time.Second,
		NovelvizSSEPollInterval:  250 * time.Millisecond,
		NovelvizStallRequeue:     60 * time.Second,
		NovelvizAPIBaseURL:       upstreamServer.URL + "/v1",
		NovelvizImageSize:        "1024x1024",
		NovelvizImageQuality:     "high",
		NovelvizImageModel:       "diff-image-model",
		NovelvizImageAPIPath:     "/images/generations",
		NovelvizImageAPIKey:      "diff-image-key",
		NovelvizTextModel:        "diff-llm-model",
		NovelvizTextAPIPath:      "/chat/completions",
		NovelvizTextAPIKey:       "diff-llm-key",
		ImagePreviewMaxDimension: 768,
		Debug:                    true,
	}
	resolver := &stubResolver{channel: &Channel{
		BaseURL:   upstreamServer.URL + "/v1",
		APIPath:   "/chat/completions",
		ModelName: "diff-llm-model",
		Name:      "默认 LLM 渠道",
		APIKey:    "diff-llm-key",
	}}
	service := NewService(Deps{DB: handle, Config: cfg, Generator: resolver, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(func() {
		service.StopWorker()
		handle.Close()
	})

	result, err := handle.Exec(`INSERT INTO users_user (password, is_superuser, username, first_name, last_name, is_staff,
		is_active, date_joined, role, status) VALUES ('x', 0, 'diff-user', '', '', 0, 1, ?, 'user', 'active')`,
		db.FormatTime(time.Now().UTC()))
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}

	store := httpapi.NewSessionStore(handle, false)
	session := store.Load(httptest.NewRequest(http.MethodGet, "/", nil))
	session.SetInt("user_id", userID)
	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	environment := &testEnvironment{
		t:         t,
		db:        handle,
		cfg:       cfg,
		service:   service,
		userID:    userID,
		mediaRoot: mediaRoot,
		bookDir:   bookDir,
		upstream:  upstreamRecorder,
		store:     store,
		sessionID: session.Key,
	}
	environment.app = environment.serveFor(service)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	parsed, err := url.Parse(environment.app.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	jar.SetCookies(parsed, []*http.Cookie{
		{Name: httpapi.SessionCookieName, Value: session.Key, Path: "/"},
	})
	environment.client = &http.Client{Jar: jar}
	// The token comes from the same endpoint the SPA uses before any write.
	environment.get("/api/auth/csrf")
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == httpapi.CSRFCookieName {
			environment.csrf = cookie.Value
		}
	}
	if environment.csrf == "" {
		t.Fatalf("csrf cookie was not issued")
	}
	return environment
}

// serveFor wraps a service in its own middleware chain so cold instances can be
// exercised next to the warm one.
func (e *testEnvironment) serveFor(svc *Service) *httptest.Server {
	e.t.Helper()
	mux := http.NewServeMux()
	svc.NewHandlers().Register(mux)
	// The production mux serves /api/auth/csrf from internal/auth; the novel
	// package's tests stand up just the novel routes, so the same endpoint is
	// reproduced here. Django only emits the csrftoken cookie on responses that
	// ask for the token, so the write endpoints below need a route that does.
	mux.HandleFunc("/api/auth/csrf", func(w http.ResponseWriter, r *http.Request) {
		httpapi.SetCSRFCookie(w)
		httpapi.OK(w, map[string]any{"csrf_token": httpapi.CSRFTokenOf(r).Masked})
	})
	middleware := httpapi.NewMiddleware(httpapi.Options{
		Config:   e.cfg,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sessions: e.store,
		ResolveUser: func(ctx context.Context, id int64) (httpapi.Principal, error) {
			return testPrincipal{id: id}, nil
		},
	}, mux)
	server := httptest.NewServer(middleware)
	e.t.Cleanup(server.Close)
	return server
}

type stubResolver struct{ channel *Channel }

func (s *stubResolver) ResolveChannel(ctx context.Context, code string) (*Channel, error) {
	return s.channel, nil
}

func (e *testEnvironment) startWorker() { e.service.StartWorker(context.Background()) }

// freshService builds a second service over the same database to exercise the
// cold hub path.
func (e *testEnvironment) freshService() *Service {
	return NewService(Deps{
		DB:        e.db,
		Config:    e.cfg,
		Generator: &stubResolver{channel: &Channel{Name: "默认 LLM 渠道", APIKey: "diff-llm-key", ModelName: "diff-llm-model"}},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func (e *testEnvironment) do(baseURL, method, path string, body io.Reader, contentType string) testResponse {
	e.t.Helper()
	request, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		e.t.Fatalf("build request: %v", err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet && e.csrf != "" {
		request.Header.Set("X-CSRFToken", e.csrf)
	}
	response, err := e.client.Do(request)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}
	return testResponse{Status: response.StatusCode, Header: response.Header, Body: content}
}

func (e *testEnvironment) get(path string) testResponse {
	return e.do(e.app.URL, http.MethodGet, path, nil, "")
}

func (e *testEnvironment) delete(path string) testResponse {
	return e.do(e.app.URL, http.MethodDelete, path, nil, "")
}

func (e *testEnvironment) postJSON(path, body string) testResponse {
	return e.do(e.app.URL, http.MethodPost, path, strings.NewReader(body), "application/json")
}

func (e *testEnvironment) postMultipart(path, field, filename string, content []byte) testResponse {
	e.t.Helper()
	if field == "" {
		return e.do(e.app.URL, http.MethodPost, path, strings.NewReader(""), "")
	}
	boundary := "test-boundary"
	buffer := &bytes.Buffer{}
	buffer.WriteString("--" + boundary + "\r\n")
	buffer.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=%q; filename=%q\r\n", field, filename))
	buffer.WriteString("Content-Type: application/epub+zip\r\n\r\n")
	buffer.Write(content)
	buffer.WriteString("\r\n--" + boundary + "--\r\n")
	return e.do(e.app.URL, http.MethodPost, path, buffer, "multipart/form-data; boundary="+boundary)
}

func (e *testEnvironment) insertTask(status string, overrides map[string]any) int64 {
	e.t.Helper()
	now := time.Now().UTC()
	bookFilename := "diff-fixture.epub"
	progress := any(1)
	lastEventID := int64(0)
	logs := "[]"
	var startedAt, finishedAt any
	for key, value := range overrides {
		switch key {
		case "progress":
			progress = value
		case "last_event_id":
			lastEventID = int64(value.(int))
		case "logs":
			logs = value.(string)
		case "started_at":
			startedAt = value
		case "finished_at":
			finishedAt = value
		case "book_filename":
			bookFilename = value.(string)
		}
	}
	result, err := e.db.Exec(`INSERT INTO novelviz_novelvisualizationtask (created_at, updated_at, book_filename, status,
		progress_percent, logs, scenes_json, error_message, started_at, finished_at, last_event_id, user_id,
		llm_channel_code, book_source, uploaded_book_id, scene_mode, target_plot)
		VALUES (?, ?, ?, ?, ?, ?, '[]', '', ?, ?, ?, ?, '', 'builtin', NULL, 'auto', '')`,
		db.FormatTime(now), db.FormatTime(now), bookFilename, status, progress, logs, startedAt, finishedAt, lastEventID, e.userID)
	if err != nil {
		e.t.Fatalf("insert task: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		e.t.Fatalf("task id: %v", err)
	}
	return id
}

func (e *testEnvironment) submitTask(t *testing.T) int64 {
	t.Helper()
	response := e.postJSON("/api/novelviz/tasks", `{"book_filename": "diff-fixture.epub", "scene_mode": "auto"}`)
	if response.Status != 201 {
		t.Fatalf("submit = %d %s", response.Status, string(response.Body))
	}
	return createdTaskID(t, response.Body)
}

func (e *testEnvironment) waitForTerminal(t *testing.T, taskID int64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := e.db.QueryRow(`SELECT status FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID).Scan(&status); err != nil {
			t.Fatalf("poll task: %v", err)
		}
		if status == StatusSucceeded || status == StatusFailed || status == StatusCanceled {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var status string
	_ = e.db.QueryRow(`SELECT status FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID).Scan(&status)
	t.Fatalf("task %d did not settle, status=%s", taskID, status)
}
