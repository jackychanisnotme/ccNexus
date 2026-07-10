package service

import (
	"encoding/json"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

// StatsService handles statistics operations
type StatsService struct {
	proxy   *proxy.Proxy
	config  *config.Config
	storage *storage.SQLiteStorage
}

// NewStatsService creates a new stats service
func NewStatsService(p *proxy.Proxy, cfg *config.Config, store *storage.SQLiteStorage) *StatsService {
	return &StatsService{proxy: p, config: cfg, storage: store}
}

// GetStats returns current statistics
func (s *StatsService) GetStats() string {
	totalSuccess, endpointStats := s.proxy.GetStats().GetStats()
	totalErrors := 0
	for _, stats := range endpointStats {
		totalErrors += stats.Errors
	}
	data, _ := json.Marshal(map[string]interface{}{
		"totalRequests":      totalSuccess,
		"totalAttempts":      totalSuccess + totalErrors,
		"successfulRequests": totalSuccess,
		"totalErrors":        totalErrors,
		"endpoints":          endpointStats,
	})
	return string(data)
}

// GetStatsDaily returns statistics for today
func (s *StatsService) GetStatsDaily() string {
	return s.GetStatsByPeriod("daily", "", "", "")
}

// GetStatsYesterday returns statistics for yesterday
func (s *StatsService) GetStatsYesterday() string {
	return s.GetStatsByPeriod("yesterday", "", "", "")
}

// GetStatsWeekly returns statistics for this week
func (s *StatsService) GetStatsWeekly() string {
	return s.GetStatsByPeriod("weekly", "", "", "")
}

// GetStatsMonthly returns statistics for this month
func (s *StatsService) GetStatsMonthly() string {
	return s.GetStatsByPeriod("monthly", "", "", "")
}

func (s *StatsService) GetStatsByPeriod(period, endpointName, clientIP, clientIPQuery string) string {
	startDate, endDate := statsPeriodDateRange(period)
	return s.getPeriodStatsFiltered(period, startDate, endDate, storage.StatsFilter{
		EndpointName:  endpointName,
		ClientIP:      clientIP,
		ClientIPQuery: clientIPQuery,
	})
}

func statsPeriodDateRange(period string) (string, string) {
	now := time.Now()
	switch period {
	case "yesterday":
		yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
		return yesterday, yesterday
	case "weekly":
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return now.AddDate(0, 0, -(weekday - 1)).Format("2006-01-02"), now.Format("2006-01-02")
	case "monthly":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02"), now.Format("2006-01-02")
	default:
		today := now.Format("2006-01-02")
		return today, today
	}
}

func (s *StatsService) getPeriodStats(period, startDate, endDate string) string {
	return s.getPeriodStatsFiltered(period, startDate, endDate, storage.StatsFilter{})
}

func (s *StatsService) getPeriodStatsFiltered(period, startDate, endDate string, filter storage.StatsFilter) string {
	var stats map[string]*proxy.DailyStats
	if startDate == endDate {
		stats = s.proxy.GetStats().GetDailyStatsFiltered(startDate, filter)
	} else {
		stats = s.proxy.GetStats().GetPeriodStatsFiltered(startDate, endDate, filter)
	}

	var totalRequests, totalErrors, totalInputTokens, totalOutputTokens int
	for _, st := range stats {
		totalRequests += st.Requests
		totalErrors += st.Errors
		totalInputTokens += st.InputTokens
		totalOutputTokens += st.OutputTokens
	}

	activeEndpoints, totalEndpoints := s.countEndpoints()

	result := map[string]interface{}{
		"period":             period,
		"totalRequests":      totalRequests,
		"totalAttempts":      totalRequests + totalErrors,
		"successfulRequests": totalRequests,
		"totalErrors":        totalErrors,
		"totalSuccess":       totalRequests - totalErrors,
		"totalInputTokens":   totalInputTokens,
		"totalOutputTokens":  totalOutputTokens,
		"activeEndpoints":    activeEndpoints,
		"totalEndpoints":     totalEndpoints,
		"endpoints":          stats,
	}
	if startDate == endDate {
		result["date"] = startDate
	} else {
		result["startDate"] = startDate
		result["endDate"] = endDate
	}

	data, _ := json.Marshal(result)
	return string(data)
}

func (s *StatsService) GetStatsFilters() string {
	if s.storage == nil {
		data, _ := json.Marshal(storage.StatsFilterOptions{})
		return string(data)
	}
	names := endpointNamesFromConfig(s.config)
	options, err := s.storage.GetStatsFilterOptions(names)
	if err != nil {
		data, _ := json.Marshal(map[string]interface{}{
			"endpoints": []interface{}{},
			"clientIps": []interface{}{},
			"error":     err.Error(),
		})
		return string(data)
	}
	data, _ := json.Marshal(options)
	return string(data)
}

func endpointNamesFromConfig(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	endpoints := cfg.GetEndpoints()
	names := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		names = append(names, endpoint.Name)
	}
	return names
}

func (s *StatsService) countEndpoints() (active, total int) {
	endpoints := s.config.GetEndpoints()
	total = len(endpoints)
	for _, ep := range endpoints {
		if ep.Enabled {
			active++
		}
	}
	return
}

// GetStatsTrend returns trend comparison data
func (s *StatsService) GetStatsTrend() string {
	return s.GetStatsTrendByPeriod("daily")
}

// GetStatsTrendByPeriod returns trend comparison data for specified period
func (s *StatsService) GetStatsTrendByPeriod(period string) string {
	return s.GetStatsTrendByPeriodFiltered(period, "", "", "")
}

func (s *StatsService) GetStatsTrendByPeriodFiltered(period, endpointName, clientIP, clientIPQuery string) string {
	now := time.Now()
	var currentStart, currentEnd, prevStart, prevEnd string

	switch period {
	case "yesterday":
		currentStart = now.AddDate(0, 0, -1).Format("2006-01-02")
		currentEnd = currentStart
		prevStart = now.AddDate(0, 0, -2).Format("2006-01-02")
		prevEnd = prevStart
	case "weekly":
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		thisWeekStart := now.AddDate(0, 0, -(weekday - 1))
		currentStart = thisWeekStart.Format("2006-01-02")
		currentEnd = now.Format("2006-01-02")
		prevStart = thisWeekStart.AddDate(0, 0, -7).Format("2006-01-02")
		prevEnd = thisWeekStart.AddDate(0, 0, -1).Format("2006-01-02")
	case "monthly":
		thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		currentStart = thisMonthStart.Format("2006-01-02")
		currentEnd = now.Format("2006-01-02")
		lastMonthStart := thisMonthStart.AddDate(0, -1, 0)
		prevStart = lastMonthStart.Format("2006-01-02")
		prevEnd = thisMonthStart.AddDate(0, 0, -1).Format("2006-01-02")
	default: // daily
		currentStart = now.Format("2006-01-02")
		currentEnd = currentStart
		prevStart = now.AddDate(0, 0, -1).Format("2006-01-02")
		prevEnd = prevStart
	}

	filter := storage.StatsFilter{
		EndpointName:  endpointName,
		ClientIP:      clientIP,
		ClientIPQuery: clientIPQuery,
	}
	current := s.sumStatsFiltered(currentStart, currentEnd, filter)
	prev := s.sumStatsFiltered(prevStart, prevEnd, filter)

	result := map[string]interface{}{
		"current":        current.requests,
		"previous":       prev.requests,
		"trend":          calculateTrend(current.requests, prev.requests),
		"currentErrors":  current.errors,
		"previousErrors": prev.errors,
		"errorsTrend":    calculateTrend(current.errors, prev.errors),
		"currentTokens":  current.tokens,
		"previousTokens": prev.tokens,
		"tokensTrend":    calculateTrend(current.tokens, prev.tokens),
	}

	data, _ := json.Marshal(result)
	return string(data)
}

type statsSummary struct {
	requests, errors, tokens int
}

func (s *StatsService) sumStats(startDate, endDate string) statsSummary {
	return s.sumStatsFiltered(startDate, endDate, storage.StatsFilter{})
}

func (s *StatsService) sumStatsFiltered(startDate, endDate string, filter storage.StatsFilter) statsSummary {
	var stats map[string]*proxy.DailyStats
	if startDate == endDate {
		stats = s.proxy.GetStats().GetDailyStatsFiltered(startDate, filter)
	} else {
		stats = s.proxy.GetStats().GetPeriodStatsFiltered(startDate, endDate, filter)
	}

	var sum statsSummary
	for _, st := range stats {
		sum.requests += st.Requests
		sum.errors += st.Errors
		sum.tokens += st.InputTokens + st.OutputTokens
	}
	return sum
}

func calculateTrend(current, previous int) float64 {
	if previous == 0 {
		if current == 0 {
			return 0
		}
		return 100.0
	}
	trend := ((float64(current) - float64(previous)) / float64(previous)) * 100.0
	if trend > 100.0 {
		return 100.0
	}
	if trend < -100.0 {
		return -100.0
	}
	return trend
}
