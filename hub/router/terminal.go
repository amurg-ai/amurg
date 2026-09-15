package router

import (
	"context"
	"encoding/json"

	"github.com/amurg-ai/amurg/hub/store"
	"github.com/amurg-ai/amurg/pkg/protocol"
)

func (r *Router) sessionIsTerminal(ctx context.Context, sess *store.Session) bool {
	agent, err := r.store.GetAgent(ctx, sess.AgentID)
	if err != nil || agent == nil {
		return false
	}
	var caps protocol.ProfileCaps
	return json.Unmarshal([]byte(agent.Caps), &caps) == nil && caps.Terminal
}

func (r *Router) handleTerminalClient(cc *clientConn, env protocol.Envelope) {
	var req protocol.TerminalRequest
	data, err := json.Marshal(env.Payload)
	if err != nil || json.Unmarshal(data, &req) != nil {
		return
	}
	fail := func(message string) {
		r.sendToClient(cc, protocol.TypeTerminalOutput, req.SessionID, protocol.TerminalOutput{
			SessionID: req.SessionID, Generation: req.Generation, Kind: "error", Error: message,
		})
	}
	ctx := context.Background()
	sess, err := r.store.GetSession(ctx, req.SessionID)
	if err != nil || sess == nil || sess.UserID != cc.userID || sess.OrgID != cc.orgID {
		fail("terminal session not found or access denied")
		return
	}
	if !r.sessionIsTerminal(ctx, sess) {
		fail("session does not support terminal passthrough")
		return
	}
	if sess.State == "closed" {
		if env.Type != protocol.TypeTerminalAttach {
			fail("session is closed")
			return
		}
		if err := r.store.UpdateSessionState(ctx, sess.ID, "active"); err != nil {
			fail("failed to reopen terminal")
			return
		}
		r.broadcastToSession(sess.ID, protocol.TypeSessionReopened, map[string]string{"session_id": sess.ID})
	}
	if env.Type == protocol.TypeTerminalInput {
		if len(req.Data) == 0 || len(req.Data) > 32*1024 || req.Generation == "" {
			fail("invalid terminal input")
			return
		}
	} else if req.Cols < 2 || req.Cols > 500 || req.Rows < 2 || req.Rows > 300 {
		fail("invalid terminal dimensions")
		return
	}
	// Override every routing field. A browser can never select an arbitrary local
	// tmux target, runtime, command or another user's session through this channel.
	req.AgentID = sess.AgentID
	req.UserID = sess.UserID
	if env.Type == protocol.TypeTerminalAttach {
		r.mu.Lock()
		if r.subscribers[req.SessionID] == nil {
			r.subscribers[req.SessionID] = make(map[string]*clientConn)
		}
		r.subscribers[req.SessionID][cc.id] = cc
		r.mu.Unlock()
	}
	if !r.sendToRuntime(sess.RuntimeID, env.Type, sess.ID, req) {
		fail("runtime is offline; reconnect before sending input")
	}
}

func (r *Router) handleTerminalOutput(runtimeID string, env protocol.Envelope) {
	var out protocol.TerminalOutput
	data, err := json.Marshal(env.Payload)
	if err != nil || json.Unmarshal(data, &out) != nil {
		return
	}
	ctx := context.Background()
	sess, err := r.store.GetSession(ctx, out.SessionID)
	if err != nil || sess == nil || sess.RuntimeID != runtimeID || sess.State == "closed" || !r.sessionIsTerminal(ctx, sess) {
		return
	}
	if len(out.Data) > 32*1024 || len(out.Error) > 4096 {
		return
	}
	// These bytes describe a live terminal display; storing them as assistant chat
	// messages would both corrupt the conversation and grow the database needlessly.
	r.broadcastToSession(out.SessionID, protocol.TypeTerminalOutput, out)
}

// A runtime can reconnect while browser sockets remain connected. Ask active
// viewers for a fresh attachment instead of applying a stream with a gap.
func (r *Router) reconnectTerminals(runtimeID string) {
	r.mu.RLock()
	ids := make([]string, 0, len(r.subscribers))
	for id := range r.subscribers {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	ctx := context.Background()
	for _, id := range ids {
		sess, err := r.store.GetSession(ctx, id)
		if err == nil && sess != nil && sess.RuntimeID == runtimeID && sess.State != "closed" && r.sessionIsTerminal(ctx, sess) {
			r.broadcastToSession(id, protocol.TypeTerminalOutput, protocol.TerminalOutput{SessionID: id, Kind: "reconnect"})
		}
	}
}
