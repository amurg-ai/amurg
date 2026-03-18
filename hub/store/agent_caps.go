package store

import "encoding/json"

func AgentAvailability(agent *Agent) (bool, string) {
	if agent == nil || agent.Caps == "" {
		return true, ""
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(agent.Caps), &raw); err != nil {
		return true, ""
	}
	availableRaw, ok := raw["available"]
	if !ok {
		return true, ""
	}

	available := true
	if err := json.Unmarshal(availableRaw, &available); err != nil {
		return true, ""
	}
	if available {
		return true, ""
	}

	reason := "agent is unavailable"
	if reasonRaw, ok := raw["unavailable_reason"]; ok {
		var parsed string
		if err := json.Unmarshal(reasonRaw, &parsed); err == nil && parsed != "" {
			reason = parsed
		}
	}
	return false, reason
}
