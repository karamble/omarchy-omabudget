package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/karamble/omarchy-omabudget/config"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/money"
)

func (s *Server) handleEnvelopes(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	sp, err := s.spanFor(r.URL.Query().Get("period"))
	if err != nil {
		s.fail(w, err)
		return
	}
	e, err := l.Envelopes(r.Context(), sp.key(), sp.from(), sp.to())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, e)
}

// handleRollover closes one period into the next: from defaults to the
// period before to, and to defaults to the current one.
func (s *Server) handleRollover(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in struct {
		From string `json:"from,omitempty"`
		To   string `json:"to,omitempty"`
	}
	if r.ContentLength != 0 && !s.decode(w, r, &in) {
		return
	}
	to, err := s.spanFor(in.To)
	if err != nil {
		s.fail(w, err)
		return
	}
	from := span{to.start.AddDate(0, -1, 0)}
	if in.From != "" {
		if from, err = s.spanFor(in.From); err != nil {
			s.fail(w, err)
			return
		}
	}
	n, err := l.Rollover(r.Context(), from.key(), from.from(), from.to(), to.key())
	if err != nil {
		s.fail(w, err)
		return
	}
	e, err := l.Envelopes(r.Context(), to.key(), to.from(), to.to())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Changed int `json:"changed"`
		domain.Envelopes
	}{n, e})
}

// categoryIn is a new category as typed.
type categoryIn struct {
	Name      string `json:"name"`
	Parent    string `json:"parent,omitempty"`    // a group's name or id; empty makes a group
	Kind      string `json:"kind,omitempty"`      // expense (default) or income; a child takes its parent's
	Icon      string `json:"icon,omitempty"`      // a Nerd Font glyph
	Behaviour string `json:"behaviour,omitempty"` // monthly (default), rollover or untracked
	Excluded  bool   `json:"excludedFromStatistics,omitempty"`
	SortOrder int    `json:"sortOrder,omitempty"`
}

func (s *Server) handleAddCategory(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in categoryIn
	if !s.decode(w, r, &in) {
		return
	}
	out, err := l.AddCategory(r.Context(), domain.Category{
		Name: in.Name, ParentID: in.Parent, Kind: in.Kind, Icon: in.Icon,
		Behaviour: in.Behaviour, Excluded: in.Excluded, SortOrder: in.SortOrder,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, out)
}

// categoryEdit changes only the fields it carries: what the category is
// called and how it is drawn, whether statistics skip it, whether it is
// archived, and its pot behaviour or goal.
type categoryEdit struct {
	Name       *string `json:"name"`
	Icon       *string `json:"icon"`
	Excluded   *bool   `json:"excludedFromStatistics"`
	Archived   *bool   `json:"archived"`
	SortOrder  *int    `json:"sortOrder"`
	Behaviour  *string `json:"behaviour"`
	GoalTarget *string `json:"goalTarget"` // an amount; 0 clears the goal
	GoalDue    string  `json:"goalDue,omitempty"`
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in categoryEdit
	if !s.decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	c, err := l.CategoryAny(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if in.Name != nil || in.Icon != nil || in.Excluded != nil || in.Archived != nil || in.SortOrder != nil {
		if in.Name != nil {
			c.Name = *in.Name
		}
		if in.Icon != nil {
			c.Icon = *in.Icon
		}
		if in.Excluded != nil {
			c.Excluded = *in.Excluded
		}
		if in.Archived != nil {
			c.Archived = *in.Archived
		}
		if in.SortOrder != nil {
			c.SortOrder = *in.SortOrder
		}
		if c, err = l.UpdateCategory(ctx, c); err != nil {
			s.fail(w, err)
			return
		}
	}
	if in.GoalTarget != nil {
		target, err := money.Parse(*in.GoalTarget, l.Base())
		if err != nil && !errors.Is(err, money.ErrZero) {
			s.fail(w, err)
			return
		}
		if c, err = l.SetCategoryGoal(ctx, c.ID, target.Minor, in.GoalDue); err != nil {
			s.fail(w, err)
			return
		}
	}
	if in.Behaviour != nil {
		if c, err = l.SetCategoryBehaviour(ctx, c.ID, strings.ToLower(*in.Behaviour)); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, c)
}

func (s *Server) handleRemoveCategory(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := l.RemoveCategory(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"removed": id})
}

// settingsOut is what the app may change at runtime.
type settingsOut struct {
	BaseCurrency   string `json:"baseCurrency"`
	Model          string `json:"model"`
	PeriodStartDay int    `json:"periodStartDay"`
	LargeAmount    int64  `json:"largeAmount"`
	Monitoring     bool   `json:"monitoring"`
}

func (s *Server) settings() settingsOut {
	c := s.Config()
	return settingsOut{
		BaseCurrency: c.BaseCurrency, Model: string(c.Model), PeriodStartDay: c.PeriodStartDay,
		LargeAmount: c.LargeAmount, Monitoring: c.MonitoringOn(),
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, s.settings())
}

// settingsIn changes only the fields it carries. The base currency is not
// here: base amounts are frozen at entry, so it is fixed once the ledger
// has postings.
type settingsIn struct {
	Model          *string `json:"model"`
	PeriodStartDay *int    `json:"periodStartDay"`
	LargeAmount    *string `json:"largeAmount"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsIn
	if !s.decode(w, r, &in) {
		return
	}
	var large *int64
	if in.LargeAmount != nil {
		v, err := money.Parse(*in.LargeAmount, s.Config().BaseCurrency)
		if err != nil && !errors.Is(err, money.ErrZero) {
			s.fail(w, err)
			return
		}
		if v.Minor < 0 {
			v.Minor = -v.Minor
		}
		large = &v.Minor
	}
	err := s.mutate(func(c *config.Config) error {
		if in.Model != nil {
			m := config.Model(strings.ToLower(*in.Model))
			if m != config.ModelLimits && m != config.ModelEnvelope {
				return fmt.Errorf("model %q: limits or envelope", *in.Model)
			}
			c.Model = m
		}
		if in.PeriodStartDay != nil {
			if *in.PeriodStartDay < 1 || *in.PeriodStartDay > 28 {
				return errors.New("the period start day is between 1 and 28")
			}
			c.PeriodStartDay = *in.PeriodStartDay
		}
		if large != nil {
			c.LargeAmount = *large
		}
		return nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.settings())
}
