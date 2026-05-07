package proxy

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lich0821/ccNexus/internal/logger"
)

func (p *Proxy) handleOllamaTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	models := p.loadModelsForResponse(false)
	tags := make([]map[string]interface{}, 0, len(models))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, model := range models {
		tags = append(tags, map[string]interface{}{
			"name":        model.ID,
			"model":       model.ID,
			"modified_at": now,
			"size":        0,
			"digest":      "",
			"details": map[string]interface{}{
				"format":             "openai",
				"family":             model.OwnedBy,
				"families":           []string{},
				"parameter_size":     "",
				"quantization_level": "",
			},
		})
	}

	writeProxyJSON(w, map[string]interface{}{"models": tags})
}

func (p *Proxy) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeProxyJSON(w, map[string]interface{}{
		"version": "ccNexus",
	})
}

func (p *Proxy) handleProps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	current := p.getCurrentEndpoint()
	writeProxyJSON(w, map[string]interface{}{
		"model":         current.Model,
		"default_model": current.Model,
		"endpoint":      current.Name,
	})
}

func writeProxyJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Debug("Failed to encode compatibility response: %v", err)
	}
}
