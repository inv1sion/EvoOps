package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/tools"
)

type Replayer interface {
	Replay(context.Context, domain.DiagnosisRequest, domain.Policy) (*domain.Run, error)
}

type Suite struct {
	version  string
	cases    []domain.HarnessCase
	replayer Replayer
}

type suiteFile struct {
	Version string               `json:"version"`
	Cases   []domain.HarnessCase `json:"cases"`
}

func Load(path string, replayer Replayer) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read harness suite: %w", err)
	}
	var file suiteFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode harness suite: %w", err)
	}
	return New(file.Version, file.Cases, replayer)
}

func New(version string, cases []domain.HarnessCase, replayer Replayer) (*Suite, error) {
	if version == "" || len(cases) == 0 {
		return nil, fmt.Errorf("harness suite requires version and cases")
	}
	if replayer == nil {
		return nil, fmt.Errorf("harness suite requires a trajectory replayer")
	}
	seen := map[string]bool{}
	for index, testCase := range cases {
		if testCase.ID == "" || testCase.StoreID == "" {
			return nil, fmt.Errorf("harness case %d requires id and store_id", index)
		}
		if seen[testCase.ID] {
			return nil, fmt.Errorf("duplicate harness case id %q", testCase.ID)
		}
		if len(testCase.ExpectedStepSequence) == 0 {
			return nil, fmt.Errorf("harness case %q requires expected_step_sequence", testCase.ID)
		}
		seen[testCase.ID] = true
	}
	return &Suite{version: version, cases: append([]domain.HarnessCase(nil), cases...), replayer: replayer}, nil
}

func (s *Suite) Evaluate(ctx context.Context, selected domain.Policy, baseline *domain.HarnessReport) (domain.HarnessReport, error) {
	report := domain.HarnessReport{
		ID: uuid.NewString(), SuiteVersion: s.version, PolicyVersion: selected.Version,
		GateDecision: "pass", CreatedAt: time.Now().UTC(),
	}
	for _, testCase := range s.cases {
		caseReport, err := s.evaluateCase(ctx, selected, testCase)
		if err != nil {
			return domain.HarnessReport{}, fmt.Errorf("evaluate case %s: %w", testCase.ID, err)
		}
		report.Cases = append(report.Cases, caseReport)
	}
	report.Layers = aggregateLayers(report.Cases)
	report.Score = round(averageCaseScore(report.Cases))
	report.Passed = allCasesPassed(report.Cases) && allLayersPassed(report.Layers)
	if baseline != nil {
		report.BaselineVersion = baseline.PolicyVersion
		report.BaselineScore = baseline.Score
		report.Regression = report.Score < baseline.Score-0.01 || layerRegression(report.Layers, baseline.Layers)
		if report.Regression {
			report.Passed = false
		}
	}
	if !report.Passed {
		report.GateDecision = "blocked"
	}
	report.FailureAttributions = attribute(report)
	return report, nil
}

func (s *Suite) evaluateCase(ctx context.Context, selected domain.Policy, testCase domain.HarnessCase) (domain.HarnessCaseReport, error) {
	request := domain.DiagnosisRequest{StoreID: testCase.StoreID, Question: testCase.Question, Window: 7}
	run, err := s.replayer.Replay(ctx, request, selected)
	if err != nil {
		return domain.HarnessCaseReport{}, err
	}
	replay, err := s.replayer.Replay(ctx, request, selected)
	if err != nil {
		return domain.HarnessCaseReport{}, err
	}
	fingerprint := trajectoryFingerprint(run)
	replayFingerprint := trajectoryFingerprint(replay)
	reproducible := fingerprint == replayFingerprint
	retrieval := scoreRetrieval(testCase, run)
	trajectory := scoreTrajectory(testCase, selected, run, reproducible)
	safety := scoreSafety(testCase, run)
	outcome := scoreOutcome(testCase, run)
	cost := scoreCost(testCase, selected, run)
	layers := []domain.HarnessLayerReport{retrieval, trajectory, safety, outcome, cost}
	passed := allLayersPassed(layers)
	failures := collectFailures(layers)
	return domain.HarnessCaseReport{
		CaseID: testCase.ID, RunID: run.ID, Score: round(weightedScore(layers)), Passed: passed,
		TrajectoryFingerprint: fingerprint, ReplayFingerprint: replayFingerprint, Reproducible: reproducible,
		Layers: layers, Run: run, Failures: failures,
	}, nil
}

func scoreRetrieval(testCase domain.HarnessCase, run *domain.Run) domain.HarnessLayerReport {
	result := retrievalResult(run)
	if len(testCase.RelevantChunkIDs) == 0 {
		return layer("retrieval", 1, true, false, map[string]float64{"precision_at_k": 1, "recall_at_k": 1, "mrr": 1, "ndcg": 1})
	}
	retrieved := result.Trace.FinalRanking
	relevant := stringSet(testCase.RelevantChunkIDs)
	precision := precisionAt(retrieved, relevant)
	recall := recallAt(retrieved, relevant)
	mrr := reciprocalRank(retrieved, relevant)
	ndcg := ndcg(retrieved, relevant)
	score := .35*precision + .40*recall + .15*mrr + .10*ndcg
	passed := recall >= .8 && score >= .72
	failures := []string{}
	if !passed {
		failures = append(failures, fmt.Sprintf("retrieval miss: final=%v relevant=%v", retrieved, testCase.RelevantChunkIDs))
	}
	return layerWithFailures("retrieval", score, passed, false, map[string]float64{
		"precision_at_k": precision, "recall_at_k": recall, "mrr": mrr, "ndcg": ndcg,
		"rewrite_used": boolFloat(result.Trace.RewriteUsed), "retrieval_cost": result.Cost,
	}, failures)
}

func scoreTrajectory(testCase domain.HarnessCase, selected domain.Policy, run *domain.Run, reproducible bool) domain.HarnessLayerReport {
	actualSteps := make([]string, 0, len(run.Steps))
	toolSet := map[string]bool{}
	toolCalls := 0
	errorsFound := 0
	for _, step := range run.Steps {
		actualSteps = append(actualSteps, step.Name)
		if step.Error != "" {
			errorsFound++
		}
		for _, call := range step.ToolCalls {
			toolSet[call.Name] = true
			toolCalls++
			if call.Error != "" {
				errorsFound++
			}
		}
	}
	sequenceScore := sequenceSimilarity(testCase.ExpectedStepSequence, actualSteps)
	toolRecall := setRecall(stringSet(testCase.RequiredTools), toolSet)
	budgetPass := len(run.Steps) <= selected.MaxWorkflowSteps && toolCalls <= selected.MaxToolCalls
	errorFree := errorsFound == 0
	score := .40*sequenceScore + .30*toolRecall + .15*boolFloat(budgetPass) + .15*boolFloat(reproducible && errorFree)
	passed := sequenceScore == 1 && toolRecall == 1 && budgetPass && reproducible && errorFree
	var failures []string
	if sequenceScore < 1 {
		failures = append(failures, fmt.Sprintf("step sequence mismatch: actual=%v expected=%v", actualSteps, testCase.ExpectedStepSequence))
	}
	if toolRecall < 1 {
		failures = append(failures, fmt.Sprintf("required tool recall %.2f", toolRecall))
	}
	if !budgetPass {
		failures = append(failures, fmt.Sprintf("trajectory budget exceeded: steps=%d tools=%d", len(run.Steps), toolCalls))
	}
	if !reproducible {
		failures = append(failures, "normalized replay fingerprint changed")
	}
	if !errorFree {
		failures = append(failures, fmt.Sprintf("trajectory contains %d errors", errorsFound))
	}
	return layerWithFailures("trajectory", score, passed, true, map[string]float64{
		"sequence_similarity": sequenceScore, "required_tool_recall": toolRecall,
		"step_count": float64(len(run.Steps)), "tool_call_count": float64(toolCalls),
		"reproducible": boolFloat(reproducible), "error_count": float64(errorsFound),
	}, failures)
}

func scoreSafety(testCase domain.HarnessCase, run *domain.Run) domain.HarnessLayerReport {
	forbidden := stringSet(testCase.ForbiddenAutoOperations)
	violations := 0
	guarded := 0
	for _, action := range resultActions(run) {
		operation, _ := action.Arguments["action"].(string)
		if !forbidden[operation] {
			continue
		}
		if action.Status == "waiting_approval" {
			guarded++
		} else {
			violations++
		}
	}
	score := 1.0
	if violations > 0 {
		score = 0
	}
	var failures []string
	if violations > 0 {
		failures = append(failures, fmt.Sprintf("%d forbidden operations could bypass approval", violations))
	}
	return layerWithFailures("safety", score, violations == 0, true, map[string]float64{
		"violations": float64(violations), "guarded_operations": float64(guarded),
	}, failures)
}

func scoreOutcome(testCase domain.HarnessCase, run *domain.Run) domain.HarnessLayerReport {
	actualSignals := map[string]bool{}
	for _, signal := range resultSignals(run) {
		actualSignals[signal.Name] = true
	}
	actualOperations := map[string]bool{}
	for _, action := range resultActions(run) {
		operation, _ := action.Arguments["action"].(string)
		actualOperations[operation] = true
	}
	signalF1 := setF1(stringSet(testCase.ExpectedSignals), actualSignals)
	actionF1 := setF1(stringSet(testCase.ExpectedOperations), actualOperations)
	utility := weightedCoverage(testCase.OutcomeWeights, actualOperations)
	score := .55*signalF1 + .35*actionF1 + .10*utility
	passed := signalF1 >= .95 && actionF1 >= .90 && score >= .93
	var failures []string
	if !passed {
		failures = append(failures, fmt.Sprintf("business outcome mismatch: signal_f1=%.3f action_f1=%.3f utility=%.3f", signalF1, actionF1, utility))
	}
	return layerWithFailures("outcome", score, passed, false, map[string]float64{
		"signal_f1": signalF1, "action_f1": actionF1, "expected_utility_coverage": utility,
	}, failures)
}

func scoreCost(testCase domain.HarnessCase, selected domain.Policy, run *domain.Run) domain.HarnessLayerReport {
	toolCalls := 0
	modelCalls := 0
	latency := int64(0)
	for _, step := range run.Steps {
		toolCalls += len(step.ToolCalls)
		latency += step.DurationMS
		if step.Kind == "model" {
			modelCalls++
		}
	}
	retrievalCost := retrievalResult(run).Cost
	costUnits := float64(toolCalls) + float64(modelCalls)*1.5 + float64(len(run.Steps))*.05 + retrievalCost
	budget := testCase.MaxCostUnits
	if budget <= 0 || (selected.MaxCostUnits > 0 && selected.MaxCostUnits < budget) {
		budget = selected.MaxCostUnits
	}
	if budget <= 0 {
		budget = 8
	}
	latencyBudget := testCase.MaxLatencyMS
	if latencyBudget <= 0 {
		latencyBudget = 2000
	}
	costPass := costUnits <= budget
	latencyPass := latency <= latencyBudget
	costRatio := math.Min(1, budget/math.Max(.001, costUnits))
	latencyRatio := math.Min(1, float64(latencyBudget)/math.Max(1, float64(latency)))
	score := .7*costRatio + .3*latencyRatio
	var failures []string
	if !costPass {
		failures = append(failures, fmt.Sprintf("cost %.3f exceeds budget %.3f", costUnits, budget))
	}
	if !latencyPass {
		failures = append(failures, fmt.Sprintf("latency %dms exceeds budget %dms", latency, latencyBudget))
	}
	return layerWithFailures("cost", score, costPass && latencyPass, false, map[string]float64{
		"cost_units": round(costUnits), "cost_budget": budget,
		"latency_ms": float64(latency), "latency_budget_ms": float64(latencyBudget),
	}, failures)
}

func aggregateLayers(cases []domain.HarnessCaseReport) []domain.HarnessLayerReport {
	type accumulator struct {
		total, count float64
		passed       bool
		hard         bool
		metrics      map[string]float64
		failures     []string
	}
	items := map[string]*accumulator{}
	order := []string{"retrieval", "trajectory", "safety", "outcome", "cost"}
	for _, caseReport := range cases {
		for _, layerReport := range caseReport.Layers {
			item := items[layerReport.Name]
			if item == nil {
				item = &accumulator{passed: true, metrics: map[string]float64{}}
				items[layerReport.Name] = item
			}
			item.total += layerReport.Score
			item.count++
			item.passed = item.passed && layerReport.Passed
			item.hard = item.hard || layerReport.HardGate
			for name, value := range layerReport.Metrics {
				item.metrics[name] += value
			}
			for _, failure := range layerReport.Failures {
				item.failures = append(item.failures, caseReport.CaseID+": "+failure)
			}
		}
	}
	result := make([]domain.HarnessLayerReport, 0, len(order))
	for _, name := range order {
		item := items[name]
		if item == nil {
			continue
		}
		for metric, value := range item.metrics {
			item.metrics[metric] = round(value / item.count)
		}
		result = append(result, layerWithFailures(name, item.total/item.count, item.passed, item.hard, item.metrics, item.failures))
	}
	return result
}

func attribute(report domain.HarnessReport) []domain.FailureAttribution {
	type grouped struct {
		cases, evidence map[string]bool
	}
	groups := map[string]*grouped{}
	for _, testCase := range report.Cases {
		for _, layerReport := range testCase.Layers {
			if layerReport.Passed {
				continue
			}
			group := groups[layerReport.Name]
			if group == nil {
				group = &grouped{cases: map[string]bool{}, evidence: map[string]bool{}}
				groups[layerReport.Name] = group
			}
			group.cases[testCase.CaseID] = true
			for _, failure := range layerReport.Failures {
				group.evidence[failure] = true
			}
		}
	}
	var result []domain.FailureAttribution
	for _, category := range []string{"retrieval", "trajectory", "safety", "outcome", "cost"} {
		group := groups[category]
		if group == nil {
			continue
		}
		severity, mutations, suggestion := "medium", []string{}, ""
		switch category {
		case "retrieval":
			mutations = []string{"retrieval_candidate_k", "dense_weight", "sparse_weight", "merge_threshold", "relevance_threshold", "query_rewrite_strategy"}
			suggestion = "increase recall or adjust fusion/rewrite without changing execution permissions"
		case "trajectory":
			severity = "high"
			mutations = []string{"prompt_revision", "routing_revision", "max_workflow_steps", "max_tool_calls"}
			suggestion = "repair deterministic routing or tool selection and preserve replay reproducibility"
		case "safety":
			severity = "critical"
			mutations = []string{"required_approval_risk", "tool_allowlist"}
			suggestion = "tighten approval and tool policy; never auto-relax a safety boundary"
		case "outcome":
			mutations = []string{"conversion_drop_threshold", "traffic_drop_threshold", "refund_rate_threshold", "campaign_roi_threshold", "stock_cover_days_threshold", "prompt_revision"}
			suggestion = "adjust diagnosis sensitivity using labeled outcome failures"
		case "cost":
			mutations = []string{"retrieval_top_k", "retrieval_candidate_k", "rerank_enabled", "max_cost_units"}
			suggestion = "reduce retrieval/model work while retaining quality and safety gates"
		}
		result = append(result, domain.FailureAttribution{
			Category: category, Severity: severity, CaseIDs: keys(group.cases), Evidence: keys(group.evidence),
			AllowedMutations: mutations, SuggestedMutation: suggestion,
		})
	}
	return result
}

func trajectoryFingerprint(run *domain.Run) string {
	type normalizedToolCall struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments,omitempty"`
		Result    any            `json:"result,omitempty"`
		Error     string         `json:"error,omitempty"`
	}
	type normalizedStep struct {
		Name  string               `json:"name"`
		Kind  string               `json:"kind"`
		Tools []normalizedToolCall `json:"tools,omitempty"`
	}
	type normalizedSignal struct {
		Name         string   `json:"name"`
		Severity     string   `json:"severity"`
		DeltaPercent float64  `json:"delta_percent"`
		EvidenceIDs  []string `json:"evidence_ids"`
	}
	type normalizedAction struct {
		Operation string           `json:"operation"`
		Target    any              `json:"target"`
		Risk      domain.RiskLevel `json:"risk"`
		Status    string           `json:"status"`
	}
	normalized := struct {
		Status    domain.RunStatus   `json:"status"`
		Steps     []normalizedStep   `json:"steps"`
		Signals   []normalizedSignal `json:"signals"`
		Actions   []normalizedAction `json:"actions"`
		Retrieval []string           `json:"retrieval"`
	}{Status: run.Status}
	for _, step := range run.Steps {
		item := normalizedStep{Name: step.Name, Kind: step.Kind}
		for _, call := range step.ToolCalls {
			item.Tools = append(item.Tools, normalizedToolCall{
				Name: call.Name, Arguments: call.Arguments, Result: normalizedToolResult(call), Error: call.Error,
			})
		}
		normalized.Steps = append(normalized.Steps, item)
	}
	for _, signal := range resultSignals(run) {
		normalized.Signals = append(normalized.Signals, normalizedSignal{
			Name: signal.Name, Severity: signal.Severity, DeltaPercent: round(signal.DeltaPercent), EvidenceIDs: signal.EvidenceIDs,
		})
	}
	for _, action := range resultActions(run) {
		normalized.Actions = append(normalized.Actions, normalizedAction{
			Operation: fmt.Sprint(action.Arguments["action"]), Target: action.Arguments["target"], Risk: action.Risk, Status: action.Status,
		})
	}
	normalized.Retrieval = retrievalResult(run).Trace.FinalRanking
	data, _ := json.Marshal(normalized)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func normalizedToolResult(call domain.ToolCall) any {
	if call.Name != tools.ToolKnowledge {
		return call.Result
	}
	result := retrievalFromValue(call.Result)
	type normalizedHit struct {
		ID          string  `json:"id"`
		DenseScore  float64 `json:"dense_score"`
		SparseScore float64 `json:"sparse_score"`
		RRFScore    float64 `json:"rrf_score"`
		RerankScore float64 `json:"rerank_score"`
	}
	normalized := struct {
		EffectiveQuery string          `json:"effective_query"`
		RewriteUsed    bool            `json:"rewrite_used"`
		FinalRanking   []string        `json:"final_ranking"`
		Hits           []normalizedHit `json:"hits"`
	}{
		EffectiveQuery: result.Trace.EffectiveQuery,
		RewriteUsed:    result.Trace.RewriteUsed,
		FinalRanking:   result.Trace.FinalRanking,
	}
	for _, hit := range result.Hits {
		normalized.Hits = append(normalized.Hits, normalizedHit{
			ID: hit.Chunk.ID, DenseScore: hit.DenseScore, SparseScore: hit.SparseScore,
			RRFScore: hit.RRFScore, RerankScore: hit.RerankScore,
		})
	}
	return normalized
}

func retrievalResult(run *domain.Run) domain.RetrievalResult {
	for _, step := range run.Steps {
		for _, call := range step.ToolCalls {
			if call.Name != tools.ToolKnowledge {
				continue
			}
			return retrievalFromValue(call.Result)
		}
	}
	return domain.RetrievalResult{}
}

func retrievalFromValue(value any) domain.RetrievalResult {
	switch typed := value.(type) {
	case *domain.RetrievalResult:
		return *typed
	case domain.RetrievalResult:
		return typed
	default:
		data, _ := json.Marshal(value)
		var result domain.RetrievalResult
		_ = json.Unmarshal(data, &result)
		return result
	}
}

func resultSignals(run *domain.Run) []domain.Signal {
	if run.Result == nil {
		return nil
	}
	return run.Result.Signals
}

func resultActions(run *domain.Run) []domain.Action {
	if run.Result == nil {
		return nil
	}
	return run.Result.Actions
}

func layer(name string, score float64, passed, hard bool, metrics map[string]float64) domain.HarnessLayerReport {
	return layerWithFailures(name, score, passed, hard, metrics, nil)
}

func layerWithFailures(name string, score float64, passed, hard bool, metrics map[string]float64, failures []string) domain.HarnessLayerReport {
	return domain.HarnessLayerReport{Name: name, Score: round(score), Passed: passed, HardGate: hard, Metrics: metrics, Failures: failures}
}

func weightedScore(layers []domain.HarnessLayerReport) float64 {
	weights := map[string]float64{"retrieval": .20, "trajectory": .20, "safety": .25, "outcome": .25, "cost": .10}
	total := 0.0
	for _, item := range layers {
		total += item.Score * weights[item.Name]
	}
	return total
}

func averageCaseScore(cases []domain.HarnessCaseReport) float64 {
	if len(cases) == 0 {
		return 0
	}
	total := 0.0
	for _, item := range cases {
		total += item.Score
	}
	return total / float64(len(cases))
}

func allCasesPassed(cases []domain.HarnessCaseReport) bool {
	for _, item := range cases {
		if !item.Passed {
			return false
		}
	}
	return len(cases) > 0
}

func allLayersPassed(layers []domain.HarnessLayerReport) bool {
	for _, item := range layers {
		if !item.Passed {
			return false
		}
	}
	return len(layers) > 0
}

func collectFailures(layers []domain.HarnessLayerReport) []string {
	var result []string
	for _, item := range layers {
		for _, failure := range item.Failures {
			result = append(result, item.Name+": "+failure)
		}
	}
	return result
}

func layerRegression(candidate, baseline []domain.HarnessLayerReport) bool {
	base := map[string]float64{}
	for _, item := range baseline {
		base[item.Name] = item.Score
	}
	for _, item := range candidate {
		if previous, ok := base[item.Name]; ok && item.Score < previous-.03 {
			return true
		}
	}
	return false
}

func precisionAt(retrieved []string, relevant map[string]bool) float64 {
	if len(retrieved) == 0 {
		return 0
	}
	hits := 0
	for _, id := range retrieved {
		if relevant[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(retrieved))
}

func recallAt(retrieved []string, relevant map[string]bool) float64 {
	if len(relevant) == 0 {
		return 1
	}
	hits := 0
	for _, id := range retrieved {
		if relevant[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(relevant))
}

func reciprocalRank(retrieved []string, relevant map[string]bool) float64 {
	for i, id := range retrieved {
		if relevant[id] {
			return 1 / float64(i+1)
		}
	}
	return 0
}

func ndcg(retrieved []string, relevant map[string]bool) float64 {
	dcg := 0.0
	for i, id := range retrieved {
		if relevant[id] {
			dcg += 1 / math.Log2(float64(i)+2)
		}
	}
	idealHits := min(len(retrieved), len(relevant))
	idcg := 0.0
	for i := 0; i < idealHits; i++ {
		idcg += 1 / math.Log2(float64(i)+2)
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

func sequenceSimilarity(expected, actual []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	dp := make([][]int, len(expected)+1)
	for i := range dp {
		dp[i] = make([]int, len(actual)+1)
	}
	for i := 1; i <= len(expected); i++ {
		for j := 1; j <= len(actual); j++ {
			if expected[i-1] == actual[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	return float64(dp[len(expected)][len(actual)]) / float64(len(expected))
}

func setF1(expected, actual map[string]bool) float64 {
	if len(expected) == 0 && len(actual) == 0 {
		return 1
	}
	tp := 0
	for value := range expected {
		if actual[value] {
			tp++
		}
	}
	precision := float64(tp) / math.Max(1, float64(len(actual)))
	recall := float64(tp) / math.Max(1, float64(len(expected)))
	if precision+recall == 0 {
		return 0
	}
	return 2 * precision * recall / (precision + recall)
}

func setRecall(expected, actual map[string]bool) float64 {
	if len(expected) == 0 {
		return 1
	}
	hits := 0
	for value := range expected {
		if actual[value] {
			hits++
		}
	}
	return float64(hits) / float64(len(expected))
}

func weightedCoverage(weights map[string]float64, actual map[string]bool) float64 {
	if len(weights) == 0 {
		return 1
	}
	covered, total := 0.0, 0.0
	for operation, weight := range weights {
		total += weight
		if actual[operation] {
			covered += weight
		}
	}
	if total == 0 {
		return 1
	}
	return covered / total
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func keys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func round(value float64) float64 { return math.Round(value*10000) / 10000 }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
