package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer/convert"
)

func TestWriteClientToolChainInvalidError(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := &convert.InvalidToolChainError{
		Protocol:     "claude",
		CallID:       "call_1",
		MessageIndex: 22,
		Reason:       "tool call missing corresponding tool result",
	}

	if handled := writeClientToolChainInvalidError(recorder, requestObservability{}, "Poe opus4.8", 1, err); !handled {
		t.Fatalf("expected invalid tool chain error to be handled")
	}

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "call_1") || !strings.Contains(body, "invalid_request_error") {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func TestWriteClientToolChainInvalidErrorIgnoresOtherErrors(t *testing.T) {
	recorder := httptest.NewRecorder()

	if handled := writeClientToolChainInvalidError(recorder, requestObservability{}, "Poe opus4.8", 1, http.ErrBodyNotAllowed); handled {
		t.Fatalf("expected unrelated error to be ignored")
	}
}
