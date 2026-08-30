package scenario

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompileScheduleMatchesGoldenFixture(t *testing.T) {
	scenarioPath := filepath.Join("..", "..", "scenarios", "examples", "offline-reconnect.v1.json")
	goldenPath := filepath.Join("..", "..", "scenarios", "golden", "offline-reconnect.v1.schedule.json")

	scenarioFile, err := os.Open(scenarioPath)
	if err != nil {
		t.Fatal(err)
	}
	defer scenarioFile.Close()

	document, err := Load(scenarioFile)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := CompileScheduleCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if actual != string(expected) {
		t.Fatalf("compiled schedule does not match golden fixture")
	}
}

func TestSplitMix64IsDeterministic(t *testing.T) {
	first := newSplitMix64(42)
	second := newSplitMix64(42)
	for range 8 {
		if first.nextUint64() != second.nextUint64() {
			t.Fatal("splitmix64 stream diverged for the same seed")
		}
	}
}
