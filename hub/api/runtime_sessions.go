package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/amurg-ai/amurg/hub/auth"
	"github.com/amurg-ai/amurg/hub/router"
	"github.com/amurg-ai/amurg/hub/store"
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/go-chi/chi/v5"
)

type runtimeSessionKey struct{}

// A device registration authorizes launches only on that runtime, for its
// registering user. Static/shared runtime tokens have no user identity.
func (s *Server) runtimeSessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing runtime token")
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		rt, err := s.store.GetRuntimeTokenByHash(r.Context(), sha256hex(token))
		if err != nil || rt == nil || rt.CreatedBy == "" {
			writeError(w, http.StatusUnauthorized, "a user-linked runtime registration token is required")
			return
		}
		user, err := s.store.GetUserByID(r.Context(), rt.CreatedBy)
		if err != nil || user == nil || user.OrgID != rt.OrgID {
			writeError(w, http.StatusForbidden, "runtime owner no longer belongs to this organization")
			return
		}
		ctx := context.WithValue(r.Context(), runtimeSessionKey{}, rt)
		ctx = context.WithValue(ctx, identityKey, &auth.Identity{UserID: user.ID, OrgID: user.OrgID, Role: user.Role})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleRuntimeSessionCreate(w http.ResponseWriter, r *http.Request) {
	rt := r.Context().Value(runtimeSessionKey{}).(*store.RuntimeToken)
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	var req struct {
		AgentID string                 `json:"agent_id"`
		Launch  protocol.SessionLaunch `json:"launch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AgentID == "" || len(req.AgentID) > 128 {
		writeError(w, http.StatusBadRequest, "invalid launch request")
		return
	}
	if !filepath.IsAbs(req.Launch.WorkDir) || len(req.Launch.WorkDir) > 4096 || len(req.Launch.Args) > 128 {
		writeError(w, http.StatusBadRequest, "launch requires an absolute working directory and at most 128 arguments")
		return
	}
	agent, err := s.store.GetAgent(r.Context(), req.AgentID)
	if err != nil || agent == nil || agent.RuntimeID != rt.RuntimeID || agent.OrgID != rt.OrgID {
		writeError(w, http.StatusForbidden, "agent does not belong to this runtime")
		return
	}
	if s.defaultAgentAccess == "none" {
		allowed, err := s.store.HasAgentAccess(r.Context(), rt.CreatedBy, agent.ID)
		if err != nil || !allowed {
			writeError(w, http.StatusForbidden, "no access to this agent")
			return
		}
	}
	if !s.router.RuntimeSupportsLocalLaunch(rt.RuntimeID) {
		writeError(w, http.StatusConflict, "runtime is offline or needs an update for local launch support")
		return
	}
	if s.enforcer != nil {
		if err := s.enforcer.CheckTrialExpiry(r.Context(), rt.OrgID); err != nil {
			writeError(w, http.StatusPaymentRequired, err.Error())
			return
		}
		if err := s.enforcer.CheckSessionLimit(r.Context(), rt.OrgID); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	sess, err := s.router.CreateSession(r.Context(), rt.CreatedBy, agent.ID, router.CreateSessionOption{Launch: &req.Launch})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = s.store.UpdateRuntimeTokenLastUsed(r.Context(), rt.ID)
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleRuntimeSessionGet(w http.ResponseWriter, r *http.Request) {
	rt := r.Context().Value(runtimeSessionKey{}).(*store.RuntimeToken)
	sess, err := s.store.GetSession(r.Context(), chi.URLParam(r, "sessionID"))
	if err != nil || sess == nil || sess.RuntimeID != rt.RuntimeID || sess.UserID != rt.CreatedBy || sess.OrgID != rt.OrgID {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}
