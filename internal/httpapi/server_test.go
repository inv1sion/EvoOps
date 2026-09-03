package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inv1sion/evoops/internal/app"
	"github.com/inv1sion/evoops/internal/config"
	"github.com/inv1sion/evoops/internal/domain"
)

func TestConsoleUsesSimplifiedChinese(t *testing.T) {
	page, err := assets.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	body := string(page)
	for _, want := range []string{`lang="zh-CN"`, "广告投放 ROI 助手", "哪些广告正在低效消耗？", "本次广告结论", "低 ROI 计划", "建议行动", "Agent 实验室", "执行轨迹", "广告证据链", "长期记忆", "运行自进化评测"} {
		if !strings.Contains(body, want) {
			t.Fatalf("console is missing Chinese copy %q", want)
		}
	}
	if !strings.Contains(body, `id="assistant-view" class="view active"`) || strings.Contains(body, `id="lab-view" class="view active"`) {
		t.Fatal("advertising assistant must be the default view")
	}
	for _, unwanted := range []string{"Evidence before action.", "Run diagnosis", "Signals & actions", "Execution trajectory"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("console still contains English copy %q", unwanted)
		}
	}
}

func TestFailedRunReturnsPersistedTrajectory(t *testing.T) {
	a, err := app.New(context.Background(), config.Config{DataDir: t.TempDir(), DemoDataPath: "../../data/demo/store.json", HarnessDataPath: "../../data/harness/suite.json"})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"store_id":"nonexistent-store"}`))
	w := httptest.NewRecorder()
	New(a).Handler().ServeHTTP(w, r)
	var result struct {
		Error string      `json:"error"`
		Run   *domain.Run `json:"run"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusUnprocessableEntity || result.Error == "" || result.Run == nil || result.Run.Status != domain.RunFailed || len(result.Run.Steps) == 0 {
		t.Fatalf("failed trajectory missing: %s", w.Body.String())
	}
	if _, err := a.Repo.GetRun(context.Background(), result.Run.ID); err != nil {
		t.Fatal("failed run was not persisted")
	}
}

func TestApprovalEndpointRequiresRole(t *testing.T) {
	application, err := app.New(context.Background(), config.Config{
		DataDir: t.TempDir(), DemoDataPath: "../../data/demo/store.json",
		HarnessDataPath: "../../data/harness/suite.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	server := httptest.NewServer(New(application).Handler())
	defer server.Close()

	payload, _ := json.Marshal(domain.DiagnosisRequest{StoreID: "demo-store"})
	response, err := http.Post(server.URL+"/api/runs", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	decision := []byte(`{"approved":true}`)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/runs/"+run.ID+"/approve", bytes.NewReader(decision))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("without role status=%d, want 403", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/runs/"+run.ID+"/approve", bytes.NewReader(decision))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-EvoOps-Role", "approver")
	request.Header.Set("X-EvoOps-Actor", "test-user")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("with role status=%d, want 200", response.StatusCode)
	}
}

func TestEvolutionEndpointRunsHarnessWithoutAutoPromotion(t *testing.T) {
	application, err := app.New(context.Background(), config.Config{
		DataDir: t.TempDir(), DemoDataPath: "../../data/demo/store.json",
		HarnessDataPath: "../../data/harness/suite.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	server := httptest.NewServer(New(application).Handler())
	defer server.Close()

	body := []byte(`{"canary_percent":10}`)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/evolution/run", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("without admin status=%d, want 403", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/evolution/run", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-EvoOps-Role", "admin")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("with admin status=%d, want 200", response.StatusCode)
	}
	var evolution domain.EvolutionRun
	if err := json.NewDecoder(response.Body).Decode(&evolution); err != nil {
		t.Fatal(err)
	}
	if evolution.Status != "canary" || !evolution.BaselineReport.Passed || !evolution.CandidateReport.Passed {
		t.Fatalf("unexpected evolution result: %#v", evolution)
	}
	state, err := application.Policies.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveVersion != "v1.0.0" || state.CanaryVersion != evolution.Candidate.Version {
		t.Fatalf("evolution auto-promoted or lost canary: %#v", state)
	}
	stored := state.Policies[evolution.Candidate.Version]
	if stored.EvaluationReportID != evolution.CandidateReport.ID || stored.EvaluatedAgainstVersion != "v1.0.0" {
		t.Fatalf("missing release credential: %#v", stored)
	}
}

func TestFeedbackBecomesStoreMemoryAndPersonalizesNextRun(t *testing.T) {
	application, err := app.New(context.Background(), config.Config{
		DataDir: t.TempDir(), DemoDataPath: "../../data/demo/store.json",
		HarnessDataPath: "../../data/harness/suite.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	server := httptest.NewServer(New(application).Handler())
	defer server.Close()

	run, err := application.Agent.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
	if err != nil || run.Result == nil || len(run.Result.Actions) == 0 {
		t.Fatalf("initial run=%#v err=%v", run, err)
	}
	selected := run.Result.Actions[0]
	payload, _ := json.Marshal(map[string]any{"useful": true, "accepted_actions": []string{selected.ID}})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/runs/"+run.ID+"/feedback", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-EvoOps-Role", "operator")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		defer response.Body.Close()
		t.Fatalf("feedback status=%d", response.StatusCode)
	}
	var feedback domain.Feedback
	if err := json.NewDecoder(response.Body).Decode(&feedback); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if feedback.StoreID != "demo-store" || len(feedback.MemoryUpdates) != 2 {
		t.Fatalf("feedback did not create auditable memory: %#v", feedback)
	}

	request, _ = http.NewRequest(http.MethodGet, server.URL+"/api/stores/demo-store/memory", nil)
	request.Header.Set("X-EvoOps-Role", "operator")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var profile domain.MerchantMemoryProfile
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(profile.Memories) != 2 {
		t.Fatalf("memory profile=%#v", profile)
	}

	next, err := application.Agent.Run(context.Background(), domain.DiagnosisRequest{StoreID: "demo-store"})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Steps) < 2 || next.Steps[1].Name != "load_merchant_memory" {
		t.Fatalf("memory node missing from Eino trajectory: %#v", next.Steps)
	}
	found := false
	selectedOperation, _ := selected.Arguments["action"].(string)
	selectedTarget, _ := selected.Arguments["target"].(string)
	for _, action := range next.Result.Actions {
		operation, _ := action.Arguments["action"].(string)
		target, _ := action.Arguments["target"].(string)
		if operation == selectedOperation && target == selectedTarget {
			found = action.Preference == "preferred" && len(action.MemoryRefs) == 1
		}
	}
	if !found {
		t.Fatalf("next run did not apply memory: %#v", next.Result.Actions)
	}
}
