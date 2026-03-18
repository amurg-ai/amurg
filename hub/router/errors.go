package router

import "fmt"

type AgentUnavailableError struct {
	AgentID string
	Reason  string
}

func (e AgentUnavailableError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("agent %s is unavailable: %s", e.AgentID, e.Reason)
	}
	return fmt.Sprintf("agent %s is unavailable", e.AgentID)
}
