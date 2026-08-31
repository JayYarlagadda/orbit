package benchmark

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type HostInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
	CPUs     int    `json:"cpus"`
}

func CaptureEnvironment() (gitCommit string, gitDirty bool, goVersion string, host HostInfo, err error) {
	goVersion = runtime.Version()
	host = HostInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		CPUs: runtime.NumCPU(),
	}
	host.Hostname, _ = os.Hostname()

	gitCommit, gitDirty, err = gitStatus()
	return gitCommit, gitDirty, goVersion, host, err
}

func gitStatus() (commit string, dirty bool, err error) {
	commitOutput, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown", false, nil
	}
	commit = strings.TrimSpace(string(commitOutput))

	statusOutput, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return commit, false, nil
	}
	dirty = strings.TrimSpace(string(statusOutput)) != ""
	return commit, dirty, nil
}

func WriteSummary(path string, summary Summary) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		return fmt.Errorf("encode benchmark summary: %w", err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write benchmark summary: %w", err)
	}
	return nil
}

func FormatRFC3339(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
