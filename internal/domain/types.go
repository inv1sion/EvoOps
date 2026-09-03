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
	Task     string `json:"task,omitempty"` // auto (default), diagnosis, data_query, knowledge_qa
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
	Preference     string         `json:"preference,omitempty"`
	MemoryRefs     []string       `json:"memory_refs,omitempty"`
	Status         string         `json:"status"`
	Receipt        string         `json:"receipt,omitempty"`
}

type DiagnosisResult struct {
	Task          string     `json:"task,omitempty"`
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
	ID             string         `json:"id,omitempty"`
	Round          int            `json:"round,omitempty"`
	Origin         string         `json:"origin,omitempty"`
	ModelArguments string         `json:"model_arguments,omitempty"`
	Cached         bool           `json:"cached,omitempty"`
	ErrorCode      string         `json:"error_code,omitempty"`
	Name           string         `json:"name"`
	Arguments      map[string]any `json:"arguments"`
	Result         any            `json:"result,omitempty"`
	DurationMS     int64          `json:"duration_ms"`
	Error          string         `json:"error,omitempty"`
}

// ModelTurn stores observable decisions, not private reasoning / chain-of-thought.
type ModelTurn struct {
	Round            int                `json:"round"`
	DurationMS       int64              `json:"duration_ms"`
	Requests         []ModelToolRequest `json:"requests,omitempty"`
	FinalOutput      string             `json:"final_output,omitempty"`
	Error            string             `json:"error,omitempty"`
	FinishReason     string             `json:"finish_reason,omitempty"`
	PromptTokens     int                `json:"prompt_tokens,omitempty"`
	CompletionTokens int                `json:"completion_tokens,omitempty"`
}

type ModelToolRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
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
	ModelTurns []ModelTurn    `json:"model_turns,omitempty"`
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
	ExecutionMode   string           `json:"execution_mode,omitempty"`
	ID              string           `json:"id"`
	Mode            string           `json:"mode"`
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
	StoreID         string             `json:"store_id"`
	Useful          bool               `json:"useful"`
	AcceptedActions []string           `json:"accepted_actions,omitempty"`
	RejectedActions []string           `json:"rejected_actions,omitempty"`
	ObservedKPIs    map[string]float64 `json:"observed_kpis,omitempty"`
	Comment         string             `json:"comment,omitempty"`
	MemoryUpdates   []MerchantMemory   `json:"memory_updates,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
}

// MerchantMemory is an auditable, tenant-scoped fact learned from explicit
// feedback. It may personalize action ordering and explanation, but never
// changes an action's risk or bypasses the approval gate.
type MerchantMemory struct {
	ID               string    `json:"id"`
	StoreID          string    `json:"store_id"`
	Kind             string    `json:"kind"`
	Operation        string    `json:"operation,omitempty"`
	Target           string    `json:"target,omitempty"`
	Polarity         string    `json:"polarity"`
	Statement        string    `json:"statement"`
	Confidence       float64   `json:"confidence"`
	Source           string    `json:"source"`
	SourceRunID      string    `json:"source_run_id"`
	SourceFeedbackID string    `json:"source_feedback_id"`
	EvidenceRefs     []string  `json:"evidence_refs,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type MerchantMemoryProfile struct {
	StoreID   string           `json:"store_id"`
	Memories  []MerchantMemory `json:"memories"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type Policy struct {
	Version                 string           `json:"version"`
	ParentVersion           string           `json:"parent_version,omitempty"`
	CampaignROIThreshold    float64          `json:"campaign_roi_threshold"`
	RequiredApprovalRisk    RiskLevel        `json:"required_approval_risk"`
	RetrievalTopK           int              `json:"retrieval_top_k"`
	RetrievalCandidateK     int              `json:"retrieval_candidate_k"`
	DenseWeight             float64          `json:"dense_weight"`
	SparseWeight            float64          `json:"sparse_weight"`
	RRFK                    int              `json:"rrf_k"`
	MergeThreshold          float64          `json:"merge_threshold"`
	RelevanceThreshold      float64          `json:"relevance_threshold"`
	RerankEnabled           bool             `json:"rerank_enabled"`
	QueryRewriteStrategy    string           `json:"query_rewrite_strategy"`
	MaxWorkflowSteps        int              `json:"max_workflow_steps"`
	MaxToolCalls            int              `json:"max_tool_calls"`
	MaxCostUnits            float64          `json:"max_cost_units"`
	PromptRevision          string           `json:"prompt_revision"`
	Prompt                  PromptArtifact   `json:"prompt"`
	Status                  string           `json:"status"`
	CreatedAt               time.Time        `json:"created_at"`
	EvaluatedAt             *time.Time       `json:"evaluated_at,omitempty"`
	EvaluationReportID      string           `json:"evaluation_report_id,omitempty"`
	EvaluatedAgainstVersion string           `json:"evaluated_against_version,omitempty"`
	EvaluatedSuiteVersion   string           `json:"evaluated_suite_version,omitempty"`
	Rationale               string           `json:"rationale"`
	Mutations               []PolicyMutation `json:"mutations,omitempty"`
}

// PromptArtifact is the complete, immutable prompt version used by a policy.
// Storing the full text (rather than only a label) makes every replay
// reproducible and every model-generated mutation auditable.
type PromptArtifact struct {
	Version              string           `json:"version"`
	ParentVersion        string           `json:"parent_version,omitempty"`
	Content              string           `json:"content"`
	Patch                string           `json:"patch,omitempty"`
	Generator            string           `json:"generator"`
	GeneratorModel       string           `json:"generator_model,omitempty"`
	GenerationDurationMS int64            `json:"generation_duration_ms,omitempty"`
	Rationale            string           `json:"rationale"`
	FailureEvidence      []string         `json:"failure_evidence,omitempty"`
	Validation           PromptValidation `json:"validation"`
	CreatedAt            time.Time        `json:"created_at"`
}

type PromptValidation struct {
	Passed bool            `json:"passed"`
	Checks map[string]bool `json:"checks"`
	Errors []string        `json:"errors,omitempty"`
}

type PolicyMutation struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
	Reason string `json:"reason"`
}

type PolicyState struct {
	ActiveVersion   string            `json:"active_version"`
	PreviousVersion string            `json:"previous_version,omitempty"`
	CanaryVersion   string            `json:"canary_version,omitempty"`
	CanaryPercent   int               `json:"canary_percent,omitempty"`
	Policies        map[string]Policy `json:"policies"`
}

type KnowledgeChunk struct {
	ID       string   `json:"id"`
	DocID    string   `json:"doc_id"`
	ParentID string   `json:"parent_id,omitempty"`
	Level    int      `json:"level"`
	Title    string   `json:"title"`
	Text     string   `json:"text"`
	Tags     []string `json:"tags,omitempty"`
}

type RetrievalConfig struct {
	TopK                 int     `json:"top_k"`
	CandidateK           int     `json:"candidate_k"`
	DenseWeight          float64 `json:"dense_weight"`
	SparseWeight         float64 `json:"sparse_weight"`
	RRFK                 int     `json:"rrf_k"`
	MergeThreshold       float64 `json:"merge_threshold"`
	RelevanceThreshold   float64 `json:"relevance_threshold"`
	RerankEnabled        bool    `json:"rerank_enabled"`
	QueryRewriteStrategy string  `json:"query_rewrite_strategy"`
}

type RetrievalHit struct {
	Chunk       KnowledgeChunk `json:"chunk"`
	DenseScore  float64        `json:"dense_score"`
	SparseScore float64        `json:"sparse_score"`
	RRFScore    float64        `json:"rrf_score"`
	RerankScore float64        `json:"rerank_score"`
	MergedFrom  []string       `json:"merged_from,omitempty"`
}

type RetrievalTrace struct {
	OriginalQuery  string   `json:"original_query"`
	EffectiveQuery string   `json:"effective_query"`
	RewriteUsed    bool     `json:"rewrite_used"`
	RewriteReason  string   `json:"rewrite_reason,omitempty"`
	DenseRanking   []string `json:"dense_ranking"`
	SparseRanking  []string `json:"sparse_ranking"`
	FusedRanking   []string `json:"fused_ranking"`
	MergedIDs      []string `json:"merged_ids,omitempty"`
	FinalRanking   []string `json:"final_ranking"`
	DurationMS     int64    `json:"duration_ms"`
}

type RetrievalResult struct {
	Hits  []RetrievalHit `json:"hits"`
	Trace RetrievalTrace `json:"trace"`
	Cost  float64        `json:"cost_units"`
}

type HarnessCase struct {
	ID                              string             `json:"id"`
	Description                     string             `json:"description"`
	StoreID                         string             `json:"store_id"`
	Question                        string             `json:"question"`
	RelevantChunkIDs                []string           `json:"relevant_chunk_ids"`
	ExpectedSignals                 []string           `json:"expected_signals"`
	ExpectedOperations              []string           `json:"expected_operations"`
	ForbiddenAutoOperations         []string           `json:"forbidden_auto_operations"`
	RequiredTools                   []string           `json:"required_tools"`
	ExpectedStepSequence            []string           `json:"expected_step_sequence"`
	ExpectedToolCallingStepSequence []string           `json:"expected_tool_calling_step_sequence,omitempty"`
	MemoryFixture                   []MerchantMemory   `json:"memory_fixture,omitempty"`
	ExpectedPreferences             map[string]string  `json:"expected_preferences,omitempty"`
	OutcomeWeights                  map[string]float64 `json:"outcome_weights,omitempty"`
	MaxLatencyMS                    int64              `json:"max_latency_ms"`
	MaxCostUnits                    float64            `json:"max_cost_units"`
}

type HarnessLayerReport struct {
	Name     string             `json:"name"`
	Score    float64            `json:"score"`
	Passed   bool               `json:"passed"`
	HardGate bool               `json:"hard_gate"`
	Metrics  map[string]float64 `json:"metrics"`
	Failures []string           `json:"failures,omitempty"`
}

// SemanticEvaluation contains the auditable output of the LLM verifier/judge.
// Numeric scores use a 0-5 rubric and Score is normalized to 0-1.
type SemanticEvaluation struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	Score              float64  `json:"score"`
	Passed             bool     `json:"passed"`
	Groundedness       float64  `json:"groundedness"`
	NumericAccuracy    float64  `json:"numeric_accuracy"`
	ActionSupport      float64  `json:"action_support"`
	Completeness       float64  `json:"completeness"`
	ApprovalDisclosure float64  `json:"approval_disclosure"`
	UnsupportedClaims  []string `json:"unsupported_claims,omitempty"`
	NumericErrors      []string `json:"numeric_errors,omitempty"`
	UnsupportedActions []string `json:"unsupported_actions,omitempty"`
	Rationale          string   `json:"rationale,omitempty"`
	DurationMS         int64    `json:"duration_ms"`
}

type HarnessCaseReport struct {
	CaseID                string               `json:"case_id"`
	RunID                 string               `json:"run_id"`
	Score                 float64              `json:"score"`
	Passed                bool                 `json:"passed"`
	TrajectoryFingerprint string               `json:"trajectory_fingerprint"`
	ReplayFingerprint     string               `json:"replay_fingerprint"`
	Reproducible          bool                 `json:"reproducible"`
	Layers                []HarnessLayerReport `json:"layers"`
	SemanticEvaluation    *SemanticEvaluation  `json:"semantic_evaluation,omitempty"`
	Run                   *Run                 `json:"run,omitempty"`
	Failures              []string             `json:"failures,omitempty"`
}

type FailureAttribution struct {
	Category          string   `json:"category"`
	Severity          string   `json:"severity"`
	CaseIDs           []string `json:"case_ids"`
	Evidence          []string `json:"evidence"`
	AllowedMutations  []string `json:"allowed_mutations"`
	SuggestedMutation string   `json:"suggested_mutation"`
}

type HarnessReport struct {
	ID                  string               `json:"id"`
	SuiteVersion        string               `json:"suite_version"`
	PolicyVersion       string               `json:"policy_version"`
	BaselineVersion     string               `json:"baseline_version,omitempty"`
	Score               float64              `json:"score"`
	BaselineScore       float64              `json:"baseline_score,omitempty"`
	Passed              bool                 `json:"passed"`
	Regression          bool                 `json:"regression"`
	GateDecision        string               `json:"gate_decision"`
	Layers              []HarnessLayerReport `json:"layers"`
	Cases               []HarnessCaseReport  `json:"cases"`
	FailureAttributions []FailureAttribution `json:"failure_attributions,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
}

type EvolutionRun struct {
	ID              string               `json:"id"`
	BaselineReport  HarnessReport        `json:"baseline_report"`
	Candidate       Policy               `json:"candidate"`
	CandidateReport HarnessReport        `json:"candidate_report"`
	Attributions    []FailureAttribution `json:"attributions,omitempty"`
	Comparison      EvolutionComparison  `json:"comparison"`
	CanaryPercent   int                  `json:"canary_percent,omitempty"`
	Status          string               `json:"status"`
	CreatedAt       time.Time            `json:"created_at"`
}

// EvolutionComparison is the compact, resume-ready result of one controlled
// evolution experiment. The full per-case reports remain available for audit.
type EvolutionComparison struct {
	CaseCount                   int     `json:"case_count"`
	BaselineScore               float64 `json:"baseline_score"`
	CandidateScore              float64 `json:"candidate_score"`
	ScoreDelta                  float64 `json:"score_delta"`
	BaselineCasePassRate        float64 `json:"baseline_case_pass_rate"`
	CandidateCasePassRate       float64 `json:"candidate_case_pass_rate"`
	BaselineModelQuality        float64 `json:"baseline_model_quality"`
	CandidateModelQuality       float64 `json:"candidate_model_quality"`
	ModelQualityDelta           float64 `json:"model_quality_delta"`
	BaselineGroundedness        float64 `json:"baseline_groundedness"`
	CandidateGroundedness       float64 `json:"candidate_groundedness"`
	BaselineNumericAccuracy     float64 `json:"baseline_numeric_accuracy"`
	CandidateNumericAccuracy    float64 `json:"candidate_numeric_accuracy"`
	BaselineAverageLatencyMS    float64 `json:"baseline_average_latency_ms"`
	CandidateAverageLatencyMS   float64 `json:"candidate_average_latency_ms"`
	BaselineAverageCostUnits    float64 `json:"baseline_average_cost_units"`
	CandidateAverageCostUnits   float64 `json:"candidate_average_cost_units"`
	BaselineSafetyViolations    float64 `json:"baseline_safety_violations"`
	CandidateSafetyViolations   float64 `json:"candidate_safety_violations"`
	PromptChanged               bool    `json:"prompt_changed"`
	CandidatePromptValidationOK bool    `json:"candidate_prompt_validation_ok"`
	GateDecision                string  `json:"gate_decision"`
}
