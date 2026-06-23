package proxy

import (
	"encoding/json"
	"fmt"
	"net/url"
)

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
