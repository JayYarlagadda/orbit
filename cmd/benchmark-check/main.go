package main

import (
	"fmt"
	"os"

	"github.com/JayYarlagadda/orbit/internal/benchmark"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <benchmark-config.json>\n", os.Args[0])
		os.Exit(2)
	}
	config, err := benchmark.LoadConfig(os.Args[1])
	if err != nil {
		exitError(err)
	}
	if err := config.Validate(); err != nil {
		exitError(err)
	}
	fmt.Printf("valid benchmark %q (%s): %d clients, %d commands, %d trials\n",
		config.Name, config.MatrixID, config.Clients, config.Commands, config.Trials)
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
