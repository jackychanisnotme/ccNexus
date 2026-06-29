package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/providercompat"
	"github.com/lich0821/ccNexus/internal/tokencount"
	"github.com/lich0821/ccNexus/internal/transformer"
)

const (
	streamFinishCompleted             = "completed"
	streamFinishClientCanceled        = "client_canceled"
	streamFinishUpstreamStreamError   = "upstream_stream_error"
	streamFinishDownstreamWriteFailed = "downstream_write_failed"
	streamFinishTransformFailed       = "transform_failed"
	streamFinishMissingResponsesDone  = "missing_response_completed"
	maxStreamEventBytes               = 128 * 1024 * 1024
)

const openAIResponsesWaitingID = "resp_ainexus_waiting"

func openAIResponsesWaitingCreatedEvent() []byte {
	return []byte(fmt.Sprintf(
		"data: {\"type\":\"response.created\",\"sequence_number\":1,\"response\":{\"id\":\"%s\",\"object\":\"response\",\"status\":\"in_progress\",\"created_at\":0,\"model\":\"\",\"output\":[],\"parallel_tool_calls\":false}}\n\n",
		openAIResponsesWaitingID,
	))
}

type downstreamStreamSession struct {
	w                       http.ResponseWriter
	flusher                 http.Flusher
	heartbeatInterval       time.Duration
	clientFormat            ClientFormat
	done                    chan struct{}
	mu                      sync.Mutex
	closeOnce               sync.Once
	heartbeatOnce           sync.Once
	started                 bool
	closed                  bool
	responsesCreatedWritten bool
	responsesWaitingCreated bool
	synthesizeMissingDone   bool
}

func newDownstreamStreamSession(w http.ResponseWriter, heartbeatInterval time.Duration, clientFormat ClientFormat) *downstreamStreamSession {
	flusher, _ := w.(http.Flusher)
	return &downstreamStreamSession{
		w:                     w,
		flusher:               flusher,
		heartbeatInterval:     heartbeatInterval,
		clientFormat:          clientFormat,
		done:                  make(chan struct{}),
		synthesizeMissingDone: true,
	}
}

func (s *downstreamStreamSession) Start() error {
	if s == nil {
		return nil
	}
	if s.flusher == nil {
		return fmt.Errorf("response writer does not support flushing")
	}

	shouldStartHeartbeat := false
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("downstream stream is closed")
	}
	if !s.started {
		header := s.w.Header()
		header.Set("Content-Type", "text/event-stream; charset=utf-8")
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Accel-Buffering", "no")
		s.w.WriteHeader(http.StatusOK)
		if _, err := s.w.Write([]byte(": AINexus waiting for upstream\n\n")); err != nil {
			s.mu.Unlock()
			return err
		}
		s.flusher.Flush()
		s.started = true
		shouldStartHeartbeat = s.heartbeatInterval > 0
	}
	s.mu.Unlock()

	if shouldStartHeartbeat {
		s.heartbeatOnce.Do(func() {
			go s.heartbeatLoop()
		})
	}
	return nil
}

func (s *downstreamStreamSession) Started() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func (s *downstreamStreamSession) Write(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := s.Start(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("downstream stream is closed")
	}
	if s.clientFormat == ClientFormatOpenAIResponses {
		data = s.filterDuplicateOpenAIResponsesCreatedLocked(data)
		if len(data) == 0 {
			return nil
		}
	}
	if _, err := s.w.Write(data); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *downstreamStreamSession) WriteError(message string) error {
	return s.WriteTypedError("service_unavailable", message)
}

func (s *downstreamStreamSession) WriteTypedError(errorType string, message string) error {
	if strings.TrimSpace(errorType) == "" {
		errorType = "service_unavailable"
	}
	if strings.TrimSpace(message) == "" {
		message = "stream failed"
	}
	payload, err := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	})
	if err != nil {
		return err
	}
	return s.Write([]byte(fmt.Sprintf("event: error\ndata: %s\n\n", payload)))
}

func (s *downstreamStreamSession) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	})
}

func (s *downstreamStreamSession) heartbeatLoop() {
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = s.writeHeartbeat()
		case <-s.done:
			return
		}
	}
}

// writeHeartbeat emits a protocol-valid keep-alive for the client format. Claude
// SSE clients (e.g. Codex Desktop on /v1/messages) do not treat a bare SSE
// comment as progress, so send an Anthropic ping event instead; OpenAI
// Responses API clients (e.g. Hermes / Python SDK) require the first event to
// be response.created or the SDK raises RuntimeError and cancels; other
// OpenAI-format clients accept the comment keep-alive.
func (s *downstreamStreamSession) writeHeartbeat() error {
	if s != nil && s.clientFormat == ClientFormatClaude {
		return s.Write([]byte("event: ping\ndata: {\"type\": \"ping\"}\n\n"))
	}
	if s != nil && s.clientFormat == ClientFormatOpenAIResponses {
		wroteCreated, err := s.writeOpenAIResponsesWaitingCreatedIfNeeded()
		if err != nil || wroteCreated {
			return err
		}
	}
	return s.writeComment("AINexus waiting for upstream")
}

func (s *downstreamStreamSession) writeOpenAIResponsesWaitingCreatedIfNeeded() (bool, error) {
	if err := s.Start(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, fmt.Errorf("downstream stream is closed")
	}
	if s.responsesCreatedWritten {
		return false, nil
	}
	if _, err := s.w.Write(openAIResponsesWaitingCreatedEvent()); err != nil {
		return false, err
	}
	s.responsesCreatedWritten = true
	s.responsesWaitingCreated = true
	s.flusher.Flush()
	return true, nil
}

func (s *downstreamStreamSession) primeStreamContext(ctx *transformer.StreamContext) {
	if s == nil || ctx == nil || s.clientFormat != ClientFormatOpenAIResponses {
		return
	}
	s.mu.Lock()
	waitingCreated := s.responsesWaitingCreated
	s.mu.Unlock()
	if !waitingCreated {
		return
	}
	ctx.MessageStartSent = true
	ctx.MessageID = openAIResponsesWaitingID
	ctx.ResponseSequenceNumber = 1
}

// filterDuplicateOpenAIResponsesCreatedLocked filters out duplicate response.created
// SSE events from OpenAI Responses streams. Callers must pass data that contains
// complete SSE blocks separated by \n\n; partial writes will split incorrectly.
func (s *downstreamStreamSession) filterDuplicateOpenAIResponsesCreatedLocked(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	parts := strings.SplitAfter(string(data), "\n\n")
	var filtered strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		if sseBlockEventType(part) == "response.created" {
			if s.responsesCreatedWritten {
				if s.responsesWaitingCreated {
					s.responsesWaitingCreated = false
					filtered.WriteString(part)
				}
				continue
			}
			s.responsesCreatedWritten = true
		}
		filtered.WriteString(part)
	}
	return []byte(filtered.String())
}

func sseBlockEventType(block string) string {
	eventName := ""
	scanner := bufio.NewScanner(strings.NewReader(block))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "" || jsonData == "[DONE]" {
			continue
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &payload); err != nil {
			continue
		}
		eventType, _ := payload["type"].(string)
		if strings.TrimSpace(eventType) != "" {
			return eventType
		}
	}
	return eventName
}

type upstreamSSEError struct {
	Type     string
	Message  string
	RawEvent []byte
}

func (e *upstreamSSEError) Error() string {
	if e == nil {
		return "upstream SSE error"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if strings.TrimSpace(e.Type) != "" {
		return "upstream SSE error: " + e.Type
	}
	return "upstream SSE error"
}

func (e *upstreamSSEError) IsRequestScoped() bool {
	if e == nil {
		return false
	}
	errorType := strings.ToLower(strings.TrimSpace(e.Type))
	return errorType == "invalid_request_error" ||
		errorType == "invalid_request" ||
		isRequestScopedInvalidRequest(http.StatusBadRequest, string(e.RawEvent))
}

func parseUpstreamSSEError(eventData []byte) *upstreamSSEError {
	eventName := ""
	var payload map[string]interface{}
	scanner := bufio.NewScanner(bytes.NewReader(eventData))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "event:") {
			eventName = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "event:")))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "" || jsonData == "[DONE]" {
			continue
		}
		if err := json.Unmarshal([]byte(jsonData), &payload); err == nil {
			break
		}
	}
	if payload == nil {
		return nil
	}

	payloadType, _ := payload["type"].(string)
	rawError, hasError := payload["error"]
	if eventName != "error" && !hasError && !strings.EqualFold(strings.TrimSpace(payloadType), "error") {
		return nil
	}

	result := &upstreamSSEError{RawEvent: append([]byte(nil), eventData...)}
	if errorMap, ok := rawError.(map[string]interface{}); ok {
		result.Type, _ = errorMap["type"].(string)
		if strings.TrimSpace(result.Type) == "" {
			result.Type, _ = errorMap["code"].(string)
		}
		result.Message, _ = errorMap["message"].(string)
	} else if message, ok := rawError.(string); ok {
		result.Message = message
	}
	if strings.TrimSpace(result.Type) == "" && !strings.EqualFold(strings.TrimSpace(payloadType), "error") {
		result.Type = payloadType
	}
	if strings.TrimSpace(result.Type) == "" {
		result.Type = eventName
	}
	if strings.TrimSpace(result.Message) == "" {
		result.Message, _ = payload["message"].(string)
	}
	return result
}

func responseRequestPath(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	return resp.Request.URL.Path
}

func (s *downstreamStreamSession) writeComment(message string) error {
	if err := s.Start(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("downstream stream is closed")
	}
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", message); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

type streamResponseResult struct {
	InputTokens               int
	OutputTokens              int
	OutputText                string
	Completed                 bool
	WroteData                 bool
	WroteSemanticData         bool
	FirstTransformedEventType string
	LastTransformedEventType  string
	ResponsesCompletionSafe   openAIResponsesCompletionState
	Reason                    string
	Err                       error
}

type openAIResponsesCompletionState struct {
	ResponseID         string
	Model              string
	ItemID             string
	Text               string
	SequenceNumber     int
	Completed          bool
	Unsafe             bool
	UnsafeReason       string
	LastEventType      string
	LastOutputItemType string
	OutputItems        map[int]*openAIResponsesOutputItemState
	ToolSeen           bool
	ToolItemDone       bool
	ToolArgumentsDone  bool
	ToolRecoverable    bool
	ToolPending        bool
	StructuralUnsafe   bool
	StructuralReason   string
	OutputItemCount    int
}

type openAIResponsesOutputItemState struct {
	Index              int
	Item               map[string]interface{}
	ItemType           string
	Done               bool
	ArgumentsDone      bool
	ArgumentsDeltaSeen bool
	InputDone          bool
	InputDeltaSeen     bool
	Input              string
}

func (s openAIResponsesCompletionState) canSynthesizeCompleted() bool {
	return !s.Completed && !s.Unsafe && (strings.TrimSpace(s.Text) != "" || s.ToolRecoverable)
}

func (s openAIResponsesCompletionState) canSynthesizeInterruptedCustomToolInputCompleted() bool {
	_, ok := s.interruptedCustomToolInputCompletionState()
	return ok
}

func (s openAIResponsesCompletionState) completedEvent(inputTokens, outputTokens int) []byte {
	responseID := strings.TrimSpace(s.ResponseID)
	if responseID == "" {
		responseID = "resp_ainexus_recovered"
	}
	itemID := strings.TrimSpace(s.ItemID)
	if itemID == "" {
		itemID = "msg_ainexus_recovered_0"
	}
	if outputTokens <= 0 {
		outputTokens = tokencount.EstimateOutputTokens(s.Text)
	}
	totalTokens := inputTokens + outputTokens
	output := s.completedOutputItems()
	if len(output) == 0 {
		output = []interface{}{
			map[string]interface{}{
				"type":   "message",
				"id":     itemID,
				"status": "completed",
				"role":   "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type": "output_text",
						"text": s.Text,
					},
				},
			},
		}
	}
	payload := map[string]interface{}{
		"type":            "response.completed",
		"sequence_number": s.SequenceNumber + 1,
		"response": map[string]interface{}{
			"id":                  responseID,
			"object":              "response",
			"created_at":          0,
			"model":               strings.TrimSpace(s.Model),
			"status":              "completed",
			"parallel_tool_calls": len(output) > 1,
			"output":              output,
			"usage": map[string]interface{}{
				"input_tokens":  inputTokens,
				"output_tokens": outputTokens,
				"total_tokens":  totalTokens,
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return []byte("data: " + string(data) + "\n\n")
}

func (s openAIResponsesCompletionState) completedOutputItems() []interface{} {
	if len(s.OutputItems) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(s.OutputItems))
	for index := range s.OutputItems {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	output := make([]interface{}, 0, len(indexes))
	for _, index := range indexes {
		itemState := s.OutputItems[index]
		if itemState == nil || !itemState.Done || !isRecoverableOpenAIResponsesToolItem(itemState.Item) {
			continue
		}
		output = append(output, cloneOpenAIResponsesMap(itemState.Item))
	}
	return output
}

func (s openAIResponsesCompletionState) clone() openAIResponsesCompletionState {
	cloned := s
	if s.OutputItems == nil {
		return cloned
	}
	cloned.OutputItems = make(map[int]*openAIResponsesOutputItemState, len(s.OutputItems))
	for index, itemState := range s.OutputItems {
		if itemState == nil {
			continue
		}
		itemClone := *itemState
		itemClone.Item = cloneOpenAIResponsesMap(itemState.Item)
		cloned.OutputItems[index] = &itemClone
	}
	return cloned
}

func (s openAIResponsesCompletionState) interruptedCustomToolInputCompletionState() (openAIResponsesCompletionState, bool) {
	if s.Completed || s.StructuralUnsafe || len(s.OutputItems) == 0 {
		return s, false
	}

	completed := s.clone()
	recovered := false
	for _, itemState := range completed.OutputItems {
		if itemState == nil {
			continue
		}
		itemType := strings.ToLower(strings.TrimSpace(itemState.ItemType))
		if itemType != "custom_tool_call" {
			continue
		}
		if itemState.Done && itemState.InputDone && isRecoverableCustomToolCallItem(itemState.Item) {
			continue
		}
		if !itemState.InputDeltaSeen || strings.TrimSpace(itemState.Input) == "" {
			continue
		}
		if itemState.Item == nil {
			continue
		}
		if !hasNonEmptyString(itemState.Item["name"]) {
			continue
		}
		if !hasNonEmptyString(itemState.Item["id"]) && !hasNonEmptyString(itemState.Item["call_id"]) {
			continue
		}

		itemState.Item["input"] = itemState.Input
		itemState.Item["status"] = "completed"
		itemState.Done = true
		itemState.InputDone = true
		completed.ToolItemDone = true
		completed.ToolArgumentsDone = true
		recovered = true
	}
	if !recovered {
		return s, false
	}

	completed.updateToolSafety()
	if completed.Unsafe || !completed.canSynthesizeCompleted() {
		return s, false
	}
	return completed, true
}

func openAIResponsesSSEEvent(event map[string]interface{}) []byte {
	data, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return []byte("data: " + string(data) + "\n\n")
}

func (s openAIResponsesCompletionState) interruptedCustomToolInputTerminalEvents(inputTokens, outputTokens int) []byte {
	completed, ok := s.interruptedCustomToolInputCompletionState()
	if !ok {
		return nil
	}

	indexes := make([]int, 0, len(completed.OutputItems))
	for index := range completed.OutputItems {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	sequenceNumber := s.SequenceNumber
	var events bytes.Buffer
	for _, index := range indexes {
		originalItemState := s.OutputItems[index]
		itemState := completed.OutputItems[index]
		if originalItemState == nil || itemState == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(originalItemState.ItemType)) != "custom_tool_call" {
			continue
		}
		if !originalItemState.InputDeltaSeen || strings.TrimSpace(originalItemState.Input) == "" || originalItemState.InputDone {
			continue
		}
		if !itemState.Done || !itemState.InputDone || !isRecoverableCustomToolCallItem(itemState.Item) {
			continue
		}

		sequenceNumber++
		inputDoneEvent := map[string]interface{}{
			"type":            "response.custom_tool_call_input.done",
			"sequence_number": sequenceNumber,
			"output_index":    index,
			"input":           itemState.Input,
		}
		if itemID := strings.TrimSpace(stringFromInterface(itemState.Item["id"])); itemID != "" {
			inputDoneEvent["item_id"] = itemID
		}
		events.Write(openAIResponsesSSEEvent(inputDoneEvent))

		sequenceNumber++
		outputItemDoneEvent := map[string]interface{}{
			"type":            "response.output_item.done",
			"sequence_number": sequenceNumber,
			"output_index":    index,
			"item":            cloneOpenAIResponsesMap(itemState.Item),
		}
		events.Write(openAIResponsesSSEEvent(outputItemDoneEvent))
	}
	if events.Len() == 0 {
		return nil
	}

	completed.SequenceNumber = sequenceNumber
	events.Write(completed.completedEvent(inputTokens, outputTokens))
	return events.Bytes()
}

func cloneOpenAIResponsesMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned map[string]interface{}
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}

func (s *openAIResponsesCompletionState) observe(eventData []byte) {
	if s == nil {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(eventData))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "" || jsonData == "[DONE]" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}
		s.observeJSON(event)
	}
}

func (s *openAIResponsesCompletionState) observeJSON(event map[string]interface{}) {
	if s == nil || event == nil {
		return
	}
	if seq := parseTokenNumber(event["sequence_number"]); seq > s.SequenceNumber {
		s.SequenceNumber = seq
	}
	eventType, _ := event["type"].(string)
	if strings.TrimSpace(eventType) != "" {
		s.LastEventType = eventType
	}

	switch eventType {
	case "response.created":
		if response, ok := event["response"].(map[string]interface{}); ok {
			if id, ok := response["id"].(string); ok && strings.TrimSpace(id) != "" {
				s.ResponseID = id
			}
			if model, ok := response["model"].(string); ok && strings.TrimSpace(model) != "" {
				s.Model = model
			}
		}
	case "response.completed":
		s.Completed = true
	case "response.output_text.delta":
		if delta, ok := event["delta"].(string); ok {
			s.Text += delta
		}
		s.captureItemID(event)
	case "response.output_text.done":
		if text, ok := event["text"].(string); ok && s.Text == "" {
			s.Text = text
		}
		s.captureItemID(event)
	case "response.content_part.added", "response.content_part.done":
		s.captureItemID(event)
		if part, ok := event["part"].(map[string]interface{}); ok {
			partType, _ := part["type"].(string)
			if partType != "" && partType != "output_text" {
				s.markStructuralUnsafe("non_message_content")
			}
			if text, ok := part["text"].(string); ok && eventType == "response.content_part.done" && s.Text == "" {
				s.Text = text
			}
		}
	case "response.function_call_arguments.delta":
		s.observeFunctionCallArguments(event, false)
	case "response.function_call_arguments.done":
		s.observeFunctionCallArguments(event, true)
	case "response.custom_tool_call_input.delta":
		s.observeCustomToolCallInput(event, false)
	case "response.custom_tool_call_input.done":
		s.observeCustomToolCallInput(event, true)
	case "response.output_item.added", "response.output_item.done":
		if item, ok := event["item"].(map[string]interface{}); ok {
			itemType := strings.ToLower(strings.TrimSpace(stringFromInterface(item["type"])))
			if strings.TrimSpace(itemType) != "" {
				s.LastOutputItemType = itemType
			}
			if itemType == "message" && s.Text == "" {
				s.Text = openAIResponsesOutputItemText(item)
			}
			if id, ok := item["id"].(string); ok && strings.TrimSpace(id) != "" && s.ItemID == "" {
				s.ItemID = id
			}
			s.observeOutputItem(event, item, eventType == "response.output_item.done")
		}
	}
	s.updateToolSafety()
}

func (s *openAIResponsesCompletionState) markStructuralUnsafe(reason string) {
	if s == nil {
		return
	}
	s.StructuralUnsafe = true
	if s.StructuralReason == "" {
		s.StructuralReason = reason
	}
}

func (s *openAIResponsesCompletionState) ensureOutputItem(index int) *openAIResponsesOutputItemState {
	if s.OutputItems == nil {
		s.OutputItems = make(map[int]*openAIResponsesOutputItemState)
	}
	itemState := s.OutputItems[index]
	if itemState == nil {
		itemState = &openAIResponsesOutputItemState{Index: index}
		s.OutputItems[index] = itemState
	}
	return itemState
}

func (s *openAIResponsesCompletionState) observeFunctionCallArguments(event map[string]interface{}, done bool) {
	if s == nil || event == nil {
		return
	}
	s.ToolSeen = true
	index := parseTokenNumber(event["output_index"])
	itemState := s.ensureOutputItem(index)
	itemState.ItemType = "function_call"
	if done {
		itemState.ArgumentsDone = true
		s.ToolArgumentsDone = true
		return
	}
	itemState.ArgumentsDeltaSeen = true
}

func (s *openAIResponsesCompletionState) observeCustomToolCallInput(event map[string]interface{}, done bool) {
	if s == nil || event == nil {
		return
	}
	s.ToolSeen = true
	index := parseTokenNumber(event["output_index"])
	itemState := s.ensureOutputItem(index)
	itemState.ItemType = "custom_tool_call"
	if done {
		itemState.InputDone = true
		s.ToolArgumentsDone = true
		if input, ok := event["input"].(string); ok {
			itemState.Input = input
		}
		if itemState.Item != nil && itemState.Input != "" {
			itemState.Item["input"] = itemState.Input
		}
		return
	}
	itemState.InputDeltaSeen = true
	if delta, ok := event["delta"].(string); ok {
		itemState.Input += delta
	}
}

func (s *openAIResponsesCompletionState) observeOutputItem(event map[string]interface{}, item map[string]interface{}, done bool) {
	if s == nil || item == nil {
		return
	}
	index := parseTokenNumber(event["output_index"])
	itemState := s.ensureOutputItem(index)
	itemState.Item = cloneOpenAIResponsesMap(item)
	itemState.ItemType = strings.ToLower(strings.TrimSpace(stringFromInterface(item["type"])))
	if itemState.ItemType == "custom_tool_call" && itemState.Input != "" && !hasNonEmptyString(itemState.Item["input"]) {
		itemState.Item["input"] = itemState.Input
	}
	itemState.Done = itemState.Done || done
	if itemState.ItemType != "message" && itemState.ItemType != "reasoning" {
		s.ToolSeen = true
	}
	if itemState.ItemType == "function_call" && done {
		s.ToolItemDone = true
		if hasNonEmptyString(item["arguments"]) {
			itemState.ArgumentsDone = true
			s.ToolArgumentsDone = true
		}
	}
	if itemState.ItemType == "custom_tool_call" && done {
		s.ToolItemDone = true
		if hasNonEmptyString(itemState.Item["input"]) {
			itemState.InputDone = true
			itemState.Input = stringFromInterface(itemState.Item["input"])
			s.ToolArgumentsDone = true
		}
	}
}

func (s *openAIResponsesCompletionState) updateToolSafety() {
	if s == nil {
		return
	}
	s.OutputItemCount = 0
	s.ToolRecoverable = false
	s.ToolPending = false

	for _, itemState := range s.OutputItems {
		if itemState == nil {
			continue
		}
		itemType := strings.ToLower(strings.TrimSpace(itemState.ItemType))
		if itemType == "" || itemType == "message" || itemType == "reasoning" {
			continue
		}
		s.OutputItemCount++
		s.ToolSeen = true
		if isRecoverableOpenAIResponsesCompletedToolItem(itemState) {
			s.ToolRecoverable = true
			continue
		}
		s.ToolPending = true
		if s.UnsafeReason == "" || s.UnsafeReason == "unknown" {
			s.UnsafeReason = openAIResponsesPendingReason(itemType)
		}
	}

	if s.ToolSeen && !s.ToolRecoverable && s.OutputItemCount == 0 {
		s.ToolPending = true
		if s.UnsafeReason == "" || s.UnsafeReason == "unknown" {
			s.UnsafeReason = "function_call_pending"
		}
	}
	if s.StructuralUnsafe {
		s.Unsafe = true
		s.UnsafeReason = s.StructuralReason
		return
	}
	s.Unsafe = s.ToolPending
	if s.ToolPending && (s.UnsafeReason == "" || s.UnsafeReason == "unknown") {
		s.UnsafeReason = "function_call_pending"
	}
	if !s.Unsafe && s.UnsafeReason != "" && strings.HasSuffix(s.UnsafeReason, "_pending") {
		s.UnsafeReason = ""
	}
}

func openAIResponsesPendingReason(itemType string) string {
	itemType = strings.ToLower(strings.TrimSpace(itemType))
	switch itemType {
	case "function_call":
		return "function_call_pending"
	case "custom_tool_call":
		return "custom_tool_input_pending"
	case "":
		return "tool_pending"
	default:
		return itemType + "_pending"
	}
}

func isRecoverableOpenAIResponsesCompletedToolItem(itemState *openAIResponsesOutputItemState) bool {
	if itemState == nil || !itemState.Done {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(itemState.ItemType))
	switch itemType {
	case "function_call":
		return isRecoverableFunctionCallItem(itemState.Item)
	case "custom_tool_call":
		return itemState.InputDone && isRecoverableCustomToolCallItem(itemState.Item)
	default:
		return false
	}
}

func isRecoverableOpenAIResponsesToolItem(item map[string]interface{}) bool {
	return isRecoverableFunctionCallItem(item) || isRecoverableCustomToolCallItem(item)
}

func isRecoverableFunctionCallItem(item map[string]interface{}) bool {
	if item == nil {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(stringFromInterface(item["type"])))
	if itemType != "function_call" {
		return false
	}
	if !hasNonEmptyString(item["name"]) {
		return false
	}
	return hasNonEmptyString(item["arguments"]) || hasNonEmptyString(item["call_id"]) || hasNonEmptyString(item["id"])
}

func isRecoverableCustomToolCallItem(item map[string]interface{}) bool {
	if item == nil {
		return false
	}
	itemType := strings.ToLower(strings.TrimSpace(stringFromInterface(item["type"])))
	if itemType != "custom_tool_call" {
		return false
	}
	if !hasNonEmptyString(item["name"]) {
		return false
	}
	if !hasNonEmptyString(item["input"]) {
		return false
	}
	return hasNonEmptyString(item["call_id"]) || hasNonEmptyString(item["id"])
}

func openAIResponsesOutputItemText(item map[string]interface{}) string {
	if item == nil {
		return ""
	}
	content, ok := item["content"].([]interface{})
	if !ok {
		return ""
	}
	var text strings.Builder
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]interface{})
		if !ok {
			continue
		}
		if value, ok := part["text"].(string); ok {
			text.WriteString(value)
		}
	}
	return text.String()
}

func (s *openAIResponsesCompletionState) captureItemID(event map[string]interface{}) {
	if s == nil || s.ItemID != "" || event == nil {
		return
	}
	if itemID, ok := event["item_id"].(string); ok && strings.TrimSpace(itemID) != "" {
		s.ItemID = itemID
	}
}

// handleStreamingResponse processes streaming SSE responses
func (p *Proxy) handleStreamingResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, endpoint config.Endpoint, trans transformer.Transformer, transformerName string, thinkingEnabled bool, modelName string, bodyBytes []byte, credentialID int64, streamSession *downstreamStreamSession) streamResponseResult {
	result := streamResponseResult{}

	flusher, ok := w.(http.Flusher)
	if streamSession == nil && !ok {
		logger.Error("[%s] ResponseWriter does not support flushing", endpoint.Name)
		resp.Body.Close()
		result.Reason = streamFinishDownstreamWriteFailed
		result.Err = fmt.Errorf("response writer does not support flushing")
		return result
	}

	headersCommitted := false
	commitHeaders := func() {
		if streamSession != nil {
			headersCommitted = true
			return
		}
		if headersCommitted {
			return
		}
		for key, values := range resp.Header {
			if key == "Content-Length" || key == "Content-Encoding" {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		if strings.TrimSpace(w.Header().Get("Content-Type")) == "" {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		}
		w.WriteHeader(resp.StatusCode)
		headersCommitted = true
	}
	writeData := func(data []byte) error {
		if streamSession != nil {
			return streamSession.Write(data)
		}
		commitHeaders()
		if _, writeErr := w.Write(data); writeErr != nil {
			return writeErr
		}
		flusher.Flush()
		return nil
	}

	// Handle gzip-encoded response body
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			logger.Error("[%s] Failed to create gzip reader: %v", endpoint.Name, err)
			resp.Body.Close()
			result.Reason = streamFinishUpstreamStreamError
			result.Err = err
			return result
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	// Create stream context for all transformers except pure passthrough
	var streamCtx *transformer.StreamContext
	switch transformerName {
	case "cx_chat_openai", "cx_resp_openai2":
		// Pure passthrough - no context needed
	default:
		// cc_claude needs context for input_tokens fallback
		streamCtx = transformer.NewStreamContext()
		streamCtx.ModelName = modelName
		if streamSession != nil {
			streamSession.primeStreamContext(streamCtx)
		}
		// Pre-estimate input tokens for fallback
		if bodyBytes != nil {
			streamCtx.InputTokens = p.estimateInputTokens(bodyBytes)
		}
	}

	scanner := bufio.NewScanner(reader)
	// Increase buffer sizes to handle large SSE events (e.g., large file reads in tool calls)
	buf := make([]byte, 0, 128*1024) // 128KB initial buffer (was 64KB)
	scanner.Buffer(buf, maxStreamEventBytes)

	var inputTokens, outputTokens int
	var buffer bytes.Buffer
	var pendingWrites bytes.Buffer
	var outputText strings.Builder
	var responsesCompletion openAIResponsesCompletionState
	eventCount := 0
	streamDone := false
	semanticDataSeen := false
	progressFlushed := false
	emptyKind := ""
	isOpenAIResponsesStream := streamSession != nil && streamSession.clientFormat == ClientFormatOpenAIResponses
	poeDiagnosticsPending := isOpenAIResponsesStream && providercompat.NormalizeTransformer(endpoint.Transformer) == providercompat.TransformerPoe

	writeTransformedEvent := func(data []byte, semantic bool, progress bool) error {
		if len(data) == 0 {
			return nil
		}
		if !semanticDataSeen {
			pendingWrites.Write(data)
			if !semantic {
				// Reasoning/thinking progress is flushed live to keep the client
				// alive, but does not mark the stream semantic, so empty-response
				// detection still applies. Silent cross-endpoint retry is given up
				// once any bytes have reached the client.
				if progress || progressFlushed {
					commitHeaders()
					if writeErr := writeData(pendingWrites.Bytes()); writeErr != nil {
						return writeErr
					}
					pendingWrites.Reset()
					progressFlushed = true
					result.WroteData = true
				}
				return nil
			}
			commitHeaders()
			if writeErr := writeData(pendingWrites.Bytes()); writeErr != nil {
				return writeErr
			}
			pendingWrites.Reset()
			semanticDataSeen = true
			result.WroteSemanticData = true
			result.WroteData = true
			return nil
		}

		if writeErr := writeData(data); writeErr != nil {
			return writeErr
		}
		result.WroteData = true
		return nil
	}
	logPoeResponsesDiagnostics := func(transformedEvent []byte) {
		if !poeDiagnosticsPending || len(transformedEvent) == 0 {
			return
		}
		poeDiagnosticsPending = false
		eventType := sseBlockEventType(string(transformedEvent))
		logger.Debug(
			"[%s] Poe streaming diagnostics upstream_path=%s content_type=%s first_transformed_event_type=%s",
			endpoint.Name,
			responseRequestPath(resp),
			resp.Header.Get("Content-Type"),
			sanitizeLogField(eventType),
		)
	}
	recordFirstTransformedEventType := func(transformedEvent []byte) {
		if len(transformedEvent) == 0 {
			return
		}
		eventType := sseBlockEventType(string(transformedEvent))
		if result.FirstTransformedEventType == "" {
			result.FirstTransformedEventType = eventType
		}
		if eventType != "" {
			result.LastTransformedEventType = eventType
		}
	}
	writeSyntheticResponsesCompletion := func() bool {
		if !isOpenAIResponsesStream || responsesCompletion.Completed {
			return true
		}
		if streamSession != nil && !streamSession.synthesizeMissingDone {
			return false
		}
		if !responsesCompletion.canSynthesizeCompleted() {
			return false
		}
		completedEvent := responsesCompletion.completedEvent(inputTokens, outputTokens)
		if len(completedEvent) == 0 {
			return false
		}
		responsesCompletion.observe(completedEvent)
		streamInspection := inspectSemanticStreamEvent(completedEvent)
		if writeErr := writeTransformedEvent(completedEvent, streamInspection.HasOutput, streamInspection.HasProgress); writeErr != nil {
			result.Completed = false
			result.Reason = streamFinishDownstreamWriteFailed
			result.Err = writeErr
			return false
		}
		if responsesCompletion.ToolRecoverable {
			toolType := responsesCompletion.LastOutputItemType
			if toolType == "" {
				toolType = "function_call"
			}
			logger.Debug(
				"[%s] Completing OpenAI Responses %s stream missing response.completed responses_tool_recoverable=%t responses_tool_pending=%t responses_output_items=%d",
				endpoint.Name,
				sanitizeLogField(toolType),
				responsesCompletion.ToolRecoverable,
				responsesCompletion.ToolPending,
				responsesCompletion.OutputItemCount,
			)
		} else {
			logger.Debug("[%s] Synthesized OpenAI Responses response.completed before stream close", endpoint.Name)
		}
		return true
	}
	failMissingResponsesCompleted := func() {
		result.Completed = false
		result.Reason = streamFinishMissingResponsesDone
		result.Err = fmt.Errorf("stream closed before response.completed")
	}

	for scanner.Scan() && !streamDone {
		line := scanner.Text()

		if strings.Contains(line, "data: [DONE]") {
			streamDone = true
			if isOpenAIResponsesStream && !responsesCompletion.Completed {
				if !writeSyntheticResponsesCompletion() {
					if result.Err == nil {
						failMissingResponsesCompleted()
					}
					break
				}
			}
			if result.Err == nil {
				result.Completed = true
				result.Reason = streamFinishCompleted
			}

			// Token Usage Fallback: Inject message_delta with estimated output_tokens before [DONE]
			if outputTokens == 0 && outputText.Len() > 0 {
				outputTokens = tokencount.EstimateOutputTokens(outputText.String())
				logger.Debug("[%s] Token fallback before [DONE]: estimated output_tokens=%d", endpoint.Name, outputTokens)

				// Update stream context for transformer fallback
				if streamCtx != nil {
					streamCtx.OutputTokens = outputTokens
				}

				if !isOpenAIResponsesStream {
					// Inject message_delta event with usage
					deltaEvent := fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", outputTokens)
					if writeErr := writeTransformedEvent([]byte(deltaEvent), false, false); writeErr != nil {
						result.Completed = false
						result.Reason = streamFinishDownstreamWriteFailed
						result.Err = writeErr
						break
					}
				}
			}

			buffer.WriteString(line + "\n")
			eventData := buffer.Bytes()
			logger.DebugLog("[%s] SSE Event #%d (Original): %s", endpoint.Name, eventCount+1, string(eventData))

			if streamSession != nil {
				streamSession.primeStreamContext(streamCtx)
			}
			transformedEvent, err := p.transformStreamEvent(eventData, trans, transformerName, streamCtx)
			if err == nil && len(transformedEvent) > 0 {
				recordFirstTransformedEventType(transformedEvent)
				logPoeResponsesDiagnostics(transformedEvent)
				logger.DebugLog("[%s] SSE Event #%d (Transformed): %s", endpoint.Name, eventCount+1, string(transformedEvent))
				if isOpenAIResponsesStream {
					responsesCompletion.observe(transformedEvent)
				}
				streamInspection := inspectSemanticStreamEvent(transformedEvent)
				if streamInspection.EmptyKind != "" {
					emptyKind = streamInspection.EmptyKind
				}
				if writeErr := writeTransformedEvent(transformedEvent, streamInspection.HasOutput, streamInspection.HasProgress); writeErr != nil {
					result.Completed = false
					result.Reason = streamFinishDownstreamWriteFailed
					result.Err = writeErr
					break
				}
			} else if err != nil {
				result.Completed = false
				result.Reason = streamFinishTransformFailed
				result.Err = err
			}
			break
		}

		buffer.WriteString(line + "\n")

		if line == "" {
			eventCount++
			eventData := buffer.Bytes()
			logger.DebugLog("[%s] SSE Event #%d (Original): %s", endpoint.Name, eventCount, string(eventData))
			if upstreamErr := parseUpstreamSSEError(eventData); upstreamErr != nil {
				result.Err = upstreamErr
				result.Reason = streamFinishUpstreamStreamError
				if upstreamErr.IsRequestScoped() {
					result.Reason = retryReasonRequestInvalid
				}
				streamDone = true
				buffer.Reset()
				break
			}

			p.captureCodexRateLimitsFromEvent(endpoint, credentialID, eventData)

			// Extract usage from original upstream events first. Some transformers may
			// not preserve usage fields in transformed events.
			p.extractTokensFromEvent(eventData, &inputTokens, &outputTokens)

			// Check if this is a message_stop event (Token Usage Fallback)
			isMessageStop := p.isMessageStopEvent(eventData)
			if isMessageStop && outputTokens == 0 && outputText.Len() > 0 {
				outputTokens = tokencount.EstimateOutputTokens(outputText.String())
				logger.Debug("[%s] Token fallback before message_stop: estimated output_tokens=%d", endpoint.Name, outputTokens)

				// Update stream context for transformer fallback
				if streamCtx != nil {
					streamCtx.OutputTokens = outputTokens
				}

				if !isOpenAIResponsesStream {
					// Inject message_delta event with usage before message_stop
					deltaEvent := fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", outputTokens)
					if writeErr := writeTransformedEvent([]byte(deltaEvent), false, false); writeErr != nil {
						result.Reason = streamFinishDownstreamWriteFailed
						result.Err = writeErr
						streamDone = true
						break
					}
				}
			}

			if streamSession != nil {
				streamSession.primeStreamContext(streamCtx)
			}
			transformedEvent, err := p.transformStreamEvent(eventData, trans, transformerName, streamCtx)
			if err != nil {
				logger.Error("[%s] Failed to transform SSE event: %v", endpoint.Name, err)
				result.Reason = streamFinishTransformFailed
				result.Err = err
				streamDone = true
			} else if len(transformedEvent) > 0 {
				recordFirstTransformedEventType(transformedEvent)
				logPoeResponsesDiagnostics(transformedEvent)
				logger.DebugLog("[%s] SSE Event #%d (Transformed): %s", endpoint.Name, eventCount, string(transformedEvent))

				p.extractTokensFromEvent(transformedEvent, &inputTokens, &outputTokens)
				p.extractTextFromEvent(transformedEvent, &outputText)
				if isOpenAIResponsesStream {
					responsesCompletion.observe(transformedEvent)
				}
				streamInspection := inspectSemanticStreamEvent(transformedEvent)
				semanticEvent := streamInspection.HasOutput
				progressEvent := streamInspection.HasProgress
				if streamInspection.EmptyKind != "" {
					emptyKind = streamInspection.EmptyKind
				}

				if writeErr := writeTransformedEvent(transformedEvent, semanticEvent, progressEvent); writeErr != nil {
					// Client disconnected (broken pipe) is normal for cancelled requests
					if strings.Contains(writeErr.Error(), "broken pipe") || strings.Contains(writeErr.Error(), "connection reset") {
						logger.Debug("[%s] Client disconnected: %v", endpoint.Name, writeErr)
						result.Reason = streamFinishClientCanceled
					} else {
						logger.Error("[%s] Failed to write transformed event: %v", endpoint.Name, writeErr)
						result.Reason = streamFinishDownstreamWriteFailed
					}
					result.Err = writeErr
					streamDone = true
					break
				}
			}
			buffer.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		errMsg := err.Error()
		result.Err = err
		var webSocketUpstreamErr *codexWebSocketUpstreamError
		if errors.As(err, &webSocketUpstreamErr) {
			result.Reason = retryReasonForCodexWebSocketUpstreamError(webSocketUpstreamErr)
			logger.Error("[%s] Codex WebSocket upstream error: %v", endpoint.Name, webSocketUpstreamErr)
		} else if isClientCanceled(ctx, err) {
			result.Reason = streamFinishClientCanceled
			logger.Debug("[%s] Streaming canceled by client: %v", endpoint.Name, err)
		} else if strings.Contains(errMsg, "stream error") || strings.Contains(errMsg, "INTERNAL_ERROR") {
			result.Reason = streamFinishUpstreamStreamError
			requestSize := len(bodyBytes)
			sizeStr := formatRequestSize(requestSize)
			logger.Error("[%s] HTTP/2 stream error (Request size: %s / %d bytes): %v",
				endpoint.Name, sizeStr, requestSize, err)

			// Provide context based on request size
			if requestSize > 100*1024 { // > 100KB
				logger.Warn("[%s] Large request detected (%s). Consider: 1) Reading fewer files at once, 2) Using smaller code sections, 3) Breaking task into smaller requests",
					endpoint.Name, sizeStr)
			} else {
				logger.Warn("[%s] This error may occur due to upstream server limitations or network issues.", endpoint.Name)
			}
		} else {
			result.Reason = streamFinishUpstreamStreamError
			logger.Error("[%s] Scanner error: %v", endpoint.Name, err)
		}
	}

	resp.Body.Close()
	if result.Reason == "" {
		if isOpenAIResponsesStream && !responsesCompletion.Completed {
			if writeSyntheticResponsesCompletion() {
				result.Reason = streamFinishCompleted
				result.Completed = true
			} else {
				if result.Err == nil {
					failMissingResponsesCompleted()
				}
			}
		} else {
			result.Reason = streamFinishCompleted
			result.Completed = true
		}
	}
	finalOutputText := outputText.String()
	if finalOutputText == "" && responsesCompletion.Text != "" {
		finalOutputText = responsesCompletion.Text
	}
	if outputTokens == 0 && finalOutputText != "" {
		outputTokens = tokencount.EstimateOutputTokens(finalOutputText)
	}
	result.InputTokens = inputTokens
	result.OutputTokens = outputTokens
	result.OutputText = finalOutputText
	result.ResponsesCompletionSafe = responsesCompletion
	if result.Err == nil && result.Completed && !result.WroteSemanticData {
		isClaudeClientStream := streamSession != nil && streamSession.clientFormat == ClientFormatClaude
		if hasSuccessfulOutputTokens(outputTokens) && !isClaudeClientStream {
			if pendingWrites.Len() > 0 {
				if writeErr := writeData(pendingWrites.Bytes()); writeErr != nil {
					result.Reason = streamFinishDownstreamWriteFailed
					result.Completed = false
					result.Err = writeErr
					return result
				}
				pendingWrites.Reset()
				result.WroteData = true
			}
			logger.Debug(
				"[%s] Completed output-token-backed stream without semantic retry output_tokens=%d outputTextLen=%d",
				endpoint.Name,
				outputTokens,
				outputText.Len(),
			)
			return result
		}
		if emptyKind == "" && isClaudeClientStream {
			emptyKind = emptyKindClaudeEmpty
		}
		result.Reason = retryReasonSemanticEmptyResponse
		result.Completed = false
		result.Err = newSemanticEmptyResponseError(emptyKind, outputTokens, outputText.Len())
	}
	return result
}

// handleStreamingAsNonStreaming aggregates SSE and returns a single non-stream response.
// This is used for Codex endpoints that require stream=true upstream while client requested non-stream.
func (p *Proxy) handleStreamingAsNonStreaming(w http.ResponseWriter, resp *http.Response, endpoint config.Endpoint, trans transformer.Transformer, credentialID int64, requireTextOutput bool) (int, int, string, error) {
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return 0, 0, "", err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 128*1024)
	scanner.Buffer(buf, maxStreamEventBytes)

	var completedPayload []byte
	var lastJSONPayload []byte
	chatAccumulator := newOpenAIChatStreamAccumulator()
	responsesAccumulator := newOpenAIResponsesStreamAccumulator()
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "" || jsonData == "[DONE]" {
			continue
		}
		p.captureCodexRateLimitsFromEvent(endpoint, credentialID, []byte("data: "+jsonData+"\n\n"))
		lastJSONPayload = []byte(jsonData)

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}
		if chatAccumulator.addChunk(event) {
			continue
		}
		responsesAccumulator.addEvent(event)
		if eventType, _ := event["type"].(string); eventType != "response.completed" {
			continue
		}

		if responseObj, ok := event["response"]; ok {
			payload, err := json.Marshal(responseObj)
			if err != nil {
				return 0, 0, "", err
			}
			completedPayload = payload
		} else {
			completedPayload = []byte(jsonData)
		}
		break
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, "", err
	}
	if responsesAccumulator.hasData() {
		payload, ok, err := responsesAccumulator.payload(completedPayload)
		if err != nil {
			return 0, 0, "", err
		}
		if ok {
			completedPayload = payload
		}
	}
	if len(completedPayload) == 0 {
		if chatAccumulator.hasData() {
			payload, err := chatAccumulator.payload()
			if err != nil {
				return 0, 0, "", err
			}
			completedPayload = payload
		}
	}
	if len(completedPayload) == 0 {
		if len(lastJSONPayload) == 0 {
			return 0, 0, "", fmt.Errorf("stream closed before response.completed")
		}
		// Fallback for providers that don't emit type=response.completed but still
		// provide final JSON payload in the stream.
		completedPayload = lastJSONPayload
	}

	transformedResp, err := trans.TransformResponse(completedPayload, false)
	if err != nil {
		return 0, 0, "", err
	}

	inputTokens, outputTokens := extractTokenUsage(transformedResp)
	transformedInputTokens, transformedOutputTokens := inputTokens, outputTokens
	upstreamInputTokens, upstreamOutputTokens := extractTokenUsage(completedPayload)
	if inputTokens == 0 && upstreamInputTokens > 0 {
		inputTokens = upstreamInputTokens
	}
	if outputTokens == 0 && upstreamOutputTokens > 0 {
		outputTokens = upstreamOutputTokens
	}
	outputText := extractResponseOutputText(transformedResp)

	logger.Debug(
		"[%s] Aggregated usage transformed(in=%d,out=%d) upstream(in=%d,out=%d) outputTextLen=%d",
		endpoint.Name,
		transformedInputTokens, transformedOutputTokens,
		upstreamInputTokens, upstreamOutputTokens,
		len(outputText),
	)

	if semanticErr := semanticEmptyErrorForResponseWithTextRequirement(transformedResp, outputTokens, requireTextOutput); semanticErr != nil {
		semanticErr.OutputTextLen = len(outputText)
		return 0, 0, "", semanticErr
	}

	for key, values := range resp.Header {
		if key == "Content-Length" || key == "Content-Encoding" || key == "Content-Type" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(transformedResp); err != nil {
		return 0, 0, "", fmt.Errorf("downstream delivery failed: %w", err)
	}

	return inputTokens, outputTokens, outputText, nil
}

type openAIChatStreamAccumulator struct {
	id                string
	created           interface{}
	model             string
	systemFingerprint interface{}
	usage             map[string]interface{}
	choices           map[int]*openAIChatStreamChoice
	seen              bool
}

type openAIResponsesStreamAccumulator struct {
	id           string
	object       string
	status       string
	model        string
	created      interface{}
	usage        map[string]interface{}
	textByOutput map[int]string
	itemIDs      map[int]string
	outputItems  map[int]map[string]interface{}
	seen         bool
}

func newOpenAIResponsesStreamAccumulator() *openAIResponsesStreamAccumulator {
	return &openAIResponsesStreamAccumulator{
		textByOutput: make(map[int]string),
		itemIDs:      make(map[int]string),
		outputItems:  make(map[int]map[string]interface{}),
	}
}

func (a *openAIResponsesStreamAccumulator) hasData() bool {
	return a != nil && a.seen
}

func (a *openAIResponsesStreamAccumulator) addEvent(event map[string]interface{}) {
	if a == nil || event == nil {
		return
	}
	eventType, _ := event["type"].(string)
	if !strings.HasPrefix(eventType, "response.") {
		return
	}
	a.seen = true

	switch eventType {
	case "response.created", "response.completed":
		if response, ok := event["response"].(map[string]interface{}); ok {
			a.observeResponse(response)
		}
	case "response.output_text.delta":
		index := parseTokenNumber(event["output_index"])
		if delta := stringFromInterface(event["delta"]); delta != "" {
			a.textByOutput[index] += delta
		}
		if itemID := stringFromInterface(event["item_id"]); itemID != "" && a.itemIDs[index] == "" {
			a.itemIDs[index] = itemID
		}
	case "response.output_text.done":
		index := parseTokenNumber(event["output_index"])
		if text := stringFromInterface(event["text"]); text != "" {
			a.textByOutput[index] = text
		} else if delta := stringFromInterface(event["delta"]); delta != "" && a.textByOutput[index] == "" {
			a.textByOutput[index] = delta
		}
		if itemID := stringFromInterface(event["item_id"]); itemID != "" && a.itemIDs[index] == "" {
			a.itemIDs[index] = itemID
		}
	case "response.output_item.done":
		index := parseTokenNumber(event["output_index"])
		if item, ok := event["item"].(map[string]interface{}); ok {
			a.outputItems[index] = cloneOpenAIResponsesMap(item)
			if itemID := stringFromInterface(item["id"]); itemID != "" && a.itemIDs[index] == "" {
				a.itemIDs[index] = itemID
			}
		}
	}
}

func (a *openAIResponsesStreamAccumulator) observeResponse(response map[string]interface{}) {
	if response == nil {
		return
	}
	if id := stringFromInterface(response["id"]); id != "" {
		a.id = id
	}
	if object := stringFromInterface(response["object"]); object != "" {
		a.object = object
	}
	if status := stringFromInterface(response["status"]); status != "" {
		a.status = status
	}
	if model := stringFromInterface(response["model"]); model != "" {
		a.model = model
	}
	if created, ok := response["created_at"]; ok && a.created == nil {
		a.created = created
	}
	if usage, ok := response["usage"].(map[string]interface{}); ok && len(usage) > 0 {
		a.usage = cloneOpenAIResponsesMap(usage)
	}
}

func (a *openAIResponsesStreamAccumulator) payload(completedPayload []byte) ([]byte, bool, error) {
	if a == nil || !a.hasData() {
		return nil, false, nil
	}

	var payload map[string]interface{}
	if len(completedPayload) > 0 {
		if err := json.Unmarshal(completedPayload, &payload); err != nil {
			return nil, false, err
		}
		if strings.TrimSpace(extractResponseOutputText(completedPayload)) != "" {
			return nil, false, nil
		}
	}
	if payload == nil {
		payload = make(map[string]interface{})
	}

	output := a.output(payload)
	if len(output) == 0 {
		return nil, false, nil
	}

	if _, ok := payload["id"]; !ok && a.id != "" {
		payload["id"] = a.id
	}
	if _, ok := payload["object"]; !ok {
		if a.object != "" {
			payload["object"] = a.object
		} else {
			payload["object"] = "response"
		}
	}
	if _, ok := payload["status"]; !ok {
		if a.status != "" {
			payload["status"] = a.status
		} else {
			payload["status"] = "completed"
		}
	}
	if _, ok := payload["model"]; !ok && a.model != "" {
		payload["model"] = a.model
	}
	if _, ok := payload["created_at"]; !ok && a.created != nil {
		payload["created_at"] = a.created
	}
	if _, ok := payload["usage"]; !ok && a.usage != nil {
		payload["usage"] = cloneOpenAIResponsesMap(a.usage)
	}
	payload["output"] = output

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (a *openAIResponsesStreamAccumulator) output(payload map[string]interface{}) []interface{} {
	output := make([]interface{}, 0)
	if existing, ok := payload["output"].([]interface{}); ok && len(existing) > 0 {
		output = append(output, existing...)
	}

	indexSet := make(map[int]bool)
	for index := range a.outputItems {
		indexSet[index] = true
	}
	for index := range a.textByOutput {
		indexSet[index] = true
	}
	indices := make([]int, 0, len(indexSet))
	for index := range indexSet {
		indices = append(indices, index)
	}
	sort.Ints(indices)

	for _, index := range indices {
		item := cloneOpenAIResponsesMap(a.outputItems[index])
		text := strings.TrimSpace(a.textByOutput[index])
		if text != "" {
			item = a.messageItemWithText(index, item, text)
		}
		if len(item) > 0 {
			output = append(output, item)
		}
	}

	return output
}

func (a *openAIResponsesStreamAccumulator) messageItemWithText(index int, item map[string]interface{}, text string) map[string]interface{} {
	if item == nil {
		item = make(map[string]interface{})
	}
	if itemType := stringFromInterface(item["type"]); itemType != "" && itemType != "message" {
		return map[string]interface{}{
			"type":   "message",
			"id":     a.outputItemID(index),
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]interface{}{
				{"type": "output_text", "text": text},
			},
		}
	}
	if _, ok := item["type"]; !ok {
		item["type"] = "message"
	}
	if _, ok := item["id"]; !ok {
		item["id"] = a.outputItemID(index)
	}
	if _, ok := item["role"]; !ok {
		item["role"] = "assistant"
	}
	if _, ok := item["status"]; !ok {
		item["status"] = "completed"
	}
	item["content"] = []map[string]interface{}{
		{"type": "output_text", "text": text},
	}
	return item
}

func (a *openAIResponsesStreamAccumulator) outputItemID(index int) string {
	if itemID := a.itemIDs[index]; itemID != "" {
		return itemID
	}
	if a.id != "" {
		return fmt.Sprintf("msg_%s_%d", a.id, index)
	}
	return fmt.Sprintf("msg_%d", index)
}

type openAIChatStreamChoice struct {
	index            int
	role             string
	content          string
	reasoningContent string
	toolCalls        map[int]*openAIChatStreamToolCall
	finishReason     interface{}
}

type openAIChatStreamToolCall struct {
	index     int
	id        string
	callType  string
	name      string
	arguments string
}

func newOpenAIChatStreamAccumulator() *openAIChatStreamAccumulator {
	return &openAIChatStreamAccumulator{
		choices: make(map[int]*openAIChatStreamChoice),
	}
}

func (a *openAIChatStreamAccumulator) hasData() bool {
	return a != nil && a.seen
}

func (a *openAIChatStreamAccumulator) addChunk(event map[string]interface{}) bool {
	if a == nil || !isOpenAIChatStreamChunk(event) {
		return false
	}
	a.seen = true

	if id, ok := event["id"].(string); ok && id != "" && a.id == "" {
		a.id = id
	}
	if created, ok := event["created"]; ok && a.created == nil {
		a.created = created
	}
	if model, ok := event["model"].(string); ok && model != "" && a.model == "" {
		a.model = model
	}
	if fp, ok := event["system_fingerprint"]; ok && a.systemFingerprint == nil {
		a.systemFingerprint = fp
	}
	if usage, ok := event["usage"].(map[string]interface{}); ok && len(usage) > 0 {
		a.usage = usage
	}

	choices, _ := event["choices"].([]interface{})
	for _, choiceValue := range choices {
		choiceMap, ok := choiceValue.(map[string]interface{})
		if !ok {
			continue
		}
		index := parseTokenNumber(choiceMap["index"])
		choice := a.choice(index)

		if finishReason, ok := choiceMap["finish_reason"]; ok && finishReason != nil {
			choice.finishReason = finishReason
		}

		delta, ok := choiceMap["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if role, ok := delta["role"].(string); ok && role != "" {
			choice.role = role
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			choice.content += content
		}
		if reasoningContent, ok := delta["reasoning_content"].(string); ok && reasoningContent != "" {
			choice.reasoningContent += reasoningContent
		}
		if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
			choice.addToolCalls(toolCalls)
		}
	}

	return true
}

func (a *openAIChatStreamAccumulator) choice(index int) *openAIChatStreamChoice {
	choice, ok := a.choices[index]
	if ok {
		return choice
	}
	choice = &openAIChatStreamChoice{
		index:     index,
		role:      "assistant",
		toolCalls: make(map[int]*openAIChatStreamToolCall),
	}
	a.choices[index] = choice
	return choice
}

func (c *openAIChatStreamChoice) addToolCalls(toolCallValues []interface{}) {
	for _, toolCallValue := range toolCallValues {
		toolCallMap, ok := toolCallValue.(map[string]interface{})
		if !ok {
			continue
		}
		index := parseTokenNumber(toolCallMap["index"])
		toolCall := c.toolCall(index)

		if id, ok := toolCallMap["id"].(string); ok && id != "" {
			toolCall.id = id
		}
		if callType, ok := toolCallMap["type"].(string); ok && callType != "" {
			toolCall.callType = callType
		}
		function, _ := toolCallMap["function"].(map[string]interface{})
		if name, ok := function["name"].(string); ok && name != "" {
			toolCall.name = name
		}
		if arguments, ok := function["arguments"].(string); ok && arguments != "" {
			toolCall.arguments += arguments
		}
	}
}

func (c *openAIChatStreamChoice) toolCall(index int) *openAIChatStreamToolCall {
	toolCall, ok := c.toolCalls[index]
	if ok {
		return toolCall
	}
	toolCall = &openAIChatStreamToolCall{index: index, callType: "function"}
	c.toolCalls[index] = toolCall
	return toolCall
}

func (a *openAIChatStreamAccumulator) payload() ([]byte, error) {
	choices := make([]map[string]interface{}, 0, len(a.choices))
	indices := make([]int, 0, len(a.choices))
	for index := range a.choices {
		indices = append(indices, index)
	}
	sort.Ints(indices)

	for _, index := range indices {
		choice := a.choices[index]
		message := map[string]interface{}{
			"role":    choice.role,
			"content": choice.content,
		}
		if choice.reasoningContent != "" {
			message["reasoning_content"] = choice.reasoningContent
		}
		if len(choice.toolCalls) > 0 {
			message["tool_calls"] = choice.toolCallPayloads()
		}
		finishReason := choice.finishReason
		if finishReason == nil {
			finishReason = "stop"
		}
		choices = append(choices, map[string]interface{}{
			"index":         choice.index,
			"message":       message,
			"finish_reason": finishReason,
		})
	}

	created := a.created
	if created == nil {
		created = 0
	}
	payload := map[string]interface{}{
		"id":      a.id,
		"object":  "chat.completion",
		"created": created,
		"model":   a.model,
		"choices": choices,
	}
	if a.usage != nil {
		payload["usage"] = a.usage
	} else {
		payload["usage"] = map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		}
	}
	if a.systemFingerprint != nil {
		payload["system_fingerprint"] = a.systemFingerprint
	}

	return json.Marshal(payload)
}

func (c *openAIChatStreamChoice) toolCallPayloads() []map[string]interface{} {
	indices := make([]int, 0, len(c.toolCalls))
	for index := range c.toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)

	payloads := make([]map[string]interface{}, 0, len(indices))
	for _, index := range indices {
		toolCall := c.toolCalls[index]
		payloads = append(payloads, map[string]interface{}{
			"index": toolCall.index,
			"id":    toolCall.id,
			"type":  toolCall.callType,
			"function": map[string]interface{}{
				"name":      toolCall.name,
				"arguments": toolCall.arguments,
			},
		})
	}
	return payloads
}

func isOpenAIChatStreamChunk(event map[string]interface{}) bool {
	if event == nil {
		return false
	}
	object, _ := event["object"].(string)
	if object == "chat.completion.chunk" {
		return true
	}
	if object != "" {
		return false
	}
	choices, ok := event["choices"].([]interface{})
	if !ok {
		return false
	}
	for _, choiceValue := range choices {
		choiceMap, ok := choiceValue.(map[string]interface{})
		if !ok {
			continue
		}
		if _, ok := choiceMap["delta"].(map[string]interface{}); ok {
			return true
		}
	}
	return false
}

// formatRequestSize formats byte size into human-readable string
func formatRequestSize(bytes int) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// transformStreamEvent transforms a single SSE event
func (p *Proxy) transformStreamEvent(eventData []byte, trans transformer.Transformer, transformerName string, streamCtx *transformer.StreamContext) ([]byte, error) {
	// Use the unified interface method instead of type assertion switch
	// All transformers now implement TransformResponseWithContext
	transformedEvent, err := trans.TransformResponseWithContext(eventData, true, streamCtx)
	if err != nil {
		return nil, err
	}
	if normalizedEvent, changed := normalizeOpenAIResponsesToolSearchArguments(transformedEvent, true); changed {
		transformedEvent = normalizedEvent
	}
	return transformedEvent, nil
}

// extractTokensFromEvent extracts token counts from SSE event
func (p *Proxy) extractTokensFromEvent(eventData []byte, inputTokens, outputTokens *int) {
	scanner := bufio.NewScanner(bytes.NewReader(eventData))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "" || jsonData == "[DONE]" {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}

		applyUsage := func(usage map[string]interface{}) {
			in, out := extractInputOutputTokens(usage)
			if in > 0 {
				*inputTokens = in
			}
			if out > 0 {
				*outputTokens = out
			}
		}

		// Claude-style events
		eventType, _ := event["type"].(string)
		if eventType == "message_start" {
			if message, ok := event["message"].(map[string]interface{}); ok {
				if usage, ok := message["usage"].(map[string]interface{}); ok {
					applyUsage(usage)
				}
			}
		} else if eventType == "message_delta" {
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				applyUsage(usage)
			}
		}

		// OpenAI Responses-style events
		if response, ok := event["response"].(map[string]interface{}); ok {
			if usage, ok := response["usage"].(map[string]interface{}); ok {
				applyUsage(usage)
			}
		}

		// OpenAI Chat chunk-style usage (top-level)
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			applyUsage(usage)
		}

		// Some providers wrap payloads with object=...
		if obj, ok := event["object"].(string); ok && strings.Contains(obj, "chat.completion") {
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				applyUsage(usage)
			}
		}
	}
}

// extractTextFromEvent extracts text content from transformed event
// Enhanced to support both delta.text and content_block_delta formats
func (p *Proxy) extractTextFromEvent(transformedEvent []byte, outputText *strings.Builder) {
	scanner := bufio.NewScanner(bytes.NewReader(transformedEvent))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		// Handle content_block_delta format (from some third-party APIs)
		if eventType == "content_block_delta" {
			if delta, ok := event["delta"].(map[string]interface{}); ok {
				if text, ok := delta["text"].(string); ok {
					outputText.WriteString(text)
				}
			}
		} else if delta, ok := event["delta"].(map[string]interface{}); ok {
			// Handle standard delta.text format
			if text, ok := delta["text"].(string); ok {
				outputText.WriteString(text)
			}
		}

		// Handle OpenAI Responses stream text delta format
		if eventType == "response.output_text.delta" {
			if delta, ok := event["delta"].(string); ok {
				outputText.WriteString(delta)
			}
		}

		// Handle OpenAI Chat stream chunk format (choices[].delta.content)
		if choices, ok := event["choices"].([]interface{}); ok {
			for _, choice := range choices {
				choiceMap, ok := choice.(map[string]interface{})
				if !ok {
					continue
				}
				delta, ok := choiceMap["delta"].(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := delta["content"].(string); ok {
					outputText.WriteString(text)
				}
			}
		}
	}
}

// isMessageStopEvent checks if the event is a message_stop event
func (p *Proxy) isMessageStopEvent(eventData []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(eventData))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)
		if eventType == "message_stop" {
			return true
		}
	}
	return false
}

// decompressGzip decompresses gzip-encoded response body
func decompressGzip(body io.ReadCloser) ([]byte, error) {
	gzipReader, err := gzip.NewReader(body)
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	return io.ReadAll(gzipReader)
}
