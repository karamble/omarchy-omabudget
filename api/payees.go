package api

import (
	"net/http"

	"github.com/karamble/omarchy-omabudget/domain"
)

// Payees, spec 1.5. They are made by naming one on a transaction; these are
// for tidying them up afterwards.

func (s *Server) handlePayees(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	list, err := l.Payees(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, list)
}

// payeeEdit carries only what it changes. MergeInto moves every transaction
// to another payee and keeps this one's name as an alias.
type payeeEdit struct {
	Name        *string `json:"name"`
	Category    *string `json:"defaultCategory"`
	Notes       *string `json:"notes"`
	AddAlias    string  `json:"addAlias,omitempty"`
	RemoveAlias string  `json:"removeAlias,omitempty"`
	MergeInto   string  `json:"mergeInto,omitempty"`
}

func (s *Server) handleUpdatePayee(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in payeeEdit
	if !s.decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	p, err := l.Payee(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if in.MergeInto != "" {
		moved, err := l.MergePayees(ctx, p.ID, in.MergeInto)
		if err != nil {
			s.fail(w, err)
			return
		}
		into, err := l.Payee(ctx, in.MergeInto)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, s.logger, http.StatusOK, struct {
			Moved int `json:"moved"`
			domain.Payee
		}{moved, into})
		return
	}
	if in.Name != nil || in.Category != nil || in.Notes != nil {
		if in.Name != nil {
			p.Name = *in.Name
		}
		if in.Category != nil {
			p.Category = *in.Category
		}
		if in.Notes != nil {
			p.Notes = *in.Notes
		}
		if p, err = l.UpdatePayee(ctx, p); err != nil {
			s.fail(w, err)
			return
		}
	}
	if in.AddAlias != "" {
		if p, err = l.AddAlias(ctx, p.ID, in.AddAlias); err != nil {
			s.fail(w, err)
			return
		}
	}
	if in.RemoveAlias != "" {
		if p, err = l.RemoveAlias(ctx, p.ID, in.RemoveAlias); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, p)
}

func (s *Server) handleRemovePayee(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := l.RemovePayee(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"removed": id})
}
