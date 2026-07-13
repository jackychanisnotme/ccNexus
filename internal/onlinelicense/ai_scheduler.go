package onlinelicense

import (
	"context"
	"log"
	"strings"
	"time"
)

const aiSchedulerInterval = time.Minute

func (s *Service) RunAIScheduler(ctx context.Context) {
	if s == nil {
		return
	}
	s.runScheduledAIJobs(ctx)
	ticker := time.NewTicker(aiSchedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runScheduledAIJobs(ctx)
		}
	}
}

func (s *Service) runScheduledAIJobs(ctx context.Context) {
	store, err := s.aiStore()
	if err != nil {
		return
	}
	settings, err := store.GetAISettings()
	if err != nil {
		log.Printf("load AI scheduler settings: %v", err)
		return
	}
	location, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		location, _ = time.LoadLocation(defaultAITimezone)
	}
	now := s.currentTime().In(location)
	dailyHour, dailyMinute := parseClockTime(settings.DailyTime, 2, 30)
	dailyEndLocal := time.Date(now.Year(), now.Month(), now.Day(), dailyHour, dailyMinute, 0, 0, location)
	if !now.Before(dailyEndLocal) {
		job, queueErr := s.queueAIJob(AIJobTypeDailyDiagnosis, 0, dailyEndLocal.Add(-24*time.Hour).UTC(), dailyEndLocal.UTC())
		if queueErr == nil && shouldRunAIJob(job) {
			if runErr := s.RunAIJob(ctx, job.ID); runErr != nil {
				log.Printf("run daily AI diagnosis: %v", runErr)
			}
		}
	}
	monthlyHour, monthlyMinute := parseClockTime(settings.MonthlyTime, 9, 0)
	monthlyRunAt := time.Date(now.Year(), now.Month(), 1, monthlyHour, monthlyMinute, 0, 0, location)
	if !now.Before(monthlyRunAt) {
		periodEnd := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
		periodStart := periodEnd.AddDate(0, -1, 0)
		ownerIDs, ownerErr := store.ListAIUsageOwnerIDs(periodStart.UTC(), periodEnd.UTC())
		if ownerErr != nil {
			log.Printf("list monthly AI report owners: %v", ownerErr)
		}
		for _, ownerID := range ownerIDs {
			job, queueErr := s.queueAIJob(AIJobTypeMonthlyReport, ownerID, periodStart.UTC(), periodEnd.UTC())
			if queueErr == nil && shouldRunAIJob(job) {
				if runErr := s.RunAIJob(ctx, job.ID); runErr != nil {
					log.Printf("run monthly AI report owner=%d: %v", ownerID, runErr)
				}
			}
		}
	}
}

func parseClockTime(value string, fallbackHour, fallbackMinute int) (int, int) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return fallbackHour, fallbackMinute
	}
	return parsed.Hour(), parsed.Minute()
}
