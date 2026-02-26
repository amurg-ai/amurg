package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/daemon"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show runtime log output",
		RunE:  runLogs,
	}
	cmd.Flags().IntP("lines", "n", 50, "number of lines to show")
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	numLines, _ := cmd.Flags().GetInt("lines")
	follow, _ := cmd.Flags().GetBool("follow")

	logPath := daemon.LogPath()
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no log file found at %s", logPath)
		}
		return err
	}
	defer func() { _ = f.Close() }()

	// Read last N lines.
	lines, err := tailLines(f, numLines)
	if err != nil {
		return err
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(os.Stdout, line)
	}

	if !follow {
		return nil
	}

	// Follow mode: read new lines as they appear.
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// Wait briefly and retry.
				select {
				case <-cmd.Context().Done():
					return nil
				default:
				}
				continue
			}
			return err
		}
		_, _ = fmt.Fprint(os.Stdout, line)
	}
}

// tailLines reads the last n lines from the file.
func tailLines(f *os.File, n int) ([]string, error) {
	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines, scanner.Err()
}
