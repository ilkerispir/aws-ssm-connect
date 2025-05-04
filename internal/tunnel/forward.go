package tunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
)

var CurrentPid int // exported for cleanup handling

// StartPortForward spawns a background SSM port-forward session
func StartPortForward(profile, instanceName, instanceID, remoteHost, remotePort, localPort string) error {
	if isPortInUse(localPort) {
		return fmt.Errorf("❌ Local port %s is already in use", localPort)
	}

	fmt.Printf("\n✅ Starting port-forward:\n💻 localhost:%s → 🖥️ %s (%s) → 🛢️ %s:%s\n\n",
		localPort, instanceName, instanceID, remoteHost, remotePort)

	cmd := exec.Command(
		"aws", "ssm", "start-session",
		"--profile", profile,
		"--target", instanceID,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", fmt.Sprintf("host=[\"%s\"],portNumber=[\"%s\"],localPortNumber=[\"%s\"]", remoteHost, remotePort, localPort),
	)

	// run in background silently
	null, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	cmd.Stdout = null
	cmd.Stderr = null
	cmd.Stdin = null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start port forward: %w", err)
	}

	CurrentPid = cmd.Process.Pid

	_ = SavePID(PIDInfo{
		PID:      CurrentPid,
		Profile:  profile,
		Instance: instanceName,
		DB:       fmt.Sprintf("%s:%s", remoteHost, localPort),
	})

	fmt.Printf("🔵 Port-forward started in background (PID %d)\n", CurrentPid)
	return nil
}

// isPortInUse checks if a local port is already bound
func isPortInUse(port string) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}
