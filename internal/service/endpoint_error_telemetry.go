package service

import (
	"time"

	"github.com/lich0821/ccNexus/internal/onlinelicense"
	"github.com/lich0821/ccNexus/internal/storage"
)

type EndpointErrorTelemetryStore struct {
	storage *storage.SQLiteStorage
}

func NewEndpointErrorTelemetryStore(store *storage.SQLiteStorage) *EndpointErrorTelemetryStore {
	return &EndpointErrorTelemetryStore{storage: store}
}

func (s *EndpointErrorTelemetryStore) ListPendingEndpointErrorTelemetry(limit int) ([]onlinelicense.EndpointErrorTelemetryLocalRecord, error) {
	if s == nil || s.storage == nil {
		return nil, nil
	}
	records, err := s.storage.ListPendingEndpointErrorStats(limit)
	if err != nil {
		return nil, err
	}
	result := make([]onlinelicense.EndpointErrorTelemetryLocalRecord, 0, len(records))
	for _, record := range records {
		result = append(result, onlinelicense.EndpointErrorTelemetryLocalRecord{
			ID: record.ID,
			EndpointErrorTelemetryItem: onlinelicense.EndpointErrorTelemetryItem{
				EndpointName:        record.EndpointName,
				EndpointFingerprint: record.EndpointFingerprint,
				APIHost:             record.APIHost,
				APIURLFingerprint:   record.APIURLFingerprint,
				AuthMode:            record.AuthMode,
				Transformer:         record.Transformer,
				Model:               record.Model,
				Reason:              record.Reason,
				StatusCode:          record.StatusCode,
				Count:               record.Count,
				FirstAt:             record.FirstAt,
				LastAt:              record.LastAt,
				WindowStart:         record.WindowStart,
				WindowEnd:           record.WindowEnd,
				Sample:              record.Sample,
			},
		})
	}
	return result, nil
}

func (s *EndpointErrorTelemetryStore) MarkEndpointErrorTelemetryUploaded(ids []int64, uploadedAt time.Time) error {
	if s == nil || s.storage == nil {
		return nil
	}
	return s.storage.MarkEndpointErrorStatsUploaded(ids, uploadedAt)
}
