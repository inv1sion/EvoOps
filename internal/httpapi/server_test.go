package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inv1sion/evoops/internal/app"
	"github.com/inv1sion/evoops/internal/config"
	"github.com/inv1sion/evoops/internal/domain"
)

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
