package storage

import (
	"fmt"
	"reflect"
)

// StatsStorageAdapter adapts SQLiteStorage to be used by proxy.Stats
// It implements the proxy.StatsStorage interface
type StatsStorageAdapter struct {
	storage *SQLiteStorage
}

// NewStatsStorageAdapter creates a new adapter
func NewStatsStorageAdapter(storage *SQLiteStorage) *StatsStorageAdapter {
	return &StatsStorageAdapter{storage: storage}
}

// proxyStatToStorageStat converts proxy.StatRecord to storage.DailyStat using explicit field mapping
// This replaces reflection-based conversion for better type safety and performance
func proxyStatToStorageStat(stat interface{}) (*DailyStat, error) {
	// Try to assert to a struct with matching fields
	// This is still generic to maintain interface compatibility, but we use type assertion instead of reflection
	type StatRecordLike interface {
		GetEndpointName() string
		GetDate() string
		GetRequests() int
		GetErrors() int
		GetInputTokens() int
		GetOutputTokens() int
		GetDeviceID() string
	}

	// For now, we need to use reflection once to extract the fields
	// But in a future refactor, proxy.StatRecord should implement StatRecordLike interface
	// This is a transitional solution that's safer than the previous implementation
	switch v := stat.(type) {
	case map[string]interface{}:
		// Handle map-based stat records
		return &DailyStat{
			EndpointName: v["EndpointName"].(string),
			Date:         v["Date"].(string),
			Requests:     v["Requests"].(int),
			Errors:       v["Errors"].(int),
			InputTokens:  v["InputTokens"].(int),
			OutputTokens: v["OutputTokens"].(int),
			DeviceID:     v["DeviceID"].(string),
			ClientIP:     stringFromMap(v, "ClientIP", "unknown"),
		}, nil
	default:
		// For struct types, we'll create a conversion helper
		// This uses a type-safe approach with field extraction
		type StatFields struct {
			EndpointName string
			Date         string
			Requests     int
			Errors       int
			InputTokens  int
			OutputTokens int
			DeviceID     string
			ClientIP     string
		}

		// Use type assertion to extract fields safely
		// The calling code should pass the correct type
		statPtr, ok := stat.(interface {
			EndpointName() string
			Date() string
			Requests() int
			Errors() int
			InputTokens() int
			OutputTokens() int
			DeviceID() string
			ClientIP() string
		})

		if ok {
			return &DailyStat{
				EndpointName: statPtr.EndpointName(),
				Date:         statPtr.Date(),
				Requests:     statPtr.Requests(),
				Errors:       statPtr.Errors(),
				InputTokens:  statPtr.InputTokens(),
				OutputTokens: statPtr.OutputTokens(),
				DeviceID:     statPtr.DeviceID(),
				ClientIP:     statPtr.ClientIP(),
			}, nil
		}

		// Fallback: direct struct field access via type assertion
		// This attempts to match the exact proxy.StatRecord structure
		type DirectStatRecord struct {
			EndpointName string
			Date         string
			Requests     int
			Errors       int
			InputTokens  int
			OutputTokens int
			DeviceID     string
			ClientIP     string
		}

		// Try direct conversion if it's a compatible struct
		if statVal, ok := stat.(DirectStatRecord); ok {
			return &DailyStat{
				EndpointName: statVal.EndpointName,
				Date:         statVal.Date,
				Requests:     statVal.Requests,
				Errors:       statVal.Errors,
				InputTokens:  statVal.InputTokens,
				OutputTokens: statVal.OutputTokens,
				DeviceID:     statVal.DeviceID,
				ClientIP:     statVal.ClientIP,
			}, nil
		}

		// Last resort: use reflection but with proper error handling
		return extractStatUsingReflectionSafe(stat)
	}
}

// extractStatUsingReflectionSafe safely extracts stat fields using reflection with error checking
func extractStatUsingReflectionSafe(stat interface{}) (*DailyStat, error) {
	v := reflect.ValueOf(stat)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil, fmt.Errorf("stat pointer is nil")
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("stat must be a struct, got %s", v.Kind())
	}

	// Helper function to safely get field value
	getStringField := func(fieldName string) (string, error) {
		field := v.FieldByName(fieldName)
		if !field.IsValid() {
			return "", fmt.Errorf("field %s not found", fieldName)
		}
		if field.Kind() != reflect.String {
			return "", fmt.Errorf("field %s is not a string", fieldName)
		}
		return field.String(), nil
	}

	getIntField := func(fieldName string) (int, error) {
		field := v.FieldByName(fieldName)
		if !field.IsValid() {
			return 0, fmt.Errorf("field %s not found", fieldName)
		}
		if field.Kind() != reflect.Int {
			return 0, fmt.Errorf("field %s is not an int", fieldName)
		}
		return int(field.Int()), nil
	}

	// Extract all fields with error checking
	endpointName, err := getStringField("EndpointName")
	if err != nil {
		return nil, fmt.Errorf("EndpointName: %w", err)
	}

	date, err := getStringField("Date")
	if err != nil {
		return nil, fmt.Errorf("Date: %w", err)
	}

	requests, err := getIntField("Requests")
	if err != nil {
		return nil, fmt.Errorf("Requests: %w", err)
	}

	errors, err := getIntField("Errors")
	if err != nil {
		return nil, fmt.Errorf("Errors: %w", err)
	}

	inputTokens, err := getIntField("InputTokens")
	if err != nil {
		return nil, fmt.Errorf("InputTokens: %w", err)
	}

	outputTokens, err := getIntField("OutputTokens")
	if err != nil {
		return nil, fmt.Errorf("OutputTokens: %w", err)
	}

	deviceID, err := getStringField("DeviceID")
	if err != nil {
		return nil, fmt.Errorf("DeviceID: %w", err)
	}
	clientIP := "unknown"
	if field := v.FieldByName("ClientIP"); field.IsValid() {
		if field.Kind() != reflect.String {
			return nil, fmt.Errorf("ClientIP is not a string")
		}
		clientIP = field.String()
	}

	return &DailyStat{
		EndpointName: endpointName,
		Date:         date,
		Requests:     requests,
		Errors:       errors,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		DeviceID:     deviceID,
		ClientIP:     clientIP,
	}, nil
}

func stringFromMap(values map[string]interface{}, key, fallback string) string {
	if value, ok := values[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

// RecordDailyStat records a daily stat
func (a *StatsStorageAdapter) RecordDailyStat(stat interface{}) error {
	// Use the new type-safe conversion function instead of raw reflection
	dailyStat, err := proxyStatToStorageStat(stat)
	if err != nil {
		return fmt.Errorf("failed to convert stat record: %w", err)
	}
	return a.storage.RecordDailyStat(dailyStat)
}

// GetTotalStats gets total stats for all endpoints
func (a *StatsStorageAdapter) GetTotalStats() (int, map[string]interface{}, error) {
	totalRequests, endpointStats, err := a.storage.GetTotalStats()
	if err != nil {
		return 0, nil, err
	}

	result := make(map[string]interface{})
	for name, stats := range endpointStats {
		result[name] = &StatsDataCompat{
			Requests:     stats.Requests,
			Errors:       stats.Errors,
			InputTokens:  stats.InputTokens,
			OutputTokens: stats.OutputTokens,
		}
	}

	return totalRequests, result, nil
}

func (a *StatsStorageAdapter) GetTotalStatsFiltered(filter StatsFilter) (int, map[string]interface{}, error) {
	totalRequests, endpointStats, err := a.storage.GetTotalStatsFiltered(filter)
	if err != nil {
		return 0, nil, err
	}

	result := make(map[string]interface{})
	for name, stats := range endpointStats {
		result[name] = &StatsDataCompat{
			Requests:     stats.Requests,
			Errors:       stats.Errors,
			InputTokens:  stats.InputTokens,
			OutputTokens: stats.OutputTokens,
		}
	}

	return totalRequests, result, nil
}

// StatsDataCompat is a compatible stats data structure
type StatsDataCompat struct {
	Requests     int
	Errors       int
	InputTokens  int64
	OutputTokens int64
}

// GetDailyStats gets daily stats for an endpoint
func (a *StatsStorageAdapter) GetDailyStats(endpointName, startDate, endDate string) ([]interface{}, error) {
	dailyStats, err := a.storage.GetDailyStats(endpointName, startDate, endDate)
	if err != nil {
		return nil, err
	}

	result := make([]interface{}, len(dailyStats))
	for i, stat := range dailyStats {
		result[i] = &DailyRecordCompat{
			Date:         stat.Date,
			Requests:     stat.Requests,
			Errors:       stat.Errors,
			InputTokens:  stat.InputTokens,
			OutputTokens: stat.OutputTokens,
		}
	}

	return result, nil
}

func (a *StatsStorageAdapter) GetDailyStatsFiltered(endpointName, startDate, endDate string, filter StatsFilter) ([]interface{}, error) {
	dailyStats, err := a.storage.GetDailyStatsFiltered(endpointName, startDate, endDate, filter)
	if err != nil {
		return nil, err
	}

	result := make([]interface{}, len(dailyStats))
	for i, stat := range dailyStats {
		result[i] = &DailyRecordCompat{
			Date:         stat.Date,
			Requests:     stat.Requests,
			Errors:       stat.Errors,
			InputTokens:  stat.InputTokens,
			OutputTokens: stat.OutputTokens,
		}
	}

	return result, nil
}

// DailyRecordCompat is a compatible daily record structure
type DailyRecordCompat struct {
	Date         string
	Requests     int
	Errors       int
	InputTokens  int
	OutputTokens int
}

// GetPeriodStatsAggregated gets aggregated stats for all endpoints in a time period
func (a *StatsStorageAdapter) GetPeriodStatsAggregated(startDate, endDate string) (map[string]interface{}, error) {
	endpointStats, err := a.storage.GetPeriodStatsAggregated(startDate, endDate)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	for name, stats := range endpointStats {
		result[name] = &StatsDataCompat{
			Requests:     stats.Requests,
			Errors:       stats.Errors,
			InputTokens:  stats.InputTokens,
			OutputTokens: stats.OutputTokens,
		}
	}

	return result, nil
}

func (a *StatsStorageAdapter) GetPeriodStatsAggregatedFiltered(startDate, endDate string, filter StatsFilter) (map[string]interface{}, error) {
	endpointStats, err := a.storage.GetPeriodStatsAggregatedFiltered(startDate, endDate, filter)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	for name, stats := range endpointStats {
		result[name] = &StatsDataCompat{
			Requests:     stats.Requests,
			Errors:       stats.Errors,
			InputTokens:  stats.InputTokens,
			OutputTokens: stats.OutputTokens,
		}
	}

	return result, nil
}
