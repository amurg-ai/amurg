package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/config"
	"github.com/amurg-ai/amurg/runtime/internal/daemon"
	"github.com/amurg-ai/amurg/runtime/internal/ipc"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show runtime status",
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
	// Try IPC first for live status.
	if status, err := queryIPCStatus(); err == nil {
		connStatus := "disconnected"
		if status.HubConnected {
			connStatus = "connected"
		} else if status.Reconnecting {
			connStatus = "reconnecting"
		}

		_, _ = fmt.Fprintf(os.Stdout, "Status:   running\n")
		_, _ = fmt.Fprintf(os.Stdout, "Runtime:  %s\n", status.RuntimeID)
		_, _ = fmt.Fprintf(os.Stdout, "Hub:      %s (%s)\n", status.HubURL, connStatus)
		_, _ = fmt.Fprintf(os.Stdout, "Uptime:   %s\n", status.Uptime)
		_, _ = fmt.Fprintf(os.Stdout, "Sessions: %d/%d\n", status.Sessions, status.MaxSessions)
		_, _ = fmt.Fprintf(os.Stdout, "Agents:   %d\n", status.Agents)
		return nil
	}

	// Fall back to PID + config.
	pid, _ := daemon.ReadPID()

	if pid == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "Status:  stopped (no PID file)")
		return nil
	}

	if !daemon.IsRunning(pid) {
		_, _ = fmt.Fprintf(os.Stdout, "Status:  stopped (stale PID %d)\n", pid)
		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout, "Status:  running\n")
	_, _ = fmt.Fprintf(os.Stdout, "PID:     %d\n", pid)
	_, _ = fmt.Fprintf(os.Stdout, "Logs:    %s\n", daemon.LogPath())

	// Try to show config info.
	configPath := resolveConfigPath(cmd, nil, "runtime-config.json")
	cfg, err := config.Load(configPath)
	if err == nil {
		_, _ = fmt.Fprintf(os.Stdout, "Config:  %s\n", configPath)
		_, _ = fmt.Fprintf(os.Stdout, "Hub:     %s\n", cfg.Hub.URL)
		_, _ = fmt.Fprintf(os.Stdout, "Runtime: %s\n", cfg.Runtime.ID)
		_, _ = fmt.Fprintf(os.Stdout, "Agents:  %d configured\n", len(cfg.Agents))
	}

	return nil
}

func queryIPCStatus() (*ipc.StatusResult, error) {
	client, err := ipc.Dial(daemon.SocketPath())
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Call("status", nil)
	if err != nil {
		return nil, err
	}

	var status ipc.StatusResult
	if err := json.Unmarshal(resp.Data, &status); err != nil {
		return nil, err
	}
	return &status, nil
}
