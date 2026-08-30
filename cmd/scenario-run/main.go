package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/JayYarlagadda/orbit/internal/scenariorunner"
)

func main() {
	var (
		scenarioPath = flag.String("scenario", "", "path to a scenario JSON document")
		databaseURL  = flag.String("database-url", os.Getenv("ORBIT_DATABASE_URL"), "PostgreSQL connection string")
		workDir      = flag.String("work-dir", "", "directory for logs and schedule artifacts")
		orbitd       = flag.String("orbitd", "", "path to the orbitd binary")
		gateway      = flag.String("gateway", "", "path to the gateway binary")
		client       = flag.String("client", "", "path to the client binary")
		orbitctl     = flag.String("orbitctl", "", "path to the orbitctl binary")
		timeout      = flag.Duration("timeout", 2*time.Minute, "scenario timeout")
	)
	flag.Parse()
	if *scenarioPath == "" || *databaseURL == "" {
		fmt.Fprintln(os.Stderr, "usage: scenario-run -scenario <path> -database-url <url> [-orbitd ...]")
		os.Exit(2)
	}
	runner, err := scenariorunner.New(scenariorunner.Config{
		DatabaseURL:  *databaseURL,
		ScenarioPath: *scenarioPath,
		WorkDir:      *workDir,
		Timeout:      *timeout,
		Binaries: scenariorunner.Binaries{
			Orbitd:   *orbitd,
			Gateway:  *gateway,
			Client:   *client,
			Orbitctl: *orbitctl,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result, err := runner.Run(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.MarshalIndent(struct {
		Passed     bool              `json:"passed"`
		Violations []json.RawMessage `json:"violations"`
		Scenario   string            `json:"scenario_name"`
		Seed       string            `json:"scenario_seed"`
	}{
		Passed:   result.Report.Passed,
		Scenario: result.Record.ScenarioName,
		Seed:     result.Record.ScenarioSeed,
	}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
	if !result.Report.Passed {
		for _, violation := range result.Report.Violations {
			fmt.Fprintf(os.Stderr, "%s: %s\n", violation.Invariant, violation.Message)
		}
		os.Exit(1)
	}
}
