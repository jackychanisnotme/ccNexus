package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

// handleStatsSummary returns overall statistics
func (h *Handler) handleStatsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	totalRequests, endpointStats := h.proxy.GetStats().GetStatsFiltered(statsFilterFromRequest(r))

	// Calculate totals
	totalErrors := 0
	var totalInputTokens int64 = 0
	var totalOutputTokens int64 = 0

	for _, stats := range endpointStats {
		totalErrors += stats.Errors
		totalInputTokens += int64(stats.InputTokens)
		totalOutputTokens += int64(stats.OutputTokens)
	}

	WriteSuccess(w, map[string]interface{}{
		"TotalRequests":     totalRequests,
		"TotalErrors":       totalErrors,
		"TotalInputTokens":  totalInputTokens,
		"TotalOutputTokens": totalOutputTokens,
		"Endpoints":         endpointStats,
	})
}

// handleStatsDaily returns today's statistics
func (h *Handler) handleStatsDaily(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	today := time.Now().Format("2006-01-02")
	stats, err := h.getStatsForPeriod(today, today, statsFilterFromRequest(r))
	if err != nil {
		logger.Error("Failed to get daily stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get daily stats")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"period": "daily",
		"date":   today,
		"stats":  stats,
	})
}

// handleStatsWeekly returns this week's statistics
func (h *Handler) handleStatsWeekly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	now := time.Now()
	// Get start of week (Monday)
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday
	}
	startOfWeek := now.AddDate(0, 0, -(weekday - 1))
	startDate := startOfWeek.Format("2006-01-02")
	endDate := now.Format("2006-01-02")

	stats, err := h.getStatsForPeriod(startDate, endDate, statsFilterFromRequest(r))
	if err != nil {
		logger.Error("Failed to get weekly stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get weekly stats")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"period":    "weekly",
		"startDate": startDate,
		"endDate":   endDate,
		"stats":     stats,
	})
}

// handleStatsMonthly returns this month's statistics
func (h *Handler) handleStatsMonthly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	startDate := startOfMonth.Format("2006-01-02")
	endDate := now.Format("2006-01-02")

	stats, err := h.getStatsForPeriod(startDate, endDate, statsFilterFromRequest(r))
	if err != nil {
		logger.Error("Failed to get monthly stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get monthly stats")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"period":    "monthly",
		"startDate": startDate,
		"endDate":   endDate,
		"stats":     stats,
	})
}

// handleStatsTrends returns trend comparison data
func (h *Handler) handleStatsTrends(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Get today's stats
	filter := statsFilterFromRequest(r)
	todayStats, err := h.getStatsForPeriod(today, today, filter)
	if err != nil {
		logger.Error("Failed to get today's stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get trend stats")
		return
	}

	// Get yesterday's stats
	yesterdayStats, err := h.getStatsForPeriod(yesterday, yesterday, filter)
	if err != nil {
		logger.Error("Failed to get yesterday's stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get trend stats")
		return
	}

	// Calculate changes
	trends := map[string]interface{}{
		"todayVsYesterday": map[string]interface{}{
			"requests": map[string]interface{}{
				"today":     todayStats["totalRequests"],
				"yesterday": yesterdayStats["totalRequests"],
				"change":    calculatePercentChange(yesterdayStats["totalRequests"].(int), todayStats["totalRequests"].(int)),
			},
			"errors": map[string]interface{}{
				"today":     todayStats["totalErrors"],
				"yesterday": yesterdayStats["totalErrors"],
				"change":    calculatePercentChange(yesterdayStats["totalErrors"].(int), todayStats["totalErrors"].(int)),
			},
			"inputTokens": map[string]interface{}{
				"today":     todayStats["totalInputTokens"],
				"yesterday": yesterdayStats["totalInputTokens"],
				"change":    calculatePercentChange(int(yesterdayStats["totalInputTokens"].(int64)), int(todayStats["totalInputTokens"].(int64))),
			},
			"outputTokens": map[string]interface{}{
				"today":     todayStats["totalOutputTokens"],
				"yesterday": yesterdayStats["totalOutputTokens"],
				"change":    calculatePercentChange(int(yesterdayStats["totalOutputTokens"].(int64)), int(todayStats["totalOutputTokens"].(int64))),
			},
		},
	}

	WriteSuccess(w, trends)
}

func (h *Handler) handleStatsFilters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	endpoints := h.config.GetEndpoints()
	currentNames := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.Name) != "" {
			currentNames = append(currentNames, endpoint.Name)
		}
	}

	options, err := h.storage.GetStatsFilterOptions(currentNames)
	if err != nil {
		logger.Error("Failed to get stats filter options: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get stats filter options")
		return
	}
	WriteSuccess(w, options)
}

func statsFilterFromRequest(r *http.Request) storage.StatsFilter {
	query := r.URL.Query()
	return storage.StatsFilter{
		EndpointName:  strings.TrimSpace(firstQueryValue(query.Get("endpoint"), query.Get("endpointName"))),
		ClientIP:      strings.TrimSpace(firstQueryValue(query.Get("clientIp"), query.Get("clientIP"))),
		ClientIPQuery: strings.TrimSpace(firstQueryValue(query.Get("clientIpQuery"), query.Get("clientIPQuery"))),
	}
}

func firstQueryValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// getStatsForPeriod retrieves statistics for a date range
func (h *Handler) getStatsForPeriod(startDate, endDate string, filter storage.StatsFilter) (map[string]interface{}, error) {
	allStats, err := h.storage.GetPeriodStatsAggregatedFiltered(startDate, endDate, filter)
	if err != nil {
		return nil, err
	}

	totalRequests := 0
	totalErrors := 0
	var totalInputTokens int64 = 0
	var totalOutputTokens int64 = 0
	endpointStats := make(map[string]interface{})

	for endpointName, stats := range allStats {
		if stats.Requests > 0 {
			endpointStats[endpointName] = map[string]interface{}{
				"requests":     stats.Requests,
				"errors":       stats.Errors,
				"inputTokens":  stats.InputTokens,
				"outputTokens": stats.OutputTokens,
			}

			totalRequests += stats.Requests
			totalErrors += stats.Errors
			totalInputTokens += stats.InputTokens
			totalOutputTokens += stats.OutputTokens
		}
	}

	return map[string]interface{}{
		"totalRequests":     totalRequests,
		"totalErrors":       totalErrors,
		"totalSuccess":      totalRequests - totalErrors,
		"totalInputTokens":  totalInputTokens,
		"totalOutputTokens": totalOutputTokens,
		"endpoints":         endpointStats,
	}, nil
}

// calculatePercentChange calculates the percentage change between two values
func calculatePercentChange(old, new int) float64 {
	if old == 0 {
		if new == 0 {
			return 0
		}
		return 100.0
	}
	return float64(new-old) / float64(old) * 100.0
}
