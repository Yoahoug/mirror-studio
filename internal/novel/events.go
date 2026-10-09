package novel

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is one entry of the task log, exactly as the reference stores it.
type Event struct {
	ID        int64
	Type      string
	Payload   *pyObj
	CreatedAt string
}

func (e Event) pyValue() *pyObj {
	payload := e.Payload
	if payload == nil {
		payload = newPyObj()
	}
	return newPyObj().
		Set("id", e.ID).
		Set("type", e.Type).
		Set("payload", payload).
		Set("created_at", e.CreatedAt)
}

// eventsJSON renders the log array the way Django's JSONField stores it:
// ensure_ascii=True and ", " separators.
func eventsJSON(events []Event) (string, error) {
	items := make([]any, 0, len(events))
	for _, event := range events {
		items = append(items, event.pyValue())
	}
	return marshalPython(items, true)
}

func eventsFromJSON(raw string) ([]Event, error) {
	text := strings.TrimSpace(raw)
	if text == "" || text == "null" {
		return nil, nil
	}
	decoded, err := decodePyJSON([]byte(text))
	if err != nil {
		return nil, err
	}
	items, ok := decoded.([]any)
	if !ok {
		return nil, nil
	}
	events := make([]Event, 0, len(items))
	for _, item := range items {
		object, ok := item.(*pyObj)
		if !ok {
			continue
		}
		event := Event{}
		if value, ok := object.Get("id"); ok {
			if parsed, ok := pythonInt(value); ok {
				event.ID = int64(parsed)
			}
		}
		if value, ok := object.Get("type"); ok {
			event.Type = pyString(value)
		}
		if payload := object.Child("payload"); payload != nil {
			event.Payload = payload
		}
		if value, ok := object.Get("created_at"); ok {
			event.CreatedAt = pyString(value)
		}
		events = append(events, event)
	}
	return events, nil
}

// truncateEventString mirrors services._truncate_event_string.
func truncateEventString(value string, maxChars int) string {
	if len([]rune(value)) <= maxChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxChars]) + "...[truncated " + strconv.Itoa(len(runes)-maxChars) + " chars]"
}

// sanitizeEventPayload mirrors services._sanitize_event_payload: drop the
// "logs" key unless it is empty, cap dict keys at 120 characters and lists at
// 50 items, and truncate strings to the configured field length.
func sanitizeEventPayload(value any, maxChars int) any {
	switch typed := value.(type) {
	case string:
		return truncateEventString(typed, maxChars)
	case *pyObj:
		sanitized := newPyObj()
		for index, key := range typed.Keys() {
			item, _ := typed.Get(key)
			if key == "logs" {
				if isEmptyLogsValue(item) {
					sanitized.Set("logs", []any{})
				}
				continue
			}
			sanitized.Set(truncateRunes(key, 120), sanitizeEventPayload(item, maxChars))
			_ = index
		}
		return sanitized
	case []any:
		limited := typed
		if len(limited) > 50 {
			limited = limited[:50]
		}
		result := make([]any, 0, len(limited))
		for _, item := range limited {
			result = append(result, sanitizeEventPayload(item, maxChars))
		}
		return result
	}
	return value
}

func isEmptyLogsValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		return len(typed) == 0
	}
	return false
}

// fitEventPayload mirrors services._fit_event_payload.
func (s *Service) fitEventPayload(payload *pyObj) *pyObj {
	sanitized, _ := sanitizeEventPayload(payload, s.settings.fieldMaxChars).(*pyObj)
	if sanitized == nil {
		sanitized = newPyObj().Set("value", payload)
	}
	encoded, err := marshalPython(sanitized, false)
	if err == nil && len([]rune(encoded)) <= s.settings.payloadMaxChars {
		return sanitized
	}
	message := ""
	if value, ok := sanitized.Get("message"); ok {
		message = pyString(value)
	}
	if message == "" {
		if value, ok := sanitized.Get("error"); ok {
			message = pyString(value)
		}
	}
	if message == "" {
		message = "事件内容过大，已裁剪"
	}
	return newPyObj().
		Set("message", truncateEventString(message, s.settings.fieldMaxChars)).
		Set("truncated", true).
		Set("original_chars", len([]rune(encoded)))
}

// taskSnapshot is the event-hub view of one task: the reference reads these
// three columns once per poll (review P1-26 keeps them in memory instead).
type taskSnapshot struct {
	Loaded  bool
	Missing bool
	Status  string
	LastID  int64
	Events  []Event
}

// eventHub mirrors each task's log to memory and wakes waiting SSE handlers
// with sync.Cond. The database stays authoritative; a cold hub entry is filled
// from the stored row on first use.
type eventHub struct {
	mu    sync.Mutex
	cond  *sync.Cond
	tasks map[int64]*taskSnapshot
	limit int
}

func newEventHub(limit int) *eventHub {
	hub := &eventHub{tasks: map[int64]*taskSnapshot{}, limit: limit}
	hub.cond = sync.NewCond(&hub.mu)
	return hub
}

// pulse wakes the SSE waiters at the configured poll interval so status changes
// and heartbeats are observed with the same latency as the reference poll loop.
func (h *eventHub) pulse(interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			h.mu.Lock()
			h.cond.Broadcast()
			h.mu.Unlock()
		}
	}
}

func (h *eventHub) notify() {
	h.mu.Lock()
	h.cond.Broadcast()
	h.mu.Unlock()
}

func (h *eventHub) snapshot(taskID int64) taskSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.tasks[taskID]
	if state == nil {
		return taskSnapshot{}
	}
	return taskSnapshot{
		Loaded:  state.Loaded,
		Missing: state.Missing,
		Status:  state.Status,
		LastID:  state.LastID,
		Events:  append([]Event(nil), state.Events...),
	}
}

// store records the task state the handler will serve on its next wake-up.
func (h *eventHub) store(taskID int64, status string, lastID int64, events []Event) {
	trimmed := events
	if len(trimmed) > h.limit {
		trimmed = trimmed[len(trimmed)-h.limit:]
	}
	h.mu.Lock()
	h.tasks[taskID] = &taskSnapshot{Loaded: true, Status: status, LastID: lastID, Events: append([]Event(nil), trimmed...)}
	h.mu.Unlock()
	h.notify()
}

// setStatus publishes a status change to streams already serving this task.
func (h *eventHub) setStatus(taskID int64, status string) {
	h.mu.Lock()
	if state := h.tasks[taskID]; state != nil && state.Loaded && !state.Missing {
		state.Status = status
	}
	h.mu.Unlock()
	h.notify()
}

func (h *eventHub) markMissing(taskID int64) {
	h.mu.Lock()
	h.tasks[taskID] = &taskSnapshot{Loaded: true, Missing: true}
	h.mu.Unlock()
	h.notify()
}

func (h *eventHub) invalidate(taskID int64) {
	h.mu.Lock()
	delete(h.tasks, taskID)
	h.mu.Unlock()
	h.notify()
}

// wait blocks until the next pulse or notification.
func (h *eventHub) wait() {
	h.mu.Lock()
	h.cond.Wait()
	h.mu.Unlock()
}
