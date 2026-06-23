package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lich0821/ccNexus/internal/config"
)

const codexWebSocketIdleTimeout = 5 * time.Minute

type codexWebSocketDialFunc func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

type codexWebSocketHandshakeError struct {
	StatusCode int
	Err        error
}

func (e *codexWebSocketHandshakeError) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("codex websocket handshake failed: %v", e.Err)
	}
	return fmt.Sprintf("codex websocket handshake failed with status %d: %v", e.StatusCode, e.Err)
}

func (e *codexWebSocketHandshakeError) Unwrap() error {
	return e.Err
}

func isCodexWebSocketUnsupported(err error) bool {
	var handshakeErr *codexWebSocketHandshakeError
	if !errors.As(err, &handshakeErr) {
		return false
	}

	switch handshakeErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUpgradeRequired:
		return true
	default:
		return false
	}
}

func shouldUseCodexWebSocket(endpoint config.Endpoint, stream bool, clientFormat ClientFormat, transformerName string) bool {
	return config.NormalizeAuthMode(endpoint.AuthMode) == config.AuthModeCodexTokenPool &&
		stream &&
		clientFormat == ClientFormatOpenAIResponses &&
		transformerName == "cx_resp_openai2"
}

func proxyRequestBodyCopy(req *http.Request) ([]byte, error) {
	if req == nil || req.GetBody == nil {
		return nil, fmt.Errorf("proxy request body cannot be replayed")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func codexWebSocketURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported websocket source scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}

func buildCodexWebSocketFrame(payload []byte) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	body["type"] = "response.create"
	return json.Marshal(body)
}

func (p *Proxy) openCodexWebSocketStream(ctx context.Context, proxyReq *http.Request, endpoint config.Endpoint, payload []byte) (*http.Response, error) {
	if proxyReq == nil || proxyReq.URL == nil {
		return nil, fmt.Errorf("codex websocket request URL is required")
	}

	wsURL, err := codexWebSocketURL(proxyReq.URL.String())
	if err != nil {
		return nil, err
	}
	frame, err := buildCodexWebSocketFrame(payload)
	if err != nil {
		return nil, fmt.Errorf("build codex websocket frame: %w", err)
	}

	dial := p.codexWebSocketDial
	if dial == nil {
		dialer := *websocket.DefaultDialer
		if proxyURL := resolveProxyURLForRequest(p.config, proxyReq.URL, endpoint); strings.TrimSpace(proxyURL) != "" {
			parsedProxy, parseErr := url.Parse(proxyURL)
			if parseErr != nil {
				return nil, fmt.Errorf("parse codex websocket proxy: %w", parseErr)
			}
			dialer.Proxy = http.ProxyURL(parsedProxy)
		}
		dial = dialer.DialContext
	}

	conn, handshakeResp, err := dial(ctx, wsURL, codexWebSocketHeaders(proxyReq.Header))
	if err != nil {
		statusCode := 0
		if handshakeResp != nil {
			statusCode = handshakeResp.StatusCode
			if handshakeResp.Body != nil {
				_ = handshakeResp.Body.Close()
			}
		}
		return nil, &codexWebSocketHandshakeError{StatusCode: statusCode, Err: err}
	}
	conn.SetReadLimit(int64(maxStreamEventBytes))
	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set codex websocket write deadline: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send codex websocket request: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Time{})

	pipeReader, pipeWriter := io.Pipe()
	bridgeCtx, cancel := context.WithCancel(ctx)
	body := &codexWebSocketStreamBody{
		PipeReader: pipeReader,
		conn:       conn,
		cancel:     cancel,
	}

	go func() {
		<-bridgeCtx.Done()
		_ = conn.Close()
	}()
	go bridgeCodexWebSocketToSSE(bridgeCtx, conn, pipeWriter, cancel)

	headers := make(http.Header)
	if handshakeResp != nil {
		headers = handshakeResp.Header.Clone()
	}
	headers.Set("Content-Type", "text/event-stream")
	headers.Del("Content-Length")
	headers.Del("Content-Encoding")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     headers,
		Body:       body,
		Request:    proxyReq,
	}, nil
}

func codexWebSocketHeaders(source http.Header) http.Header {
	headers := source.Clone()
	for _, name := range []string{
		"Accept",
		"Accept-Encoding",
		"Connection",
		"Content-Length",
		"Host",
		"Sec-Websocket-Extensions",
		"Sec-Websocket-Key",
		"Sec-Websocket-Protocol",
		"Sec-Websocket-Version",
		"Upgrade",
	} {
		headers.Del(name)
	}
	return headers
}

func bridgeCodexWebSocketToSSE(ctx context.Context, conn *websocket.Conn, writer *io.PipeWriter, cancel context.CancelFunc) {
	defer cancel()
	completed := false
	for {
		if err := conn.SetReadDeadline(time.Now().Add(codexWebSocketIdleTimeout)); err != nil {
			_ = writer.CloseWithError(fmt.Errorf("set codex websocket read deadline: %w", err))
			return
		}
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				_ = writer.CloseWithError(ctx.Err())
			} else if completed {
				_ = writer.Close()
			} else {
				_ = writer.CloseWithError(fmt.Errorf("codex websocket closed before response.completed: %w", err))
			}
			return
		}
		if messageType != websocket.TextMessage {
			_ = writer.CloseWithError(fmt.Errorf("unexpected codex websocket message type %d", messageType))
			return
		}

		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			_ = writer.CloseWithError(fmt.Errorf("decode codex websocket event: %w", err))
			return
		}
		if strings.TrimSpace(event.Type) == "" {
			_ = writer.CloseWithError(fmt.Errorf("codex websocket event missing type"))
			return
		}
		if event.Type == "error" {
			_ = writer.CloseWithError(fmt.Errorf("codex websocket upstream error"))
			return
		}
		if _, err := writer.Write(append(append([]byte("data: "), payload...), []byte("\n\n")...)); err != nil {
			return
		}
		if event.Type == "response.completed" {
			completed = true
			_ = writer.Close()
			return
		}
	}
}

type codexWebSocketStreamBody struct {
	*io.PipeReader
	conn   *websocket.Conn
	cancel context.CancelFunc
	once   sync.Once
}

func (b *codexWebSocketStreamBody) Close() error {
	var closeErr error
	b.once.Do(func() {
		b.cancel()
		closeErr = b.PipeReader.Close()
		_ = b.conn.Close()
	})
	return closeErr
}
