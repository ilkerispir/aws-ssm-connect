package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type PIDInfo struct {
	PID      int    `json:"pid"`
	Profile  string `json:"profile"`
	Instance string `json:"instance"`
	DB       string `json:"db"`
}

var pidsFilePath = filepath.Join(os.Getenv("HOME"), ".aws-ssm-connect", "pids.json")

// SavePID appends a new PID record to pids.json
func SavePID(info PIDInfo) error {
	var existing []PIDInfo

	data, err := os.ReadFile(pidsFilePath)
	if err == nil {
		_ = json.Unmarshal(data, &existing)
	}

	existing = append(existing, info)
	out, _ := json.MarshalIndent(existing, "", "  ")

	_ = os.MkdirAll(filepath.Dir(pidsFilePath), 0700)
	return os.WriteFile(pidsFilePath, out, 0600)
}

// ListPIDs prints alive PIDs and cleans up dead ones
func ListPIDs() error {
	data, err := os.ReadFile(pidsFilePath)
	if err != nil {
		return fmt.Errorf("could not read pids file: %w", err)
	}

	var pids []PIDInfo
	if err := json.Unmarshal(data, &pids); err != nil {
		return fmt.Errorf("could not parse pids file: %w", err)
	}

	var alive []PIDInfo
	for _, p := range pids {
		if processExists(p.PID) {
			alive = append(alive, p)
		}
	}

	out, _ := json.MarshalIndent(alive, "", "  ")
	_ = os.WriteFile(pidsFilePath, out, 0600)

	if len(alive) == 0 {
		fmt.Println("No active port-forward sessions.")
		return nil
	}

	fmt.Println("Active Port-Forward Sessions:")
	for _, p := range alive {
		fmt.Printf("🔵 PID: %d | Profile: %s | Instance: %s | DB: %s\n", p.PID, p.Profile, p.Instance, p.DB)
	}
	return nil
}

// KillPID kills a specific session by PID and removes it from file
func KillPID(pid int) error {
	fmt.Printf("🛑 Attempting to kill PID %d...\n", pid)

	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err != nil && !strings.Contains(err.Error(), "no such process") {
		return fmt.Errorf("failed to kill pid %d: %w", pid, err)
	}

	data, _ := os.ReadFile(pidsFilePath)
	var pids []PIDInfo
	_ = json.Unmarshal(data, &pids)

	var updated []PIDInfo
	for _, p := range pids {
		if p.PID != pid {
			updated = append(updated, p)
		}
	}

	out, _ := json.MarshalIndent(updated, "", "  ")
	return os.WriteFile(pidsFilePath, out, 0600)
}

// KillAllPIDs kills all sessions and clears pids.json
func KillAllPIDs() error {
	fmt.Println("🛑 Attempting to kill all active port-forward sessions...")

	data, err := os.ReadFile(pidsFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No active sessions to kill.")
			return nil
		}
		return fmt.Errorf("could not read pids file: %w", err)
	}

	var pids []PIDInfo
	_ = json.Unmarshal(data, &pids)

	killed := 0
	for _, p := range pids {
		err := syscall.Kill(-p.PID, syscall.SIGKILL)
		if err != nil && !strings.Contains(err.Error(), "no such process") {
			fmt.Printf("❌ Failed to kill PID %d: %v\n", p.PID, err)
		} else {
			fmt.Printf("✅ Killed PID %d\n", p.PID)
			killed++
		}
	}

	_ = os.Remove(pidsFilePath)

	if killed == 0 {
		fmt.Println("ℹ️ No alive sessions were found.")
	} else {
		fmt.Printf("🔵 Successfully killed %d sessions.\n", killed)
	}
	return nil
}

// processExists checks whether a process with given PID is alive
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
