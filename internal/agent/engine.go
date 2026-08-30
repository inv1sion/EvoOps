package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
	"github.com/inv1sion/evoops/internal/tools"
)

type execution struct {
	Request  domain.DiagnosisRequest
	Run      *domain.Run
	Policy   domain.Policy
	Context  AnalysisContext
	Analysis Analysis
	DryRun   bool
}

type Engine struct {
	repo        repository.Repository
	policies    *policy.Manager
	tools       *tools.Registry
	synthesizer Synthesizer
	runner      compose.Runnable[*execution, *execution]
	mu          sync.Mutex
}

func NewEngine(ctx context.Context, repo repository.Repository, policies *policy.Manager, registry *tools.Registry, synthesizer Synthesizer) (*Engine, error) {
	if synthesizer == nil {
		synthesizer = LocalSynthesizer{}
	}
	engine := &Engine{repo: repo, policies: policies, tools: registry, synthesizer: synthesizer}
	wf := compose.NewWorkflow[*execution, *execution]()
	wf.AddLambdaNode("gather_operational_data", compose.InvokableLambda(engine.gather)).AddInput(compose.START)
	wf.AddLambdaNode("diagnose_signals", compose.InvokableLambda(engine.diagnose)).AddInput("gather_operational_data")
	wf.AddLambdaNode("retrieve_playbooks", compose.InvokableLambda(engine.retrieve)).AddInput("diagnose_signals")
	wf.AddLambdaNode("synthesize_plan", compose.InvokableLambda(engine.synthesize)).AddInput("retrieve_playbooks")
	wf.AddLambdaNode("approval_and_execution_gate", compose.InvokableLambda(engine.guard)).AddInput("synthesize_plan")
	wf.End().AddInput("approval_and_execution_gate")
	runner, err := wf.Compile(ctx, compose.WithGraphName("EvoOpsDiagnosisWorkflow"))
	if err != nil {
		return nil, fmt.Errorf("compile Eino workflow: %w", err)
	}
	engine.runner = runner
	return engine, nil
}

func (e *Engine) Run(ctx context.Context, request domain.DiagnosisRequest) (*domain.Run, error) {
	selected, err := e.policies.Select(ctx, request.StoreID)
	if err != nil {
		return nil, fmt.Errorf("select policy: %w", err)
	}
	return e.run(ctx, request, selected, "live", true, false)
}

// Replay runs the exact production workflow with a caller-supplied policy,
// disables side effects, and keeps the complete trajectory in memory for the
// Harness. It is the reproducible execution primitive used by release gates.
func (e *Engine) Replay(ctx context.Context, request domain.DiagnosisRequest, selected domain.Policy) (*domain.Run, error) {
	return e.run(ctx, request, selected, "replay", false, true)
}

func (e *Engine) run(ctx context.Context, request domain.DiagnosisRequest, selected domain.Policy, mode string, persist, dryRun bool) (*domain.Run, error) {
	if strings.TrimSpace(request.StoreID) == "" {
		return nil, fmt.Errorf("store_id is required")
	}
	if request.Window <= 0 {
		request.Window = 7
	}
	if strings.TrimSpace(request.Question) == "" {
		request.Question = "经营发生了什么变化、原因是什么、下一步应该怎么做？"
	}
	run := &domain.Run{
		ID: uuid.NewString(), Mode: mode, Request: request, Status: domain.RunRunning,
		PolicyVersion: selected.Version, StartedAt: time.Now().UTC(), Steps: []domain.Step{},
	}
	if persist {
		if err := e.repo.SaveRun(ctx, run); err != nil {
			return nil, err
		}
	}
	state := &execution{Request: request, Run: run, Policy: selected, Context: AnalysisContext{StoreID: request.StoreID}, DryRun: dryRun}
	result, err := e.runner.Invoke(ctx, state)
	if err != nil {
		run.Status = domain.RunFailed
		run.Error = err.Error()
		finished := time.Now().UTC()
		run.FinishedAt = &finished
		if persist {
			_ = e.repo.SaveRun(ctx, run)
		}
		return run, err
	}
	if result.Run.Status == domain.RunCompleted {
		finished := time.Now().UTC()
		result.Run.FinishedAt = &finished
	}
	if persist {
		if err := e.repo.SaveRun(ctx, result.Run); err != nil {
			return nil, err
		}
	}
	return result.Run, nil
}

func (e *Engine) Approve(ctx context.Context, runID string, decision domain.ApprovalDecision) (*domain.Run, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	run, err := e.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != domain.RunWaitingApproval || run.PendingApproval == nil || run.Result == nil {
		return nil, fmt.Errorf("运行 %s 当前不处于待审批状态", runID)
	}
	step := e.beginStep(run, "resume_approved_operations", "approval", map[string]any{"decision": decision})
	pending := make(map[string]bool, len(run.PendingApproval.ActionIDs))
	for _, id := range run.PendingApproval.ActionIDs {
		pending[id] = true
	}
	for i := range run.Result.Actions {
		action := &run.Result.Actions[i]
		if !pending[action.ID] {
			continue
		}
		if !decision.Approved {
			action.Status = "rejected"
			continue
		}
		var receipt any
		call := e.callTool(ctx, tools.ToolExecute, action.Arguments, &receipt)
		step.ToolCalls = append(step.ToolCalls, call)
		if call.Error != "" {
			action.Status = "failed"
			action.Receipt = call.Error
			continue
		}
		action.Status = "executed"
		receiptJSON, _ := json.Marshal(receipt)
		action.Receipt = string(receiptJSON)
	}
	if decision.Approved {
		run.Status = domain.RunCompleted
	} else {
		run.Status = domain.RunRejected
	}
	run.Result.Status = run.Status
	run.PendingApproval = nil
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	e.finishStep(run, step, map[string]any{"status": run.Status}, nil)
	if err := e.repo.SaveRun(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

func (e *Engine) gather(ctx context.Context, state *execution) (*execution, error) {
	step := e.beginStep(state.Run, "gather_operational_data", "workflow", map[string]any{"store_id": state.Request.StoreID, "window_days": state.Request.Window})
	var firstErr error
	defer func() {
		e.finishStep(state.Run, step, map[string]any{"evidence_sources": len(step.ToolCalls)}, firstErr)
	}()

	args := map[string]any{"store_id": state.Request.StoreID}
	metricsCall := e.callTool(ctx, tools.ToolMetrics, args, &state.Context.Metrics)
	step.ToolCalls = append(step.ToolCalls, metricsCall)
	if metricsCall.Error != "" {
		firstErr = errors.New(metricsCall.Error)
		return state, firstErr
	}
	inventoryCall := e.callTool(ctx, tools.ToolInventory, args, &state.Context.Inventory)
	step.ToolCalls = append(step.ToolCalls, inventoryCall)
	if inventoryCall.Error != "" {
		firstErr = errors.New(inventoryCall.Error)
		return state, firstErr
	}
	campaignCall := e.callTool(ctx, tools.ToolCampaigns, args, &state.Context.Campaigns)
	step.ToolCalls = append(step.ToolCalls, campaignCall)
	if campaignCall.Error != "" {
		firstErr = errors.New(campaignCall.Error)
		return state, firstErr
	}
	return state, nil
}

func (e *Engine) diagnose(_ context.Context, state *execution) (*execution, error) {
	step := e.beginStep(state.Run, "diagnose_signals", "reasoning", map[string]any{"policy_version": state.Policy.Version})
	state.Analysis = Analyze(state.Context, state.Policy)
	e.finishStep(state.Run, step, map[string]any{"signals": state.Analysis.Signals, "proposed_actions": len(state.Analysis.Actions)}, nil)
	return state, nil
}

func (e *Engine) retrieve(ctx context.Context, state *execution) (*execution, error) {
	query := signalQuery(state.Analysis.Signals)
	step := e.beginStep(state.Run, "retrieve_playbooks", "rag", map[string]any{"query": query, "top_k": state.Policy.RetrievalTopK, "candidate_k": state.Policy.RetrievalCandidateK})
	args := map[string]any{
		"store_id": state.Request.StoreID, "query": query, "top_k": state.Policy.RetrievalTopK,
		"candidate_k": state.Policy.RetrievalCandidateK, "dense_weight": state.Policy.DenseWeight,
		"sparse_weight": state.Policy.SparseWeight, "rrf_k": state.Policy.RRFK,
		"merge_threshold": state.Policy.MergeThreshold, "relevance_threshold": state.Policy.RelevanceThreshold,
		"rerank_enabled": state.Policy.RerankEnabled, "query_rewrite_strategy": state.Policy.QueryRewriteStrategy,
	}
	call := e.callTool(ctx, tools.ToolKnowledge, args, &state.Context.Knowledge)
	step.ToolCalls = append(step.ToolCalls, call)
	if call.Error != "" {
		err := errors.New(call.Error)
		e.finishStep(state.Run, step, nil, err)
		return state, err
	}
	AddKnowledgeEvidence(&state.Analysis, state.Context.Knowledge)
	e.finishStep(state.Run, step, map[string]any{
		"documents": len(state.Context.Knowledge.Hits), "retrieval_trace": state.Context.Knowledge.Trace,
		"cost_units": state.Context.Knowledge.Cost,
	}, nil)
	return state, nil
}

func (e *Engine) synthesize(ctx context.Context, state *execution) (*execution, error) {
	input := map[string]any{"provider": e.synthesizer.Name(), "prompt_revision": state.Policy.PromptRevision}
	if described, ok := e.synthesizer.(interface{ ModelName() string }); ok {
		input["model"] = described.ModelName()
	}
	step := e.beginStep(state.Run, "synthesize_plan", "model", input)
	summary, err := e.synthesizer.Synthesize(ctx, state.Request, state.Analysis)
	if strings.TrimSpace(summary) != "" {
		state.Analysis.Summary = summary
	}
	// The real-model adapter deliberately falls back to deterministic output.
	// Keep the run usable, but retain the provider error in the trajectory.
	e.finishStep(state.Run, step, map[string]any{"summary": state.Analysis.Summary}, err)
	return state, nil
}

func (e *Engine) guard(ctx context.Context, state *execution) (*execution, error) {
	step := e.beginStep(state.Run, "approval_and_execution_gate", "guard", map[string]any{"minimum_risk": state.Policy.RequiredApprovalRisk})
	var pending []string
	for i := range state.Analysis.Actions {
		action := &state.Analysis.Actions[i]
		if RequiresApproval(action.Risk, state.Policy.RequiredApprovalRisk) {
			action.Status = "waiting_approval"
			pending = append(pending, action.ID)
			continue
		}
		if state.DryRun {
			action.Status = "would_execute"
			continue
		}
		var receipt any
		call := e.callTool(ctx, tools.ToolExecute, action.Arguments, &receipt)
		step.ToolCalls = append(step.ToolCalls, call)
		if call.Error != "" {
			action.Status = "failed"
			action.Receipt = call.Error
			continue
		}
		action.Status = "executed"
		receiptJSON, _ := json.Marshal(receipt)
		action.Receipt = string(receiptJSON)
	}
	status := domain.RunCompleted
	if len(pending) > 0 {
		status = domain.RunWaitingApproval
		state.Run.PendingApproval = &domain.ApprovalRequest{
			RunID: state.Run.ID, Reason: "一个或多个操作达到当前策略规定的人工审批风险阈值。", ActionIDs: pending,
		}
	}
	state.Run.Status = status
	state.Run.Result = &domain.DiagnosisResult{
		RunID: state.Run.ID, StoreID: state.Request.StoreID, Summary: state.Analysis.Summary,
		Signals: state.Analysis.Signals, Evidence: state.Analysis.Evidence, Actions: state.Analysis.Actions,
		PolicyVersion: state.Policy.Version, Status: status,
	}
	e.finishStep(state.Run, step, map[string]any{"status": status, "pending_action_ids": pending}, nil)
	return state, nil
}

func (e *Engine) beginStep(run *domain.Run, name, kind string, input map[string]any) *domain.Step {
	run.Steps = append(run.Steps, domain.Step{
		ID: uuid.NewString(), Name: name, Kind: kind, StartedAt: time.Now().UTC(), Input: input,
	})
	return &run.Steps[len(run.Steps)-1]
}

func (e *Engine) finishStep(run *domain.Run, step *domain.Step, output any, err error) {
	step.DurationMS = time.Since(step.StartedAt).Milliseconds()
	step.Output = output
	if err != nil {
		step.Error = err.Error()
	}
	if run.Mode != "replay" {
		_ = e.repo.SaveRun(context.Background(), run)
	}
}

func (e *Engine) callTool(ctx context.Context, name string, args map[string]any, target any) domain.ToolCall {
	started := time.Now()
	raw, err := e.tools.Invoke(ctx, name, args, target)
	call := domain.ToolCall{Name: name, Arguments: args, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		call.Error = err.Error()
		return call
	}
	if target != nil {
		call.Result = target
	} else {
		call.Result = raw
	}
	return call
}
