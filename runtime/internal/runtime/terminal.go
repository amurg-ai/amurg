package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/amurg-ai/amurg/pkg/protocol"
)

func (r *Runtime) handleTerminal(env protocol.Envelope) error {
	data, err := json.Marshal(env.Payload)
	if err != nil {
		return err
	}
	var req protocol.TerminalRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := r.sessions.HandleTerminal(ctx, env.Type, req); err != nil {
		r.sendToHub(protocol.TypeTerminalOutput, req.SessionID, protocol.TerminalOutput{
			SessionID: req.SessionID, Generation: req.Generation, Kind: "error", Error: err.Error(),
		})
	}
	return nil
}
