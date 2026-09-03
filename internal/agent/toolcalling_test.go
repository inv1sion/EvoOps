package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/policy"
	"github.com/inv1sion/evoops/internal/repository"
	"github.com/inv1sion/evoops/internal/tools"
)

type scriptedToolModel struct {
	responses []*schema.Message
	inputs    [][]*schema.Message
	err       error
}

func (m *scriptedToolModel) Generate(ctx context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.inputs = append(m.inputs, append([]*schema.Message(nil), messages...))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.err != nil {
		return nil, m.err
	}
	index := len(m.inputs) - 1
	if index >= len(m.responses) {
		return nil, fmt.Errorf("script exhausted")
	}
	return m.responses[index], nil
}
func (*scriptedToolModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, fmt.Errorf("not used")
}
func toolResponse(id, name, args string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: args}}}}
}
func finalResponse(task string) *schema.Message {
	return schema.AssistantMessage(fmt.Sprintf(`{"task":%q,"answer":"仅依据演示数据与工具证据回答。"}`, task), nil)
}
func toolEngine(t *testing.T, m model.BaseChatModel) *Engine {
	t.Helper()
	ctx := context.Background()
	repo, err := repository.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pm, err := policy.NewManager(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	data, err := dataset.LoadFile("../../data/demo/store.json")
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	if err := tools.RegisterLocal(ctx, registry, data); err != nil {
		t.Fatal(err)
	}
	planner := &ToolCallingPlanner{model: m, modelName: "scripted-test", limits: defaultToolCallingLimits()}
	e, err := NewToolCallingEngine(ctx, repo, pm, registry, LocalSynthesizer{}, planner)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func collection(run *domain.Run) *domain.Step {
	for i := range run.Steps {
		if run.Steps[i].Name == "model_tool_collection" {
			return &run.Steps[i]
		}
	}
	return nil
}

func TestToolCallingDiagnosisUsesNativeRoundTripAndGuard(t *testing.T) {
	m := &scriptedToolModel{responses: []*schema.Message{
		toolResponse("read", tools.ToolCampaigns, `{}`),
		toolResponse("search", tools.ToolKnowledge, `{"query":"低 ROI 广告处置"}`), finalResponse("diagnosis"),
	}}
	e := toolEngine(t, m)
	run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store", Question: "诊断低 ROI 广告"})
	if err != nil {
		t.Fatal(err)
	}
	if run.ExecutionMode != "model_tool_calling" || run.Result.Task != "diagnosis" || run.Status != domain.RunWaitingApproval {
		t.Fatalf("unexpected result: %#v", run)
	}
	step := collection(run)
	if len(step.ModelTurns) != 3 || len(step.ToolCalls) != 2 {
		t.Fatalf("missing tool trace: %#v", step)
	}
	for _, input := range m.inputs[1:] {
		found := false
		for _, msg := range input {
			if msg.Role == schema.Tool && msg.ToolCallID == "read" && strings.Contains(msg.Content, "CMP-SEARCH-01") {
				found = true
			}
		}
		if !found {
			t.Fatal("model never received correlated campaign tool result")
		}
	}
	for _, call := range step.ToolCalls {
		if call.Origin != "model" || call.Arguments["store_id"] != "demo-store" || call.ID == "" {
			t.Fatalf("missing scope/provenance: %#v", call)
		}
	}
	for _, action := range run.Result.Actions {
		if action.Status != "waiting_approval" {
			t.Fatal("approval bypass")
		}
	}
	loaded, err := e.repo.GetRun(context.Background(), run.ID)
	if err != nil || len(collection(loaded).ModelTurns) != 3 {
		t.Fatal("model turns were not persisted")
	}
}

func TestToolCallingReadOnlyRoutesNeverCreateActions(t *testing.T) {
	for _, task := range []string{"data_query", "knowledge_qa", "clarify"} {
		t.Run(task, func(t *testing.T) {
			var messages []*schema.Message
			if task == "data_query" {
				messages = append(messages, toolResponse("a", tools.ToolCampaigns, `{}`))
			}
			if task == "knowledge_qa" {
				messages = append(messages, toolResponse("a", tools.ToolKnowledge, `{"query":"ROI 是什么"}`))
			}
			messages = append(messages, finalResponse(task))
			e := toolEngine(t, &scriptedToolModel{responses: messages})
			run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store", Question: "只回答问题"})
			if err != nil {
				t.Fatal(err)
			}
			if run.Result.Task != task || len(run.Result.Actions) != 0 || len(run.Result.Signals) != 0 || run.PendingApproval != nil || run.Status != domain.RunCompleted {
				t.Fatalf("read-only route generated actions: %#v", run.Result)
			}
			if task == "knowledge_qa" && collection(run).ToolCalls[0].Name != tools.ToolKnowledge {
				t.Fatal("knowledge QA read campaign data")
			}
		})
	}
}

func TestToolCallingCancellationStopsBeforeModelInvocation(t *testing.T) {
	m := &scriptedToolModel{}
	e := toolEngine(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.Run(ctx, domain.DiagnosisRequest{StoreID: "demo-store"})
	if err == nil || len(m.inputs) != 0 {
		t.Fatal("cancelled request reached model")
	}
}

func TestToolCallingEmptyKnowledgeCanFinishWithoutInventingEvidence(t *testing.T) {
	if _, err := validatePlannerFinal(`{"task":"diagnosis","answer":""}`, "diagnosis", true, true); err != nil {
		t.Fatal(err)
	}
	// A successful empty retrieval is distinct from a failed retrieval; the
	// answer's factual quality is evaluated separately, not fabricated by runtime.
	m := &scriptedToolModel{responses: []*schema.Message{toolResponse("a", tools.ToolKnowledge, `{"query":"不存在的知识"}`), finalResponse("knowledge_qa")}}
	e := toolEngine(t, m)
	registry := tools.NewRegistry()
	emptyTool, err := utils.InferTool(tools.ToolKnowledge, "empty test", func(context.Context, struct {
		Query string `json:"query"`
	}) (domain.RetrievalResult, error) { return domain.RetrievalResult{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(context.Background(), emptyTool); err != nil {
		t.Fatal(err)
	}
	e.tools = registry
	run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
	if err != nil || len(run.Result.Actions) != 0 {
		t.Fatal("read-only completion failed", err)
	}
	if len(run.Result.Evidence) != 0 {
		t.Fatal("invented evidence for empty retrieval")
	}
}

func TestToolCallingInvalidArgumentsCanBeRepaired(t *testing.T) {
	m := &scriptedToolModel{responses: []*schema.Message{
		toolResponse("bad", tools.ToolKnowledge, `{"query":123}`),
		toolResponse("good", tools.ToolKnowledge, `{"query":"ROI"}`), finalResponse("knowledge_qa"),
	}}
	e := toolEngine(t, m)
	run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
	if err != nil {
		t.Fatal(err)
	}
	calls := collection(run).ToolCalls
	if calls[0].ErrorCode != "invalid_arguments" || calls[0].Result != nil || calls[1].Error != "" {
		t.Fatalf("bad repair trace: %#v", calls)
	}
	last := m.inputs[1][len(m.inputs[1])-1]
	if last.Role != schema.Tool || last.ToolCallID != "bad" || !strings.Contains(last.Content, "invalid_arguments") {
		t.Fatal("repair feedback not correlated")
	}
}

func TestToolCallingMissingEvidenceDoesNotProduceHealthyDiagnosis(t *testing.T) {
	m := &scriptedToolModel{responses: []*schema.Message{finalResponse("diagnosis"), finalResponse("diagnosis"), finalResponse("diagnosis")}}
	e := toolEngine(t, m)
	run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
	if err == nil || run.Status != domain.RunFailed || run.Result != nil {
		t.Fatal("ungrounded diagnosis accepted")
	}
	if len(collection(run).ModelTurns) != 3 {
		t.Fatal("failed repair history missing")
	}
}

func TestToolCallingCannotInvokeWriteOrUnknownTools(t *testing.T) {
	for _, name := range []string{tools.ToolExecute, "delete_store", "mcp_write_budget"} {
		t.Run(name, func(t *testing.T) {
			m := &scriptedToolModel{responses: []*schema.Message{toolResponse("blocked", name, `{"action":"pause_campaign"}`)}}
			e := toolEngine(t, m)
			run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
			if err == nil || run.Result != nil || collection(run).ToolCalls[0].ErrorCode != "tool_denied" {
				t.Fatal("write/unknown tool was allowed")
			}
		})
	}
}

func TestToolCallingCannotOverrideTenantOrPolicy(t *testing.T) {
	state := &execution{Request: domain.DiagnosisRequest{StoreID: "trusted"}, Policy: policy.DefaultPolicy()}
	for _, raw := range []string{`{"store_id":"other"}`, `{"query":"ROI","store_id":"other"}`, `{"query":"ROI","top_k":999}`, `{"query":"ROI","required_approval_risk":"low"}`} {
		if _, err := normalizedReadArguments(tools.ToolKnowledge, raw, state); err == nil {
			t.Fatalf("accepted override %s", raw)
		}
	}
	args, err := normalizedReadArguments(tools.ToolKnowledge, `{"query":" ROI  处置 "}`, state)
	if err != nil || args["store_id"] != "trusted" || args["top_k"] != 3 || args["query"] != "ROI 处置" {
		t.Fatalf("trusted injection failed: %#v %v", args, err)
	}
}

func TestToolCallingStrictJSON(t *testing.T) {
	state := &execution{Request: domain.DiagnosisRequest{StoreID: "demo-store"}, Policy: policy.DefaultPolicy()}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"query":null}`, `{"query":""}`, `{"query":"a","query":"b"}`, `{"query":"a"} {}`, `{"query":["ROI"]}`, `{"query":"ROI","extra":true}`} {
		if _, err := normalizedReadArguments(tools.ToolKnowledge, raw, state); err == nil {
			t.Fatalf("accepted malformed args %s", raw)
		}
	}
	if _, err := normalizedReadArguments(tools.ToolCampaigns, `{}`, state); err != nil {
		t.Fatal(err)
	}
	if _, err := validatePlannerFinal(`{"task":"data_query","answer":"ok"}`, "diagnosis", true, true); err == nil {
		t.Fatal("explicit task ignored")
	}
}

func TestToolCallingCachesDuplicatesAndStopsLoops(t *testing.T) {
	for _, stop := range []bool{false, true} {
		m := &scriptedToolModel{responses: []*schema.Message{toolResponse("a", tools.ToolCampaigns, `{}`), toolResponse("b", tools.ToolCampaigns, `{}`)}}
		if stop {
			m.responses = append(m.responses, toolResponse("c", tools.ToolCampaigns, `{}`))
		} else {
			m.responses = append(m.responses, finalResponse("data_query"))
		}
		e := toolEngine(t, m)
		run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
		if stop != (err != nil) {
			t.Fatalf("stop=%v err=%v", stop, err)
		}
		if !collection(run).ToolCalls[1].Cached {
			t.Fatal("duplicate executed instead of cached")
		}
	}
}

func TestToolCallingBudgetsAndProtocolFailClosed(t *testing.T) {
	for _, kind := range []string{"batch_budget", "duplicate_id", "round_budget", "model_error", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			msg := toolResponse("a", tools.ToolCampaigns, `{}`)
			m := &scriptedToolModel{responses: []*schema.Message{msg}}
			switch kind {
			case "batch_budget":
				for i := 0; i < 4; i++ {
					tc := msg.ToolCalls[0]
					tc.ID = fmt.Sprint(i)
					msg.ToolCalls = append(msg.ToolCalls, tc)
				}
			case "duplicate_id":
				msg.ToolCalls = append(msg.ToolCalls, msg.ToolCalls[0])
			case "model_error":
				m.err = fmt.Errorf("provider unavailable")
			case "truncated":
				msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "length"}
			}
			e := toolEngine(t, m)
			if kind == "round_budget" {
				e.planner.limits.Rounds = 1
			}
			run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
			if err == nil || run.Status != domain.RunFailed || run.Result != nil {
				t.Fatal("unsafe completion")
			}
			if kind != "round_budget" && len(collection(run).ToolCalls) != 0 {
				t.Fatal("executed tool despite preflight failure")
			}
		})
	}
}

func TestToolCallingToolFailureAndTimeoutAreNotEmptyData(t *testing.T) {
	for _, kind := range []string{"error", "timeout", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			m := &scriptedToolModel{responses: []*schema.Message{toolResponse("a", tools.ToolCampaigns, `{}`), finalResponse("data_query"), finalResponse("data_query")}}
			e := toolEngine(t, m)
			e.planner.limits.ToolTimeout = 10 * time.Millisecond
			registry := tools.NewRegistry()
			tool, err := utils.InferTool(tools.ToolCampaigns, "test", func(ctx context.Context, _ struct {
				StoreID string `json:"store_id"`
			}) ([]domain.Campaign, error) {
				if kind == "timeout" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if kind == "oversize" {
					return []domain.Campaign{{Name: strings.Repeat("x", 40000)}}, nil
				}
				return nil, fmt.Errorf("read failed")
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.Register(context.Background(), tool); err != nil {
				t.Fatal(err)
			}
			e.tools = registry
			run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
			if err == nil || run.Result != nil || collection(run).ToolCalls[0].Error == "" {
				t.Fatal("failed read became valid data")
			}
		})
	}
}

func TestToolCallingReplayUsesSamePlannerWithoutExecution(t *testing.T) {
	m := &scriptedToolModel{responses: []*schema.Message{toolResponse("a", tools.ToolCampaigns, `{}`), toolResponse("b", tools.ToolKnowledge, `{"query":"低 ROI"}`), finalResponse("diagnosis")}}
	e := toolEngine(t, m)
	run, err := e.Replay(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store", Task: "diagnosis"}, policy.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range run.Steps {
		for _, call := range step.ToolCalls {
			if call.Name == tools.ToolExecute {
				t.Fatal("replay executed an operation")
			}
		}
	}
	if run.Result.Actions[0].Status != "waiting_approval" {
		t.Fatal("model-selected intent must not authorize writes, including low-risk tasks")
	}
}

func TestEinoToolCallingAdapterSendsSchemasAndToolResults(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []struct {
				Function struct {
					Name       string          `json:"name"`
					Parameters json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
			Messages []schema.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "bad body", 400)
			return
		}
		requests++
		if len(body.Tools) != 2 {
			t.Errorf("expected 2 native tool schemas, got %d", len(body.Tools))
		}
		for _, tool := range body.Tools {
			if tool.Function.Name == tools.ToolExecute || strings.Contains(string(tool.Function.Parameters), "store_id") {
				t.Error("unsafe schema exposed")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			fmt.Fprint(w, `{"id":"m1","object":"chat.completion","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"native-id","type":"function","function":{"name":"get_campaign_performance","arguments":"{}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
		} else {
			found := false
			for _, msg := range body.Messages {
				if msg.Role == schema.Tool && msg.ToolCallID == "native-id" && strings.Contains(msg.Content, "CMP-SEARCH-01") {
					found = true
				}
			}
			if !found {
				t.Error("native tool result missing")
			}
			fmt.Fprint(w, `{"id":"m2","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"{\"task\":\"data_query\",\"answer\":\"演示广告 ROI 为 1.13。\"}"}}]}`)
		}
	}))
	defer server.Close()
	planner, err := NewEinoToolCallingPlanner(context.Background(), "test-key", server.URL+"/v1", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	e := toolEngine(t, planner.model)
	e.planner = planner
	run, err := e.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store", Question: "只查广告 ROI"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || run.Result.Task != "data_query" || collection(run).ModelTurns[0].PromptTokens != 10 {
		t.Fatal("adapter did not complete observable native tool loop")
	}
}
