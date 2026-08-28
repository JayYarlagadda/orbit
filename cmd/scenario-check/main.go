package main

import (
	"fmt"
	"os"

	"github.com/JayYarlagadda/orbit/internal/scenario"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: scenario-check <scenario.json>")
		os.Exit(2)
	}

	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open scenario: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	document, err := scenario.Load(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid scenario: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("valid scenario %q: schema=%s seed=%s events=%d duration_ms=%d\n",
		document.Name,
		document.SchemaVersion,
		document.Seed,
		len(document.Events),
		document.DurationMS,
	)
}
