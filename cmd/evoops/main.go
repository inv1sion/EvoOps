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
	"syscall"
	"time"

	"github.com/inv1sion/evoops/internal/app"
	"github.com/inv1sion/evoops/internal/config"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/httpapi"
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
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: evoops [serve|demo|evolve]\n", os.Args[1])
		os.Exit(2)
	}
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
	approve := flags.Bool("approve", false, "approve pending medium/high-risk operations")
	_ = flags.Parse(args)
	ctx := context.Background()
	application := bootstrap(ctx)
	defer application.Close()
	run, err := application.Agent.Run(ctx, domain.DiagnosisRequest{StoreID: *storeID, Question: "Diagnose the current operating decline and propose evidence-backed actions."})
	if err != nil {
		log.Fatal(err)
	}
	if *approve && run.Status == domain.RunWaitingApproval {
		run, err = application.Agent.Approve(ctx, run.ID, domain.ApprovalDecision{Approved: true, Reason: "CLI demo approval"})
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
	candidate, err := application.Evolution.GenerateCandidate(ctx)
	if err != nil {
		log.Fatal(err)
	}
	result, err := application.Evolution.Evaluate(ctx, candidate.Version)
	if err != nil {
		log.Fatal(err)
	}
	if result.Passed && *canary > 0 {
		if err := application.Evolution.StartCanary(ctx, candidate.Version, *canary); err != nil {
			log.Fatal(err)
		}
	}
	printJSON(map[string]any{"candidate": candidate, "evaluation": result, "canary_percent": *canary})
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		log.Fatal(err)
	}
}
