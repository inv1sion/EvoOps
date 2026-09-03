package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/inv1sion/evoops/internal/app"
	"github.com/inv1sion/evoops/internal/config"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/httpapi"
	"github.com/inv1sion/evoops/internal/rag"
)

func main() {
	if len(os.Args) < 2 {
		serve(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "demo":
		demo(os.Args[2:])
	case "evolve":
		evolve(os.Args[2:])
	case "harness":
		runHarness(os.Args[2:])
	case "ingest":
		ingest(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: evoops [serve|demo|harness|evolve|ingest]\n", os.Args[1])
		os.Exit(2)
	}
}

func ingest(args []string) {
	flags := flag.NewFlagSet("ingest", flag.ExitOnError)
	path := flags.String("path", "", "PDF, TXT, Markdown or JSONL document path")
	title := flags.String("title", "", "document title; defaults to the file name")
	scope := flags.String("scope", rag.ScopePlatform, "platform or store")
	storeID := flags.String("store", "", "required when scope=store")
	_ = flags.Parse(args)
	if *path == "" {
		log.Fatal("ingest requires -path")
	}
	parsed, err := rag.ParseFile(*path)
	if err != nil {
		log.Fatal(err)
	}
	absPath, err := filepath.Abs(*path)
	if err != nil {
		log.Fatal(err)
	}
	if *title == "" {
		*title = filepath.Base(absPath)
	}
	ctx := context.Background()
	application := bootstrap(ctx)
	defer application.Close()
	if application.RAG == nil {
		log.Fatal("ingest requires EVOOPS_RAG_BACKEND=external")
	}
	result, err := application.RAG.Ingest(ctx, rag.IngestInput{
		StoreID: *storeID, Scope: *scope, Title: *title, SourceURI: "file:///" + filepath.ToSlash(absPath),
		MediaType: parsed.MediaType, Content: parsed.Content, Metadata: map[string]any{"source_path": absPath},
	})
	if err != nil {
		log.Fatal(err)
	}
	printJSON(result)
}

func bootstrap(ctx context.Context) *app.App {
	application, err := app.New(ctx, config.Load())
	if err != nil {
		log.Fatal(err)
	}
	return application
}

func serve(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	address := flags.String("addr", "", "HTTP listen address; overrides EVOOPS_ADDR")
	_ = flags.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application := bootstrap(ctx)
	defer application.Close()
	if *address == "" {
		*address = application.Config.Address
	}
	server := &http.Server{Addr: *address, Handler: httpapi.New(application).Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("EvoOps listening", "address", *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func demo(args []string) {
	flags := flag.NewFlagSet("demo", flag.ExitOnError)
	storeID := flags.String("store", "demo-store", "store identifier")
	question := flags.String("question", "找出低 ROI 广告并给出受控处置建议。", "question for the advertising assistant")
	task := flags.String("task", "auto", "auto, diagnosis, data_query or knowledge_qa")
	approve := flags.Bool("approve", false, "approve pending medium/high-risk operations")
	_ = flags.Parse(args)
	ctx := context.Background()
	application := bootstrap(ctx)
	defer application.Close()
	run, err := application.Agent.Run(ctx, domain.DiagnosisRequest{StoreID: *storeID, Question: *question, Task: *task})
	if err != nil {
		log.Fatal(err)
	}
	if *approve && run.Status == domain.RunWaitingApproval {
		run, err = application.Agent.Approve(ctx, run.ID, domain.ApprovalDecision{Approved: true, Reason: "CLI 演示：已人工复核广告暂停操作"})
		if err != nil {
			log.Fatal(err)
		}
	}
	printJSON(run)
}

func evolve(args []string) {
	flags := flag.NewFlagSet("evolve", flag.ExitOnError)
	canary := flags.Int("canary", 0, "start a canary at this percentage if replay passes")
	_ = flags.Parse(args)
	ctx := context.Background()
	application := bootstrap(ctx)
	defer application.Close()
	run, err := application.Evolution.Evolve(ctx, *canary)
	if err != nil {
		log.Fatal(err)
	}
	printJSON(run)
}

func runHarness(args []string) {
	flags := flag.NewFlagSet("harness", flag.ExitOnError)
	version := flags.String("policy", "", "policy version; defaults to active")
	_ = flags.Parse(args)
	ctx := context.Background()
	application := bootstrap(ctx)
	defer application.Close()
	if *version == "" {
		state, err := application.Policies.State(ctx)
		if err != nil {
			log.Fatal(err)
		}
		*version = state.ActiveVersion
	}
	report, err := application.Evolution.RunHarness(ctx, *version)
	if err != nil {
		log.Fatal(err)
	}
	printJSON(report)
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		log.Fatal(err)
	}
}
