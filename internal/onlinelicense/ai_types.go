package onlinelicense

import (
	"encoding/json"
	"time"
)

const (
	PermissionAIAnalysisView   = "ai:analysis:view"
	PermissionAIAnalysisRun    = "ai:analysis:run"
	PermissionAIReportsView    = "ai:reports:view"
	PermissionAISettingsManage = "ai:settings:manage"

	AIJobTypeDailyDiagnosis = "daily_diagnosis"
	AIJobTypeMonthlyReport  = "monthly_report"
	AIJobTypeManual         = "manual"

	AIJobStatusQueued              = "queued"
	AIJobStatusRunning             = "running"
	AIJobStatusCompleted           = "completed"
	AIJobStatusFailed              = "failed"
	AIJobStatusSkippedNoAIProvider = "skipped_no_ai_provider"

	AIFindingSupplierIssue         = "supplier_issue"
	AIFindingCustomerNetwork       = "customer_network"
	AIFindingCustomerConfigAccount = "customer_config_or_account"
	AIFindingUnknown               = "unknown"

	defaultAIGatewayURL    = "http://127.0.0.1:24221"
	defaultAITimezone      = "Asia/Shanghai"
	defaultAIDailyTime     = "02:30"
	defaultAIMonthlyTime   = "09:00"
	defaultAIPromptVersion = "endpoint-stability-v1"
)

type EndpointUsageRecord struct {
	ID                  int64     `json:"id,omitempty"`
	DeviceID            string    `json:"deviceId,omitempty"`
	OwnerAccountID      int64     `json:"ownerAccountId,omitempty"`
	EndpointName        string    `json:"endpointName"`
	EndpointFingerprint string    `json:"endpointFingerprint"`
	APIHost             string    `json:"apiHost,omitempty"`
	AuthMode            string    `json:"authMode,omitempty"`
	Transformer         string    `json:"transformer,omitempty"`
	Model               string    `json:"model,omitempty"`
	WindowStart         time.Time `json:"windowStart"`
	WindowEnd           time.Time `json:"windowEnd"`
	Requests            int       `json:"requests"`
	Errors              int       `json:"errors"`
	InputTokens         int       `json:"inputTokens"`
	OutputTokens        int       `json:"outputTokens"`
}

type AISettings struct {
	Enabled       bool      `json:"enabled"`
	GatewayURL    string    `json:"gatewayUrl"`
	Model         string    `json:"model"`
	Timezone      string    `json:"timezone"`
	DailyTime     string    `json:"dailyTime"`
	MonthlyTime   string    `json:"monthlyTime"`
	PromptVersion string    `json:"promptVersion"`
	UpdatedAt     time.Time `json:"updatedAt,omitempty"`
}

type AIJob struct {
	ID             int64           `json:"id"`
	JobType        string          `json:"jobType"`
	OwnerAccountID int64           `json:"ownerAccountId,omitempty"`
	PeriodStart    time.Time       `json:"periodStart"`
	PeriodEnd      time.Time       `json:"periodEnd"`
	PromptVersion  string          `json:"promptVersion"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	Error          string          `json:"error,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	StartedAt      time.Time       `json:"startedAt,omitempty"`
	FinishedAt     time.Time       `json:"finishedAt,omitempty"`
}

type AIFinding struct {
	ID              int64           `json:"id,omitempty"`
	JobID           int64           `json:"jobId"`
	OwnerAccountID  int64           `json:"ownerAccountId,omitempty"`
	DeviceID        string          `json:"deviceId,omitempty"`
	APIHost         string          `json:"apiHost,omitempty"`
	EndpointName    string          `json:"endpointName,omitempty"`
	Classification  string          `json:"classification"`
	Confidence      float64         `json:"confidence"`
	Severity        string          `json:"severity"`
	Count           int             `json:"count"`
	Evidence        json.RawMessage `json:"evidence,omitempty"`
	Recommendation  string          `json:"recommendation,omitempty"`
	CustomerSummary string          `json:"customerSummary,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
}

type SupplierStabilitySummary struct {
	APIHost          string    `json:"apiHost"`
	Requests         int       `json:"requests"`
	Errors           int       `json:"errors"`
	SupplierFailures int       `json:"supplierFailures"`
	Score            float64   `json:"score"`
	SampleSufficient bool      `json:"sampleSufficient"`
	DeviceCount      int       `json:"deviceCount"`
	LastSeen         time.Time `json:"lastSeen,omitempty"`
}

type AIReport struct {
	ID             int64           `json:"id"`
	JobID          int64           `json:"jobId,omitempty"`
	OwnerAccountID int64           `json:"ownerAccountId,omitempty"`
	PeriodStart    time.Time       `json:"periodStart"`
	PeriodEnd      time.Time       `json:"periodEnd"`
	Title          string          `json:"title"`
	Metrics        json.RawMessage `json:"metrics"`
	Narrative      string          `json:"narrative"`
	CreatedAt      time.Time       `json:"createdAt"`
}

type AIJobRequest struct {
	JobType        string    `json:"jobType"`
	OwnerAccountID int64     `json:"ownerAccountId,omitempty"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
}

type AIReportRequest struct {
	OwnerAccountID int64     `json:"ownerAccountId,omitempty"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
}

type AIQuery struct {
	OwnerAccountID int64
	DeviceID       string
	APIHost        string
	Classification string
	From           time.Time
	To             time.Time
	Limit          int
}

type AIAnalysisResult struct {
	Findings  []AIFinding                `json:"findings"`
	Suppliers []SupplierStabilitySummary `json:"suppliers"`
	Narrative string                     `json:"narrative,omitempty"`
}
