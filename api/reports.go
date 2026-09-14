package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/config"
	"github.com/karamble/omarchy-omabudget/domain"
	"github.com/karamble/omarchy-omabudget/store"
)

// handleSpending reports one period against the one before, or an explicit
// window against the same length before it.
func (s *Server) handleSpending(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	var from, to, prevFrom, prevTo, key string
	if q.Get("from") != "" || q.Get("to") != "" {
		from, to = q.Get("from"), q.Get("to")
		a, err := time.Parse(dateFmt, from)
		if err != nil {
			s.fail(w, fmt.Errorf("from %q must be YYYY-MM-DD", from))
			return
		}
		b, err := time.Parse(dateFmt, to)
		if err != nil || b.Before(a) {
			s.fail(w, fmt.Errorf("to %q must be YYYY-MM-DD, on or after from", to))
			return
		}
		days := int(b.Sub(a).Hours()/24) + 1
		prevTo = a.AddDate(0, 0, -1).Format(dateFmt)
		prevFrom = a.AddDate(0, 0, -days).Format(dateFmt)
	} else {
		rep, err := s.spendingFor(r.Context(), l, q.Get("period"))
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, s.logger, http.StatusOK, rep)
		return
	}
	rep, err := l.Spending(r.Context(), from, to, prevFrom, prevTo, key)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, rep)
}

// spendingFor is the report for one period key against the period before.
func (s *Server) spendingFor(ctx context.Context, l *domain.Ledger, period string) (domain.SpendingReport, error) {
	sp, err := s.spanFor(period)
	if err != nil {
		return domain.SpendingReport{}, err
	}
	prev := span{sp.start.AddDate(0, -1, 0)}
	return l.Spending(ctx, sp.from(), sp.to(), prev.from(), prev.to(), sp.key())
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	now := clock()
	sp := span{periodStart(now, s.Config().PeriodStartDay)}
	p := sp.bounds(now)
	trailing := make([][2]string, 0, 3)
	for _, t := range spansBack(span{sp.start.AddDate(0, -1, 0)}, 3) {
		trailing = append(trailing, [2]string{t.from(), t.to()})
	}
	m, err := l.Metrics(r.Context(), p.From, p.To, p.Days, p.Elapsed, now.Format(dateFmt), trailing)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, m)
}

// exportIn names a file to write under the home directory.
type exportIn struct {
	Format string `json:"format,omitempty"` // journal or csv
	Path   string `json:"path"`
}

// userFile resolves a path the user typed to a directory and a file name
// inside the home directory, which is as far as an export may go.
func userFile(path string) (dir, name string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", errors.New("a path is needed")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	path = filepath.Clean(path)
	if path != home && !strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s is outside your home directory", path)
	}
	dir, name = filepath.Split(path)
	if name == "" {
		return "", "", errors.New("the path names a directory, not a file")
	}
	return filepath.Clean(dir), name, nil
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in exportIn
	if !s.decode(w, r, &in) {
		return
	}
	dir, name, err := userFile(in.Path)
	if err != nil {
		s.fail(w, err)
		return
	}
	var text string
	switch strings.ToLower(in.Format) {
	case "", "journal", "hledger":
		text, err = l.Journal(r.Context())
	case "csv":
		text, err = l.CSV(r.Context())
	default:
		err = fmt.Errorf("format %q: journal or csv", in.Format)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	d, err := store.Open(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer d.Close()
	if err := d.Write(name, []byte(text), 0o600); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"path": filepath.Join(dir, name), "bytes": len(text)})
}

// handleBackup writes a consistent copy of the database to the path given.
// The copy is taken into the configuration directory first, at 0600, and
// moved out through the store, so nothing outside is written by SQLite.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	l, ok := s.ledgerOr(w)
	if !ok {
		return
	}
	var in exportIn
	if !s.decode(w, r, &in) {
		return
	}
	dir, name, err := userFile(in.Path)
	if err != nil {
		s.fail(w, err)
		return
	}
	home, err := store.Shared(config.Dir())
	if err != nil {
		s.fail(w, err)
		return
	}
	tmp := fmt.Sprintf("backup-%d.tmp", time.Now().UnixNano())
	if err := home.Write(tmp, nil, 0o600); err != nil {
		s.fail(w, err)
		return
	}
	defer home.Remove(tmp)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := l.DB().SnapshotInto(ctx, filepath.Join(config.Dir(), tmp)); err != nil {
		s.fail(w, err)
		return
	}
	raw, err := home.Read(tmp, 0o600)
	if err != nil {
		s.fail(w, err)
		return
	}
	d, err := store.Open(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer d.Close()
	if err := d.Write(name, raw, 0o600); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]any{"path": filepath.Join(dir, name), "bytes": len(raw)})
}
