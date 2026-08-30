package domain

import "time"

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type RunStatus string

const (
	RunRunning         RunStatus = "running"
	RunWaitingApproval RunStatus = "waiting_approval"
	RunCompleted       RunStatus = "completed"
	RunRejected        RunStatus = "rejected"
	RunFailed          RunStatus = "failed"
)

type DiagnosisRequest struct {
	StoreID  string `json:"store_id"`
	Question string `json:"question"`
	Window   int    `json:"window_days,omitempty"`
}

type MetricPeriod struct {
	Label          string  `json:"label"`
	Revenue        float64 `json:"revenue"`
	Orders         int     `json:"orders"`
	Visitors       int     `json:"visitors"`
	ConversionRate float64 `json:"conversion_rate"`
	RefundRate     float64 `json:"refund_rate"`
	AdSpend        float64 `json:"ad_spend"`
	AdRevenue      float64 `json:"ad_revenue"`
}

type MetricsSnapshot struct {
	Current  MetricPeriod `json:"current"`
	Previous MetricPeriod `json:"previous"`
}

type InventoryItem struct {
	SKU              string  `json:"sku"`
	Name             string  `json:"name"`
	Available        int     `json:"available"`
	DailySales       float64 `json:"daily_sales"`
	StockoutHours    int     `json:"stockout_hours"`
	EstimatedDays    float64 `json:"estimated_cover_days"`
	ContributionRate float64 `json:"revenue_contribution_rate"`
}

type Campaign struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Spend    float64 `json:"spend"`
	Revenue  float64 `json:"revenue"`
	ROI      float64 `json:"roi"`
	PrevROI  float64 `json:"previous_roi"`
	Budget   float64 `json:"daily_budget"`
	Audience string  `json:"audience"`
}

type KnowledgeArticle struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

type StoreData struct {
	StoreID   string             `json:"store_id"`
	StoreName string             `json:"store_name"`
	Metrics   MetricsSnapshot    `json:"metrics"`
	Inventory []InventoryItem    `json:"inventory"`
	Campaigns []Campaign         `json:"campaigns"`
	Knowledge []KnowledgeArticle `json:"knowledge"`
}

type Evidence struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Ref     string `json:"ref"`
	Excerpt string `json:"excerpt"`
}

type Signal struct {
	Name         string   `json:"name"`
	Severity     string   `json:"severity"`
	Observation  string   `json:"observation"`
	DeltaPercent float64  `json:"delta_percent,omitempty"`
	EvidenceIDs  []string `json:"evidence_ids"`
}

type Action struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Tool           string         `json:"tool"`
	Arguments      map[string]any `json:"arguments"`
	Risk           RiskLevel      `json:"risk"`
	ExpectedImpact string         `json:"expected_impact"`
	Status         string         `json:"status"`
	Receipt        string         `json:"receipt,omitempty"`
}

type DiagnosisResult struct {
	RunID         string     `json:"run_id"`
	StoreID       string     `json:"store_id"`
	Summary       string     `json:"summary"`
	Signals       []Signal   `json:"signals"`
	Evidence      []Evidence `json:"evidence"`
	Actions       []Action   `json:"actions"`
	PolicyVersion string     `json:"policy_version"`
	Status        RunStatus  `json:"status"`
}

type ToolCall struct {
	Name       string         `json:"name"`
	Arguments  map[string]any `json:"arguments"`
	Result     any            `json:"result,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Error      string         `json:"error,omitempty"`
}

type Step struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	StartedAt  time.Time      `json:"started_at"`
	DurationMS int64          `json:"duration_ms"`
	Input      map[string]any `json:"input,omitempty"`
	Output     any            `json:"output,omitempty"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type ApprovalRequest struct {
	RunID     string   `json:"run_id"`
	Reason    string   `json:"reason"`
	ActionIDs []string `json:"action_ids"`
}

type ApprovalDecision struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
	Actor    string `json:"actor,omitempty"`
}

type Run struct {
	ID              string           `json:"id"`
	Request         DiagnosisRequest `json:"request"`
	Status          RunStatus        `json:"status"`
	PolicyVersion   string           `json:"policy_version"`
	StartedAt       time.Time        `json:"started_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
	Steps           []Step           `json:"steps"`
	Result          *DiagnosisResult `json:"result,omitempty"`
	PendingApproval *ApprovalRequest `json:"pending_approval,omitempty"`
	Error           string           `json:"error,omitempty"`
}

type Feedback struct {
	ID              string             `json:"id"`
	RunID           string             `json:"run_id"`
	Useful          bool               `json:"useful"`
	AcceptedActions []string           `json:"accepted_actions,omitempty"`
	RejectedActions []string           `json:"rejected_actions,omitempty"`
	ObservedKPIs    map[string]float64 `json:"observed_kpis,omitempty"`
	Comment         string             `json:"comment,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
}

type Policy struct {
	Version                 string    `json:"version"`
	ParentVersion           string    `json:"parent_version,omitempty"`
	ConversionDropThreshold float64   `json:"conversion_drop_threshold"`
	TrafficDropThreshold    float64   `json:"traffic_drop_threshold"`
	RefundRateThreshold     float64   `json:"refund_rate_threshold"`
	CampaignROIThreshold    float64   `json:"campaign_roi_threshold"`
	StockCoverDaysThreshold float64   `json:"stock_cover_days_threshold"`
	RequiredApprovalRisk    RiskLevel `json:"required_approval_risk"`
	RetrievalTopK           int       `json:"retrieval_top_k"`
	PromptRevision          string    `json:"prompt_revision"`
	Status                  string    `json:"status"`
	CreatedAt               time.Time `json:"created_at"`
	Rationale               string    `json:"rationale"`
}

type PolicyState struct {
	ActiveVersion   string            `json:"active_version"`
	PreviousVersion string            `json:"previous_version,omitempty"`
	CanaryVersion   string            `json:"canary_version,omitempty"`
	CanaryPercent   int               `json:"canary_percent,omitempty"`
	Policies        map[string]Policy `json:"policies"`
}

type ReplayCase struct {
	Name            string          `json:"name"`
	Metrics         MetricsSnapshot `json:"metrics"`
	Inventory       []InventoryItem `json:"inventory"`
	Campaigns       []Campaign      `json:"campaigns"`
	ExpectedSignals []string        `json:"expected_signals"`
	ForbiddenTools  []string        `json:"forbidden_auto_execute_tools"`
}

type EvalResult struct {
	ID             string             `json:"id"`
	PolicyVersion  string             `json:"policy_version"`
	Baseline       string             `json:"baseline_version,omitempty"`
	Score          float64            `json:"score"`
	SignalF1       float64            `json:"signal_f1"`
	SafetyScore    float64            `json:"safety_score"`
	CostScore      float64            `json:"cost_score"`
	Passed         bool               `json:"passed"`
	Regression     bool               `json:"regression"`
	CaseScores     map[string]float64 `json:"case_scores"`
	FailureReasons []string           `json:"failure_reasons,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
}
