package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/tokencount"
	"github.com/lich0821/ccNexus/internal/transformer"
)

const (
	streamFinishCompleted             = "completed"
	streamFinishClientCanceled        = "client_canceled"
	streamFinishUpstreamStreamError   = "upstream_stream_error"
	streamFinishDownstreamWriteFailed = "downstream_write_failed"
	streamFinishTransformFailed       = "transform_failed"
)

type streamResponseResult struct {
	InputTokens       int
	OutputTokens      int
	OutputText        string
	Completed         bool
	WroteData         bool
	WroteSemanticData bool
	Reason            string
	Err               error
}

// handleStreamingResponse processes streaming SSE responses
func (p *Proxy) handleStreamingResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, endpoint config.Endpoint, trans transformer.Transformer, transformerName string, thinkingEnabled bool, modelName string, bodyBytes []byte, credentialID int64) streamResponseResult {
	result := streamResponseResult{}

	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("[%s] ResponseWriter does not support flushing", endpoint.Name)
		resp.Body.Close()
		result.Reason = streamFinishDownstreamWriteFailed
		result.Err = fmt.Errorf("response writer does not support flushing")
		return result
	}

	headersCommitted := false
	commitHeaders := func() {
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
		// Pre-estimate input tokens for fallback
		if bodyBytes != nil {
			streamCtx.InputTokens = p.estimateInputTokens(bodyBytes)
		}
	}

	scanner := bufio.NewScanner(reader)
	// Increase buffer sizes to handle large SSE events (e.g., large file reads in tool calls)
	buf := make([]byte, 0, 128*1024) // 128KB initial buffer (was 64KB)
	scanner.Buffer(buf, 2*1024*1024) // 2MB max buffer (was 1MB)

	var inputTokens, outputTokens int
	var buffer bytes.Buffer
	var pendingWrites bytes.Buffer
	var outputText strings.Builder
	eventCount := 0
	streamDone := false
	semanticDataSeen := false
	emptyKind := ""

	writeTransformedEvent := func(data []byte, semantic bool) error {
		if len(data) == 0 {
			return nil
		}
		if !semanticDataSeen {
			pendingWrites.Write(data)
			if !semantic {
				return nil
			}
			commitHeaders()
			if _, writeErr := w.Write(pendingWrites.Bytes()); writeErr != nil {
				return writeErr
			}
			pendingWrites.Reset()
			semanticDataSeen = true
			result.WroteSemanticData = true
			result.WroteData = true
			flusher.Flush()
			return nil
		}

		commitHeaders()
		if _, writeErr := w.Write(data); writeErr != nil {
			return writeErr
		}
		result.WroteData = true
		flusher.Flush()
		return nil
	}

	for scanner.Scan() && !streamDone {
		line := scanner.Text()

		if strings.Contains(line, "data: [DONE]") {
			streamDone = true
			result.Completed = true
			result.Reason = streamFinishCompleted

			// Token Usage Fallback: Inject message_delta with estimated output_tokens before [DONE]
			if outputTokens == 0 && outputText.Len() > 0 {
				outputTokens = tokencount.EstimateOutputTokens(outputText.String())
				logger.Debug("[%s] Token fallback before [DONE]: estimated output_tokens=%d", endpoint.Name, outputTokens)

				// Update stream context for transformer fallback
				if streamCtx != nil {
					streamCtx.OutputTokens = outputTokens
				}

				// Inject message_delta event with usage
				deltaEvent := fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", outputTokens)
				if writeErr := writeTransformedEvent([]byte(deltaEvent), false); writeErr != nil {
					result.Completed = false
					result.Reason = streamFinishDownstreamWriteFailed
					result.Err = writeErr
					break
				}
			}

			buffer.WriteString(line + "\n")
			eventData := buffer.Bytes()
			logger.DebugLog("[%s] SSE Event #%d (Original): %s", endpoint.Name, eventCount+1, string(eventData))

			transformedEvent, err := p.transformStreamEvent(eventData, trans, transformerName, streamCtx)
			if err == nil && len(transformedEvent) > 0 {
				logger.DebugLog("[%s] SSE Event #%d (Transformed): %s", endpoint.Name, eventCount+1, string(transformedEvent))
				streamInspection := inspectSemanticStreamEvent(transformedEvent)
				if streamInspection.EmptyKind != "" {
					emptyKind = streamInspection.EmptyKind
				}
				if writeErr := writeTransformedEvent(transformedEvent, streamInspection.HasOutput); writeErr != nil {
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

				// Inject message_delta event with usage before message_stop
				deltaEvent := fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", outputTokens)
				if writeErr := writeTransformedEvent([]byte(deltaEvent), false); writeErr != nil {
					result.Reason = streamFinishDownstreamWriteFailed
					result.Err = writeErr
					streamDone = true
					break
				}
			}

			transformedEvent, err := p.transformStreamEvent(eventData, trans, transformerName, streamCtx)
			if err != nil {
				logger.Error("[%s] Failed to transform SSE event: %v", endpoint.Name, err)
				result.Reason = streamFinishTransformFailed
				result.Err = err
				streamDone = true
			} else if len(transformedEvent) > 0 {
				logger.DebugLog("[%s] SSE Event #%d (Transformed): %s", endpoint.Name, eventCount, string(transformedEvent))

				p.extractTokensFromEvent(transformedEvent, &inputTokens, &outputTokens)
				p.extractTextFromEvent(transformedEvent, &outputText)
				streamInspection := inspectSemanticStreamEvent(transformedEvent)
				semanticEvent := streamInspection.HasOutput
				if streamInspection.EmptyKind != "" {
					emptyKind = streamInspection.EmptyKind
				}

				if writeErr := writeTransformedEvent(transformedEvent, semanticEvent); writeErr != nil {
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
		if isClientCanceled(ctx, err) {
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
		result.Reason = streamFinishCompleted
		result.Completed = true
	}
	result.InputTokens = inputTokens
	result.OutputTokens = outputTokens
	result.OutputText = outputText.String()
	if result.Err == nil && result.Completed && !result.WroteSemanticData {
		result.Reason = retryReasonSemanticEmptyResponse
		result.Completed = false
		result.Err = newSemanticEmptyResponseError(emptyKind, outputTokens, outputText.Len())
	}
	return result
}

// handleStreamingAsNonStreaming aggregates SSE and returns a single non-stream response.
// This is used for Codex endpoints that require stream=true upstream while client requested non-stream.
func (p *Proxy) handleStreamingAsNonStreaming(w http.ResponseWriter, resp *http.Response, endpoint config.Endpoint, trans transformer.Transformer, credentialID int64) (int, int, string, error) {
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
	scanner.Buffer(buf, 2*1024*1024)

	var completedPayload []byte
	var lastJSONPayload []byte
	chatAccumulator := newOpenAIChatStreamAccumulator()
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

	if semanticErr := semanticEmptyErrorForResponse(transformedResp, outputTokens); semanticErr != nil {
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
	w.Write(transformedResp)

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
	return trans.TransformResponseWithContext(eventData, true, streamCtx)
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
