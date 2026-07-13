package onlinelicense

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AIProvider interface {
	ListModels(ctx context.Context) ([]string, error)
	Analyze(ctx context.Context, model string, payload interface{}) (AIModelAnalysis, error)
}

type AIModelAnalysis struct {
	Narrative string `json:"narrative"`
}

type OpenAICompatibleProvider struct {
	BaseURL string
	Client  *http.Client
}

type aiDataStore interface {
	ListEndpointUsageWindows(query AIQuery) ([]EndpointUsageRecord, error)
	ListEndpointErrorsForAI(query AIQuery) ([]EndpointErrorTelemetryRecord, error)
	ListAIUsageOwnerIDs(from, to time.Time) ([]int64, error)
	CleanupAIData(now time.Time) error
	GetAISettings() (AISettings, error)
	SaveAISettings(settings AISettings, now time.Time) error
	CreateAIJob(job *AIJob) error
	GetAIJob(id int64) (*AIJob, error)
	FindAIJob(jobType string, ownerAccountID int64, from, to time.Time, promptVersion string) (*AIJob, error)
	UpdateAIJob(job *AIJob) error
	ListAIJobs(query AIQuery) ([]AIJob, error)
	ReplaceAIFindings(jobID int64, findings []AIFinding, now time.Time) error
	ListAIFindings(query AIQuery) ([]AIFinding, error)
	SaveAIReport(report *AIReport) error
	GetAIReport(id int64) (*AIReport, error)
	ListAIReports(query AIQuery) ([]AIReport, error)
}

func NewOpenAICompatibleProvider(baseURL string) AIProvider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultAIGatewayURL
	}
	return &OpenAICompatibleProvider{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *OpenAICompatibleProvider) ListModels(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("model gateway returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&decoded); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(decoded.Data))
	seen := map[string]bool{}
	for _, item := range decoded.Data {
		id := strings.TrimSpace(item.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	sort.Strings(models)
	return models, nil
}

func (p *OpenAICompatibleProvider) Analyze(ctx context.Context, model string, payload interface{}) (AIModelAnalysis, error) {
	sanitized, err := json.Marshal(payload)
	if err != nil {
		return AIModelAnalysis{}, err
	}
	requestBody, err := json.Marshal(map[string]interface{}{
		"model": strings.TrimSpace(model),
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an operations analyst. Use only the supplied aggregate evidence. Return strict JSON with one field named narrative. Do not invent events, customers, devices, credentials, prompts, or responses.",
			},
			{"role": "user", "content": string(sanitized)},
		},
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0,
	})
	if err != nil {
		return AIModelAnalysis{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/v1/chat/completions", bytes.NewReader(requestBody))
	if err != nil {
		return AIModelAnalysis{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return AIModelAnalysis{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return AIModelAnalysis{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AIModelAnalysis{}, fmt.Errorf("model gateway returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Choices) == 0 {
		return AIModelAnalysis{}, fmt.Errorf("invalid model gateway response")
	}
	var analysis AIModelAnalysis
	if err := json.Unmarshal([]byte(strings.TrimSpace(decoded.Choices[0].Message.Content)), &analysis); err != nil {
		return AIModelAnalysis{}, fmt.Errorf("invalid structured AI response: %w", err)
	}
	analysis.Narrative = sanitizeAIText(analysis.Narrative, 8000)
	if analysis.Narrative == "" {
		return AIModelAnalysis{}, fmt.Errorf("empty AI narrative")
	}
	return analysis, nil
}

func (p *OpenAICompatibleProvider) httpClient() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (s *Service) aiStore() (aiDataStore, error) {
	store, ok := s.store.(aiDataStore)
	if !ok {
		return nil, fmt.Errorf("ai analysis storage is unavailable")
	}
	return store, nil
}

func (s *Service) AISettingsFor(actor *AdminAccount) (AISettings, error) {
	if actor == nil || !hasPermission(actor, PermissionAIAnalysisView) {
		return AISettings{}, ErrForbidden
	}
	store, err := s.aiStore()
	if err != nil {
		return AISettings{}, err
	}
	return store.GetAISettings()
}

func (s *Service) UpdateAISettingsFor(actor *AdminAccount, settings AISettings) (AISettings, error) {
	if actor == nil || !hasPermission(actor, PermissionAISettingsManage) {
		return AISettings{}, ErrForbidden
	}
	store, err := s.aiStore()
	if err != nil {
		return AISettings{}, err
	}
	settings.GatewayURL = defaultAIGatewayURL
	settings.Model = strings.TrimSpace(settings.Model)
	settings.Timezone = strings.TrimSpace(settings.Timezone)
	settings.DailyTime = strings.TrimSpace(settings.DailyTime)
	settings.MonthlyTime = strings.TrimSpace(settings.MonthlyTime)
	settings.PromptVersion = strings.TrimSpace(settings.PromptVersion)
	if settings.Timezone == "" {
		settings.Timezone = defaultAITimezone
	}
	if _, err := time.LoadLocation(settings.Timezone); err != nil {
		return AISettings{}, fmt.Errorf("invalid timezone")
	}
	if !validClockTime(settings.DailyTime) || !validClockTime(settings.MonthlyTime) {
		return AISettings{}, fmt.Errorf("invalid schedule time")
	}
	if settings.PromptVersion == "" {
		settings.PromptVersion = defaultAIPromptVersion
	}
	if settings.Enabled && settings.Model == "" {
		return AISettings{}, fmt.Errorf("analysis model is required when enabled")
	}
	if err := store.SaveAISettings(settings, s.currentTime()); err != nil {
		return AISettings{}, err
	}
	return store.GetAISettings()
}

func (s *Service) AIModelsFor(actor *AdminAccount) ([]string, error) {
	if actor == nil || !hasPermission(actor, PermissionAISettingsManage) {
		return nil, ErrForbidden
	}
	if s.aiProvider == nil {
		return []string{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.aiProvider.ListModels(ctx)
}

func (s *Service) QueueAIJobFor(actor *AdminAccount, req AIJobRequest) (*AIJob, error) {
	if actor == nil || !hasPermission(actor, PermissionAIAnalysisRun) {
		return nil, ErrForbidden
	}
	ownerID, err := s.aiOwnerScope(actor, req.OwnerAccountID)
	if err != nil {
		return nil, err
	}
	jobType := strings.TrimSpace(req.JobType)
	if jobType == "" {
		jobType = AIJobTypeManual
	}
	if jobType != AIJobTypeManual && jobType != AIJobTypeDailyDiagnosis && jobType != AIJobTypeMonthlyReport {
		return nil, fmt.Errorf("invalid ai job type")
	}
	from, to := normalizeAIWindow(req.From, req.To, s.currentTime())
	return s.queueAIJob(jobType, ownerID, from, to)
}

func (s *Service) queueAIJob(jobType string, ownerID int64, from, to time.Time) (*AIJob, error) {
	store, err := s.aiStore()
	if err != nil {
		return nil, err
	}
	settings, err := store.GetAISettings()
	if err != nil {
		return nil, err
	}
	job := &AIJob{
		JobType:        jobType,
		OwnerAccountID: ownerID,
		PeriodStart:    from.UTC(),
		PeriodEnd:      to.UTC(),
		PromptVersion:  settings.PromptVersion,
		Status:         AIJobStatusQueued,
		CreatedAt:      s.currentTime(),
	}
	if err := store.CreateAIJob(job); err != nil {
		if isUniqueConstraint(err) {
			return store.FindAIJob(job.JobType, job.OwnerAccountID, job.PeriodStart, job.PeriodEnd, job.PromptVersion)
		}
		return nil, err
	}
	return job, nil
}

func (s *Service) RunAIJob(ctx context.Context, jobID int64) error {
	store, err := s.aiStore()
	if err != nil {
		return err
	}
	job, err := store.GetAIJob(jobID)
	if err != nil {
		return err
	}
	if job.Status == AIJobStatusRunning || job.Status == AIJobStatusCompleted {
		return nil
	}
	now := s.currentTime()
	job.Status = AIJobStatusRunning
	job.Attempts++
	job.StartedAt = now
	job.Error = ""
	if err := store.UpdateAIJob(job); err != nil {
		return err
	}
	result, analysisErr := s.analyzeWindow(job.OwnerAccountID, job.PeriodStart, job.PeriodEnd)
	if analysisErr != nil {
		job.Status = AIJobStatusFailed
		job.Error = sanitizeAIText(analysisErr.Error(), 500)
		job.FinishedAt = s.currentTime()
		_ = store.UpdateAIJob(job)
		return analysisErr
	}
	if err := store.ReplaceAIFindings(job.ID, result.Findings, s.currentTime()); err != nil {
		job.Status = AIJobStatusFailed
		job.Error = sanitizeAIText(err.Error(), 500)
		job.FinishedAt = s.currentTime()
		_ = store.UpdateAIJob(job)
		return err
	}
	settings, _ := store.GetAISettings()
	if settings.Enabled && strings.TrimSpace(settings.Model) != "" && s.aiProvider != nil {
		modelInput := map[string]interface{}{
			"periodStart": job.PeriodStart,
			"periodEnd":   job.PeriodEnd,
			"suppliers":   result.Suppliers,
			"findings":    redactFindingsForModel(result.Findings),
		}
		modelCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		modelResult, modelErr := s.aiProvider.Analyze(modelCtx, settings.Model, modelInput)
		cancel()
		if modelErr != nil {
			job.Error = sanitizeAIText(modelErr.Error(), 500)
		} else {
			result.Narrative = modelResult.Narrative
		}
	} else {
		job.Status = AIJobStatusSkippedNoAIProvider
	}
	resultJSON, _ := json.Marshal(result)
	job.Result = resultJSON
	if job.Status != AIJobStatusSkippedNoAIProvider {
		job.Status = AIJobStatusCompleted
	}
	job.FinishedAt = s.currentTime()
	if job.JobType == AIJobTypeMonthlyReport {
		report := buildAIReport(job, result, s.currentTime())
		if err := store.SaveAIReport(&report); err != nil {
			job.Status = AIJobStatusFailed
			job.Error = sanitizeAIText(err.Error(), 500)
		}
	}
	if err := store.UpdateAIJob(job); err != nil {
		return err
	}
	_ = store.CleanupAIData(s.currentTime())
	return nil
}

func (s *Service) analyzeWindow(ownerID int64, from, to time.Time) (AIAnalysisResult, error) {
	store, err := s.aiStore()
	if err != nil {
		return AIAnalysisResult{}, err
	}
	query := AIQuery{OwnerAccountID: ownerID, From: from, To: to}
	usage, err := store.ListEndpointUsageWindows(query)
	if err != nil {
		return AIAnalysisResult{}, err
	}
	errorRows, err := store.ListEndpointErrorsForAI(query)
	if err != nil {
		return AIAnalysisResult{}, err
	}
	return correlateAIAnalysis(usage, errorRows, to.Sub(from) >= 27*24*time.Hour, s.currentTime()), nil
}

func (s *Service) ListAIJobsFor(actor *AdminAccount, query AIQuery) ([]AIJob, error) {
	if actor == nil || !hasPermission(actor, PermissionAIAnalysisView) {
		return nil, ErrForbidden
	}
	ownerID, err := s.aiOwnerScope(actor, query.OwnerAccountID)
	if err != nil {
		return nil, err
	}
	query.OwnerAccountID = ownerID
	store, err := s.aiStore()
	if err != nil {
		return nil, err
	}
	return store.ListAIJobs(query)
}

func (s *Service) ListAIFindingsFor(actor *AdminAccount, query AIQuery) ([]AIFinding, error) {
	if actor == nil || !hasPermission(actor, PermissionAIAnalysisView) {
		return nil, ErrForbidden
	}
	ownerID, err := s.aiOwnerScope(actor, query.OwnerAccountID)
	if err != nil {
		return nil, err
	}
	query.OwnerAccountID = ownerID
	if query.DeviceID != "" {
		if ok, scopeErr := s.deviceInScope(actor, query.DeviceID); scopeErr != nil || !ok {
			return nil, ErrForbidden
		}
	}
	store, err := s.aiStore()
	if err != nil {
		return nil, err
	}
	return store.ListAIFindings(query)
}

func (s *Service) SupplierSummaryFor(actor *AdminAccount, query AIQuery) ([]SupplierStabilitySummary, error) {
	if actor == nil || !hasPermission(actor, PermissionAIAnalysisView) {
		return nil, ErrForbidden
	}
	ownerID, err := s.aiOwnerScope(actor, query.OwnerAccountID)
	if err != nil {
		return nil, err
	}
	query.OwnerAccountID = ownerID
	store, err := s.aiStore()
	if err != nil {
		return nil, err
	}
	usage, err := store.ListEndpointUsageWindows(query)
	if err != nil {
		return nil, err
	}
	errorRows, err := store.ListEndpointErrorsForAI(query)
	if err != nil {
		return nil, err
	}
	result := correlateAIAnalysis(usage, errorRows, query.To.Sub(query.From) >= 27*24*time.Hour, s.currentTime())
	return result.Suppliers, nil
}

func (s *Service) ListAIReportsFor(actor *AdminAccount, query AIQuery) ([]AIReport, error) {
	if actor == nil || !hasPermission(actor, PermissionAIReportsView) {
		return nil, ErrForbidden
	}
	ownerID, err := s.aiOwnerScope(actor, query.OwnerAccountID)
	if err != nil {
		return nil, err
	}
	query.OwnerAccountID = ownerID
	store, err := s.aiStore()
	if err != nil {
		return nil, err
	}
	return store.ListAIReports(query)
}

func (s *Service) AIReportFor(actor *AdminAccount, id int64) (*AIReport, error) {
	if actor == nil || !hasPermission(actor, PermissionAIReportsView) {
		return nil, ErrForbidden
	}
	store, err := s.aiStore()
	if err != nil {
		return nil, err
	}
	report, err := store.GetAIReport(id)
	if err != nil {
		return nil, err
	}
	ownerID, err := s.aiOwnerScope(actor, report.OwnerAccountID)
	if err != nil {
		return nil, err
	}
	if actor.Level != AdminLevelRoot && ownerID != report.OwnerAccountID {
		return nil, ErrForbidden
	}
	return report, nil
}

func (s *Service) aiOwnerScope(actor *AdminAccount, requested int64) (int64, error) {
	if actor == nil {
		return 0, ErrForbidden
	}
	if actor.Level == AdminLevelRoot && requested == 0 {
		return 0, nil
	}
	if requested == 0 {
		requested = actor.ID
	}
	ok, err := s.accountInScope(actor, requested)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrForbidden
	}
	return requested, nil
}

func correlateAIAnalysis(usage []EndpointUsageRecord, errorsRows []EndpointErrorTelemetryRecord, monthly bool, now time.Time) AIAnalysisResult {
	type hostAggregate struct {
		requests         int
		errors           int
		supplierFailures int
		lastSeen         time.Time
		devices          map[string]bool
	}
	hostStats := map[string]*hostAggregate{}
	for _, item := range usage {
		host := normalizedAIHost(item.APIHost, item.EndpointName)
		entry := hostStats[host]
		if entry == nil {
			entry = &hostAggregate{devices: map[string]bool{}}
			hostStats[host] = entry
		}
		entry.requests += nonNegativeInt(item.Requests)
		entry.errors += nonNegativeInt(item.Errors)
		entry.devices[item.DeviceID] = true
		if item.WindowEnd.After(entry.lastSeen) {
			entry.lastSeen = item.WindowEnd
		}
	}
	errorDevicesByHost := map[string]map[string]bool{}
	for _, item := range errorsRows {
		host := normalizedAIHost(item.APIHost, item.EndpointName)
		if errorDevicesByHost[host] == nil {
			errorDevicesByHost[host] = map[string]bool{}
		}
		errorDevicesByHost[host][item.DeviceID] = true
		if hostStats[host] == nil {
			hostStats[host] = &hostAggregate{devices: map[string]bool{}}
		}
		hostStats[host].devices[item.DeviceID] = true
	}
	findings := make([]AIFinding, 0)
	for _, item := range errorsRows {
		host := normalizedAIHost(item.APIHost, item.EndpointName)
		category := classifyEndpointStabilityError(item.Reason, item.StatusCode)
		classification := string(category)
		hostDeviceCount := len(hostStats[host].devices)
		errorDeviceCount := len(errorDevicesByHost[host])
		if category == EndpointStabilityFindingSupplierIssue && errorDeviceCount < 2 {
			classification = AIFindingUnknown
		}
		if category == EndpointStabilityFindingCustomerNetwork {
			if errorDeviceCount >= 2 {
				classification = AIFindingSupplierIssue
			} else if hostDeviceCount < 2 {
				classification = AIFindingUnknown
			} else {
				classification = AIFindingCustomerNetwork
			}
		}
		if category == EndpointStabilityFindingCustomerConfigOrAccount {
			classification = AIFindingCustomerConfigAccount
		}
		if classification == AIFindingSupplierIssue {
			hostStats[host].supplierFailures += nonNegativeInt(item.Count)
		}
		evidence, _ := json.Marshal(map[string]interface{}{
			"reason":              item.Reason,
			"statusCode":          item.StatusCode,
			"hostDeviceCount":     hostDeviceCount,
			"affectedDeviceCount": errorDeviceCount,
			"windowStart":         item.WindowStart,
			"windowEnd":           item.WindowEnd,
		})
		findings = append(findings, AIFinding{
			OwnerAccountID:  item.OwnerAccountID,
			DeviceID:        item.DeviceID,
			APIHost:         host,
			EndpointName:    item.EndpointName,
			Classification:  classification,
			Confidence:      aiFindingConfidence(classification, hostDeviceCount, errorDeviceCount),
			Severity:        aiFindingSeverity(item.Count, hostStats[host].requests),
			Count:           nonNegativeInt(item.Count),
			Evidence:        evidence,
			Recommendation:  aiRecommendation(classification),
			CustomerSummary: aiCustomerSummary(classification, host),
			CreatedAt:       now.UTC(),
		})
	}
	minimum := EndpointStabilityDailyMinimumRequests
	if monthly {
		minimum = EndpointStabilityMonthlyMinimumRequests
	}
	suppliers := make([]SupplierStabilitySummary, 0, len(hostStats))
	for host, item := range hostStats {
		score := 0.0
		if item.requests > 0 {
			score = math.Max(0, 100*(1-float64(minInt(item.supplierFailures, item.requests))/float64(item.requests)))
		}
		suppliers = append(suppliers, SupplierStabilitySummary{
			APIHost:          host,
			Requests:         item.requests,
			Errors:           item.errors,
			SupplierFailures: item.supplierFailures,
			Score:            math.Round(score*100) / 100,
			SampleSufficient: item.requests >= minimum,
			DeviceCount:      len(item.devices),
			LastSeen:         item.lastSeen,
		})
	}
	sort.Slice(suppliers, func(i, j int) bool {
		if suppliers[i].SampleSufficient != suppliers[j].SampleSufficient {
			return suppliers[i].SampleSufficient
		}
		if suppliers[i].Score != suppliers[j].Score {
			return suppliers[i].Score > suppliers[j].Score
		}
		return suppliers[i].APIHost < suppliers[j].APIHost
	})
	return AIAnalysisResult{Findings: findings, Suppliers: suppliers}
}

func buildAIReport(job *AIJob, result AIAnalysisResult, now time.Time) AIReport {
	metrics, _ := json.Marshal(map[string]interface{}{
		"suppliers": result.Suppliers,
		"findings":  redactFindingsForModel(result.Findings),
	})
	title := fmt.Sprintf("端点稳定性月报 %s - %s", job.PeriodStart.Format("2006-01-02"), job.PeriodEnd.Format("2006-01-02"))
	narrative := result.Narrative
	if narrative == "" {
		narrative = deterministicReportNarrative(result)
	}
	return AIReport{
		JobID:          job.ID,
		OwnerAccountID: job.OwnerAccountID,
		PeriodStart:    job.PeriodStart,
		PeriodEnd:      job.PeriodEnd,
		Title:          title,
		Metrics:        metrics,
		Narrative:      narrative,
		CreatedAt:      now.UTC(),
	}
}

func deterministicReportNarrative(result AIAnalysisResult) string {
	if len(result.Suppliers) == 0 {
		return "统计周期内暂无足够的端点使用数据。"
	}
	issues := 0
	for _, finding := range result.Findings {
		if finding.Classification == AIFindingSupplierIssue {
			issues += finding.Count
		}
	}
	return fmt.Sprintf("本周期共评估 %d 个供应商入口，识别 %d 次供应商侧失败。评分仅基于聚合请求量和脱敏错误遥测。", len(result.Suppliers), issues)
}

func RenderAIReportHTML(report *AIReport) ([]byte, error) {
	if report == nil {
		return nil, sql.ErrNoRows
	}
	const page = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>body{font:14px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#1d1d1f;max-width:980px;margin:0 auto;padding:36px}h1{font-size:28px}pre{white-space:pre-wrap;background:#f5f5f7;padding:16px;border:1px solid #ddd}footer{margin-top:36px;color:#6e6e73}@media print{body{padding:0}}</style></head><body><h1>{{.Title}}</h1><p>统计周期：{{.PeriodStart.Format "2006-01-02"}} 至 {{.PeriodEnd.Format "2006-01-02"}}</p><h2>分析摘要</h2><p>{{.Narrative}}</p><h2>结构化指标</h2><pre>{{printf "%s" .Metrics}}</pre><footer>本报告基于聚合遥测生成，不包含客户提示词、响应正文、API Key 或 Token。</footer></body></html>`
	tmpl, err := template.New("report").Parse(page)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, report); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func RenderAIReportCSV(report *AIReport) ([]byte, error) {
	if report == nil {
		return nil, sql.ErrNoRows
	}
	var metrics struct {
		Suppliers []SupplierStabilitySummary `json:"suppliers"`
	}
	if err := json.Unmarshal(report.Metrics, &metrics); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	_ = writer.Write([]string{"API Host", "Requests", "Errors", "Supplier Failures", "Score", "Sample Sufficient", "Devices"})
	for _, item := range metrics.Suppliers {
		_ = writer.Write([]string{item.APIHost, strconv.Itoa(item.Requests), strconv.Itoa(item.Errors),
			strconv.Itoa(item.SupplierFailures), strconv.FormatFloat(item.Score, 'f', 2, 64),
			strconv.FormatBool(item.SampleSufficient), strconv.Itoa(item.DeviceCount)})
	}
	writer.Flush()
	return output.Bytes(), writer.Error()
}

func redactFindingsForModel(findings []AIFinding) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(findings))
	for _, finding := range findings {
		result = append(result, map[string]interface{}{
			"apiHost":        finding.APIHost,
			"classification": finding.Classification,
			"confidence":     finding.Confidence,
			"severity":       finding.Severity,
			"count":          finding.Count,
			"evidence":       finding.Evidence,
		})
	}
	return result
}

func normalizeAIWindow(from, to, now time.Time) (time.Time, time.Time) {
	if to.IsZero() {
		to = now
	}
	if from.IsZero() || !from.Before(to) {
		from = to.Add(-24 * time.Hour)
	}
	return from.UTC(), to.UTC()
}

func normalizedAIHost(host, endpointName string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if parsed, err := url.Parse("//" + host); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	if host == "" {
		host = "endpoint:" + strings.TrimSpace(endpointName)
	}
	return host
}

func validClockTime(value string) bool {
	_, err := time.Parse("15:04", strings.TrimSpace(value))
	return err == nil
}

func aiFindingConfidence(classification string, hostDevices, affectedDevices int) float64 {
	switch classification {
	case AIFindingSupplierIssue:
		if affectedDevices >= 3 {
			return 0.95
		}
		return 0.85
	case AIFindingCustomerNetwork:
		if hostDevices >= 3 {
			return 0.85
		}
		return 0.7
	case AIFindingCustomerConfigAccount:
		return 0.9
	default:
		return 0.4
	}
}

func aiFindingSeverity(count, requests int) string {
	rate := endpointStabilityRate(count, requests)
	return endpointStabilitySeverity(rate)
}

func aiRecommendation(classification string) string {
	switch classification {
	case AIFindingSupplierIssue:
		return "核对供应商状态页和同时间段跨设备错误，必要时切换备用供应商。"
	case AIFindingCustomerNetwork:
		return "检查客户 DNS、代理、防火墙和出口网络，并对比同供应商其他设备。"
	case AIFindingCustomerConfigAccount:
		return "检查账户额度、授权范围、API Key 或 Token 状态，不要在报告中暴露凭证。"
	default:
		return "继续收集样本并结合后续时间窗口复核。"
	}
}

func aiCustomerSummary(classification, host string) string {
	switch classification {
	case AIFindingSupplierIssue:
		return fmt.Sprintf("%s 在统计窗口内出现跨设备上游异常。", host)
	case AIFindingCustomerNetwork:
		return fmt.Sprintf("访问 %s 时检测到局部网络异常。", host)
	case AIFindingCustomerConfigAccount:
		return fmt.Sprintf("访问 %s 时检测到账户或配置类异常。", host)
	default:
		return fmt.Sprintf("%s 的异常证据暂不足以完成归因。", host)
	}
}

func sanitizeAIText(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	value = endpointTelemetrySecretPattern.ReplaceAllString(value, "[redacted]")
	value = endpointTelemetryURLQueryPattern.ReplaceAllString(value, "$1?[redacted]")
	value = endpointTelemetryKVSecretPattern.ReplaceAllString(value, "$1=[redacted]")
	if maxLength > 0 && len(value) > maxLength {
		value = value[:maxLength]
	}
	return value
}

func parseAIQuery(values url.Values) (AIQuery, error) {
	query := AIQuery{
		DeviceID:       strings.TrimSpace(values.Get("deviceId")),
		APIHost:        strings.TrimSpace(values.Get("host")),
		Classification: strings.TrimSpace(values.Get("classification")),
	}
	if raw := strings.TrimSpace(values.Get("ownerAccountId")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return query, fmt.Errorf("invalid ownerAccountId")
		}
		query.OwnerAccountID = value
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return query, fmt.Errorf("invalid limit")
		}
		query.Limit = value
	}
	if raw := strings.TrimSpace(values.Get("from")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, fmt.Errorf("invalid from")
		}
		query.From = value.UTC()
	}
	if raw := strings.TrimSpace(values.Get("to")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, fmt.Errorf("invalid to")
		}
		query.To = value.UTC()
	}
	return query, nil
}

func isAIJobTerminal(status string) bool {
	return status == AIJobStatusCompleted || status == AIJobStatusFailed || status == AIJobStatusSkippedNoAIProvider
}

func aiReportJSON(report *AIReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func aiStoreErrorIsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
