package onlinelicense

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *HTTPHandler) handleAISettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := h.service.AISettingsFor(adminFromContext(r))
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		writeJSONSuccess(w, settings)
	case http.MethodPut:
		var settings AISettings
		if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		updated, err := h.service.UpdateAISettingsFor(adminFromContext(r), settings)
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		h.recordAudit("ai_settings_update", "ai_settings", 1, "AI analysis settings updated")
		writeJSONSuccess(w, updated)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *HTTPHandler) handleAIModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	models, err := h.service.AIModelsFor(adminFromContext(r))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, map[string]interface{}{"models": models})
}

func (h *HTTPHandler) handleAIJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		query, err := parseAIQuery(r.URL.Query())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		jobs, err := h.service.ListAIJobsFor(adminFromContext(r), query)
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		writeJSONSuccess(w, jobs)
	case http.MethodPost:
		if !h.allowRate(w, r, "admin_ai_run", 6, time.Minute) {
			return
		}
		var req AIJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		job, err := h.service.QueueAIJobFor(adminFromContext(r), req)
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		h.recordAudit("ai_job_queue", "ai_job", job.ID, fmt.Sprintf("type=%s owner=%d", job.JobType, job.OwnerAccountID))
		go func(jobID int64) {
			_ = h.service.RunAIJob(context.Background(), jobID)
		}(job.ID)
		writeJSONSuccess(w, job)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *HTTPHandler) handleAIFindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query, err := parseAIQuery(r.URL.Query())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := h.service.ListAIFindingsFor(adminFromContext(r), query)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, items)
}

func (h *HTTPHandler) handleAISupplierSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query, err := parseAIQuery(r.URL.Query())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if query.To.IsZero() {
		query.To = h.currentTime()
	}
	if query.From.IsZero() {
		query.From = query.To.Add(-24 * time.Hour)
	}
	items, err := h.service.SupplierSummaryFor(adminFromContext(r), query)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, items)
}

func (h *HTTPHandler) handleAIReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query, err := parseAIQuery(r.URL.Query())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	reports, err := h.service.ListAIReportsFor(adminFromContext(r), query)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, reports)
}

func (h *HTTPHandler) handleAIGenerateReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.allowRate(w, r, "admin_ai_run", 6, time.Minute) {
		return
	}
	var req AIReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	job, err := h.service.QueueAIJobFor(adminFromContext(r), AIJobRequest{
		JobType:        AIJobTypeMonthlyReport,
		OwnerAccountID: req.OwnerAccountID,
		From:           req.From,
		To:             req.To,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.recordAudit("ai_report_queue", "ai_job", job.ID, fmt.Sprintf("owner=%d", job.OwnerAccountID))
	go func(jobID int64) {
		_ = h.service.RunAIJob(context.Background(), jobID)
	}(job.ID)
	writeJSONSuccess(w, job)
}

func (h *HTTPHandler) handleAIReportDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/ai/reports/"), "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 {
		writeJSONError(w, http.StatusBadRequest, "invalid report path")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid report id")
		return
	}
	report, err := h.service.AIReportFor(adminFromContext(r), id)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	var body []byte
	switch parts[1] {
	case "html":
		body, err = RenderAIReportHTML(report)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case "json":
		body, err = aiReportJSON(report)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	case "csv":
		body, err = RenderAIReportCSV(report)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="endpoint-stability-report-%d.csv"`, report.ID))
	default:
		writeJSONError(w, http.StatusNotFound, "report format not found")
		return
	}
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	_, _ = w.Write(body)
}
