package api

import (
	"net/http"
	"time"

	"github.com/karamble/omarchy-omabudget/alerts"
	"github.com/karamble/omarchy-omabudget/mcpserver"
)

// Arming a watch was reachable only over MCP, which meant only an agent could
// do it. These are the same thing for a person.

func (s *Server) engineOr(w http.ResponseWriter) (mcpserver.Alerts, bool) {
	e := s.alertEngine()
	if e == nil {
		writeJSON(w, s.logger, http.StatusServiceUnavailable, map[string]string{"error": "alerts are not running"})
		return nil, false
	}
	return e, true
}

func (s *Server) handleArm(w http.ResponseWriter, r *http.Request) {
	e, ok := s.engineOr(w)
	if !ok {
		return
	}
	var in alerts.Spec
	if !s.decode(w, r, &in) {
		return
	}
	t, err := in.Trigger(time.Now(), "you")
	if err != nil {
		s.fail(w, err)
		return
	}
	armed, err := e.Arm(t)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, armed)
}

func (s *Server) handleEditAlert(w http.ResponseWriter, r *http.Request) {
	e, ok := s.engineOr(w)
	if !ok {
		return
	}
	var in alerts.Spec
	if !s.decode(w, r, &in) {
		return
	}
	next, err := in.Trigger(time.Now(), "you")
	if err != nil {
		s.fail(w, err)
		return
	}
	edited, err := e.Edit(r.PathValue("id"), func(cur *alerts.Trigger) {
		cur.Path, cur.Operator, cur.Params, cur.Where = next.Path, next.Operator, next.Params, next.Where
		cur.DeliverTo, cur.Standing, cur.Reason, cur.ExpiresAt = next.DeliverTo, next.Standing, next.Reason, next.ExpiresAt
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, edited)
}
