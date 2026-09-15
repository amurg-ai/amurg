package router

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/amurg-ai/amurg/hub/store"
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/gorilla/websocket"
)

func readTerminalEnvelope(t *testing.T, conn *websocket.Conn) protocol.Envelope {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var env protocol.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestTerminalRoutingOwnershipAndBytes(t *testing.T) {
	r, db, auth := setupTestRouter(t)
	ctx := context.Background()
	seedRuntimeAndAgent(t, db, "rt-1", "agent-terminal")
	a, _ := db.GetAgent(ctx, "agent-terminal")
	caps, _ := json.Marshal(protocol.KnownProfiles[protocol.ProfileCodex])
	a.Caps = string(caps)
	a.Profile = protocol.ProfileCodex
	if err := db.UpsertAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	owner := seedUser(t, auth, "terminalowner")
	other := seedUser(t, auth, "terminalother")
	sess := &store.Session{ID: "terminal-session", OrgID: "default", UserID: owner, AgentID: a.ID, RuntimeID: "rt-1", Profile: protocol.ProfileCodex, State: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	runtimeServer, runtimeClient := newWSPair(t)
	r.runtimes["rt-1"] = &runtimeConn{id: "rt-1", orgID: "default", conn: runtimeServer}
	clientServer, client := newWSPair(t)
	cc := &clientConn{id: "terminal-client", userID: owner, orgID: "default", conn: clientServer}
	req := protocol.TerminalRequest{SessionID: sess.ID, Cols: 80, Rows: 24, AgentID: "forged-agent", UserID: "forged-user"}
	r.handleClientMessage(cc, protocol.Envelope{Type: protocol.TypeTerminalAttach, Payload: req})
	forwarded := readTerminalEnvelope(t, runtimeClient)
	data, _ := json.Marshal(forwarded.Payload)
	var attached protocol.TerminalRequest
	_ = json.Unmarshal(data, &attached)
	if attached.AgentID != a.ID || attached.UserID != owner {
		t.Fatalf("trusted browser routing fields: %+v", attached)
	}
	req.Generation = "generation-1"
	req.Data = []byte{0, 3, 27, 255, 0xc3, 0xa9}
	r.handleClientMessage(cc, protocol.Envelope{Type: protocol.TypeTerminalInput, Payload: req})
	forwarded = readTerminalEnvelope(t, runtimeClient)
	data, _ = json.Marshal(forwarded.Payload)
	var input protocol.TerminalRequest
	_ = json.Unmarshal(data, &input)
	if !bytes.Equal(input.Data, req.Data) {
		t.Fatalf("input corrupted: %x", input.Data)
	}
	out := protocol.TerminalOutput{SessionID: sess.ID, Generation: req.Generation, Kind: "output", Data: req.Data}
	r.handleTerminalOutput("rt-1", protocol.Envelope{Payload: out})
	forwarded = readTerminalEnvelope(t, client)
	data, _ = json.Marshal(forwarded.Payload)
	var received protocol.TerminalOutput
	_ = json.Unmarshal(data, &received)
	if !bytes.Equal(received.Data, req.Data) {
		t.Fatalf("output corrupted: %x", received.Data)
	}
	messages, err := db.GetMessages(ctx, sess.ID, 0, 100)
	if err != nil || len(messages) != 0 {
		t.Fatalf("terminal stream stored as chat: %v %v", messages, err)
	}
	// Another authenticated user is denied even if all routing fields are forged.
	cc.userID = other
	r.handleClientMessage(cc, protocol.Envelope{Type: protocol.TypeTerminalAttach, Payload: req})
	forwarded = readTerminalEnvelope(t, client)
	data, _ = json.Marshal(forwarded.Payload)
	_ = json.Unmarshal(data, &received)
	if received.Kind != "error" {
		t.Fatal("cross-user terminal access accepted")
	}
}
