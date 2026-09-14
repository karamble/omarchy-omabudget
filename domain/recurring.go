package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/karamble/omarchy-omabudget/db"
)

// Frequencies a rule can run on, spec 1.8.
var frequencies = map[string]bool{
	"daily": true, "weekly": true, "biweekly": true, "monthly": true,
	"quarterly": true, "semiannual": true, "annual": true, "custom": true,
}

// Template is the transaction a rule posts. Amount is a magnitude; the sign
// follows the kind when an instance is written.
type Template struct {
	Kind             Kind     `json:"kind"`
	AccountID        string   `json:"accountId"`
	CounterAccountID string   `json:"counterAccountId,omitempty"`
	Amount           int64    `json:"amount"`
	Currency         string   `json:"currency"`
	CategoryID       string   `json:"categoryId,omitempty"`
	Description      string   `json:"description,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

// Rule is a recurring obligation: a template and a schedule. Instances are
// ordinary transactions carrying the rule's id. NextDue is the occurrence
// waiting to be posted or skipped; it can lie in the past.
type Rule struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Template        Template `json:"template"`
	Frequency       string   `json:"frequency"`
	IntervalDays    int      `json:"intervalDays,omitempty"`
	DayRule         string   `json:"dayRule,omitempty"` // "" or "last"
	StartDate       string   `json:"startDate"`
	EndDate         string   `json:"endDate,omitempty"`
	OccurrenceCount int      `json:"occurrenceCount,omitempty"`
	AutoPost        bool     `json:"autoPost"`
	LeadDays        int      `json:"leadDays"`
	VariableAmount  bool     `json:"variableAmount"`
	Active          bool     `json:"active"`
	LastPosted      string   `json:"lastPosted,omitempty"`
	NextDue         string   `json:"nextDue,omitempty"`
	Posted          int      `json:"posted"`
}

// Due is one occurrence in a window: overdue, or coming up.
type Due struct {
	RuleID           string `json:"ruleId"`
	Name             string `json:"name"`
	Date             string `json:"date"`
	Kind             Kind   `json:"kind"`
	Amount           int64  `json:"amount"`
	Currency         string `json:"currency"`
	AccountID        string `json:"accountId"`
	CounterAccountID string `json:"counterAccountId,omitempty"`
	CategoryID       string `json:"categoryId,omitempty"`
	AutoPost         bool   `json:"autoPost"`
	VariableAmount   bool   `json:"variableAmount"`
	Overdue          bool   `json:"overdue"`
	DaysUntil        int    `json:"daysUntil"`
}

// checkRule validates a rule and resolves the references in its template
// the way a transaction would be resolved.
func (l *Ledger) checkRule(ctx context.Context, r *Rule) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errors.New("a rule needs a name")
	}
	r.Frequency = strings.ToLower(strings.TrimSpace(r.Frequency))
	if !frequencies[r.Frequency] {
		return fmt.Errorf("unknown frequency %q", r.Frequency)
	}
	if r.Frequency == "custom" {
		if r.IntervalDays < 1 {
			return errors.New("a custom frequency needs the interval in days")
		}
	} else {
		r.IntervalDays = 0
	}
	r.DayRule = strings.ToLower(strings.TrimSpace(r.DayRule))
	if r.DayRule != "" && r.DayRule != "last" {
		return fmt.Errorf("day rule %q: only \"last\" is known", r.DayRule)
	}
	if _, err := time.Parse(dateFmt, r.StartDate); err != nil {
		return fmt.Errorf("start date %q must be YYYY-MM-DD", r.StartDate)
	}
	if r.EndDate != "" {
		if _, err := time.Parse(dateFmt, r.EndDate); err != nil {
			return fmt.Errorf("end date %q must be YYYY-MM-DD", r.EndDate)
		}
		if r.EndDate < r.StartDate {
			return errors.New("the end date is before the start date")
		}
	}
	if r.OccurrenceCount < 0 || r.LeadDays < 0 {
		return errors.New("counts and lead days cannot be negative")
	}
	if r.Template.Amount < 0 {
		r.Template.Amount = -r.Template.Amount
	}
	if r.Template.Amount == 0 {
		return errors.New("the amount is zero")
	}
	probe := Transaction{
		Kind: r.Template.Kind, AccountID: r.Template.AccountID, CounterAccountID: r.Template.CounterAccountID,
		Amount: r.Template.Amount, Currency: r.Template.Currency, CategoryID: r.Template.CategoryID,
		Date: r.StartDate, FXRate: "1",
	}
	if err := l.prepare(ctx, &probe); err != nil {
		return err
	}
	r.Template.AccountID = probe.AccountID
	r.Template.CounterAccountID = probe.CounterAccountID
	r.Template.Currency = probe.Currency
	r.Template.CategoryID = probe.CategoryID
	return nil
}

func (l *Ledger) AddRule(ctx context.Context, r Rule) (Rule, error) {
	if err := l.checkRule(ctx, &r); err != nil {
		return Rule{}, err
	}
	if r.ID == "" {
		r.ID = newID()
	}
	r.Active = true
	r.NextDue = r.StartDate
	r.LastPosted = ""
	r.Posted = 0
	tpl, err := json.Marshal(r.Template)
	if err != nil {
		return Rule{}, err
	}
	_, err = l.db.ExecContext(ctx, `INSERT INTO recurring_rules
		(id,name,template,frequency,interval_days,day_rule,start_date,end_date,occurrence_count,
		 auto_post,lead_days,variable_amount,is_active,last_posted,next_due)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,1,NULL,?)`,
		r.ID, r.Name, string(tpl), r.Frequency, nullInt(r.IntervalDays), r.DayRule, r.StartDate,
		nullIf(r.EndDate), nullInt(r.OccurrenceCount), boolInt(r.AutoPost), r.LeadDays,
		boolInt(r.VariableAmount), r.NextDue)
	if err != nil {
		return Rule{}, err
	}
	return r, nil
}

const ruleColumns = `r.id,r.name,r.template,r.frequency,COALESCE(r.interval_days,0),r.day_rule,r.start_date,
	COALESCE(r.end_date,''),COALESCE(r.occurrence_count,0),r.auto_post,r.lead_days,r.variable_amount,
	r.is_active,COALESCE(r.last_posted,''),COALESCE(r.next_due,''),
	(SELECT COUNT(*) FROM transactions t WHERE t.recurring_rule_id=r.id AND t.deleted_at IS NULL)`

func scanRule(rows interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var tpl string
	var auto, variable, active int
	if err := rows.Scan(&r.ID, &r.Name, &tpl, &r.Frequency, &r.IntervalDays, &r.DayRule, &r.StartDate,
		&r.EndDate, &r.OccurrenceCount, &auto, &r.LeadDays, &variable, &active, &r.LastPosted, &r.NextDue,
		&r.Posted); err != nil {
		return Rule{}, err
	}
	if err := json.Unmarshal([]byte(tpl), &r.Template); err != nil {
		return Rule{}, fmt.Errorf("rule %s: %w", r.ID, err)
	}
	r.AutoPost, r.VariableAmount, r.Active = auto == 1, variable == 1, active == 1
	return r, nil
}

// Rules lists the live rules, the paused ones too when all is set, soonest
// due first.
func (l *Ledger) Rules(ctx context.Context, all bool) ([]Rule, error) {
	q := `SELECT ` + ruleColumns + ` FROM recurring_rules r WHERE r.deleted_at IS NULL`
	if !all {
		q += ` AND r.is_active=1`
	}
	q += ` ORDER BY r.is_active DESC, r.next_due, r.name`
	rows, err := l.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (l *Ledger) Rule(ctx context.Context, id string) (Rule, error) {
	row := l.db.QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM recurring_rules r WHERE r.id=? AND r.deleted_at IS NULL`, id)
	r, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, fmt.Errorf("rule %q: %w", id, db.ErrNotFound)
	}
	return r, err
}

// UpdateRule rewrites a rule. What was posted stays; the next occurrence is
// kept unless it now falls before the start date.
func (l *Ledger) UpdateRule(ctx context.Context, r Rule) (Rule, error) {
	cur, err := l.Rule(ctx, r.ID)
	if err != nil {
		return Rule{}, err
	}
	if err := l.checkRule(ctx, &r); err != nil {
		return Rule{}, err
	}
	r.LastPosted = cur.LastPosted
	r.Posted = cur.Posted
	if r.NextDue == "" {
		r.NextDue = cur.NextDue
	}
	if r.NextDue == "" || r.NextDue < r.StartDate {
		r.NextDue = r.StartDate
	}
	return r, l.writeRule(ctx, r)
}

func (l *Ledger) writeRule(ctx context.Context, r Rule) error {
	tpl, err := json.Marshal(r.Template)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx, `UPDATE recurring_rules SET name=?,template=?,frequency=?,interval_days=?,
		day_rule=?,start_date=?,end_date=?,occurrence_count=?,auto_post=?,lead_days=?,variable_amount=?,
		is_active=?,last_posted=?,next_due=? WHERE id=? AND deleted_at IS NULL`,
		r.Name, string(tpl), r.Frequency, nullInt(r.IntervalDays), r.DayRule, r.StartDate, nullIf(r.EndDate),
		nullInt(r.OccurrenceCount), boolInt(r.AutoPost), r.LeadDays, boolInt(r.VariableAmount),
		boolInt(r.Active), nullIf(r.LastPosted), nullIf(r.NextDue), r.ID)
	return err
}

// RemoveRule soft-deletes a rule. Instances already posted stay.
func (l *Ledger) RemoveRule(ctx context.Context, id string) error {
	res, err := l.db.ExecContext(ctx, `UPDATE recurring_rules SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, l.stamp(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("rule %q: %w", id, db.ErrNotFound)
	}
	return nil
}

// nextDate is the occurrence after from. Month-based frequencies keep the
// start date's day of month, clamped to shorter months, or the last day
// when the day rule says so.
func nextDate(from string, r Rule) (string, error) {
	d, err := time.Parse(dateFmt, from)
	if err != nil {
		return "", err
	}
	start, err := time.Parse(dateFmt, r.StartDate)
	if err != nil {
		return "", err
	}
	switch r.Frequency {
	case "daily":
		d = d.AddDate(0, 0, 1)
	case "weekly":
		d = d.AddDate(0, 0, 7)
	case "biweekly":
		d = d.AddDate(0, 0, 14)
	case "custom":
		d = d.AddDate(0, 0, r.IntervalDays)
	case "monthly":
		d = addMonths(d, 1, start.Day(), r.DayRule == "last")
	case "quarterly":
		d = addMonths(d, 3, start.Day(), r.DayRule == "last")
	case "semiannual":
		d = addMonths(d, 6, start.Day(), r.DayRule == "last")
	case "annual":
		d = addMonths(d, 12, start.Day(), r.DayRule == "last")
	default:
		return "", fmt.Errorf("unknown frequency %q", r.Frequency)
	}
	return d.Format(dateFmt), nil
}

func addMonths(d time.Time, n int, anchorDay int, last bool) time.Time {
	first := time.Date(d.Year(), d.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	days := first.AddDate(0, 1, -1).Day()
	day := anchorDay
	if last || day > days {
		day = days
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

// advance moves a rule to its next occurrence and retires it when the
// schedule is spent.
func advance(r *Rule) error {
	if r.NextDue == "" {
		r.Active = false
		return nil
	}
	next, err := nextDate(r.NextDue, *r)
	if err != nil {
		return err
	}
	r.NextDue = next
	if r.EndDate != "" && r.NextDue > r.EndDate {
		r.Active = false
	}
	if r.OccurrenceCount > 0 && r.Posted >= r.OccurrenceCount {
		r.Active = false
	}
	return nil
}

// Post writes the occurrence as a transaction dated date (the due date when
// empty) for amount (the template's when zero), then moves the rule on.
func (l *Ledger) Post(ctx context.Context, id, date string, amount int64) (Transaction, error) {
	r, err := l.Rule(ctx, id)
	if err != nil {
		return Transaction{}, err
	}
	if !r.Active {
		return Transaction{}, fmt.Errorf("%s is not active", r.Name)
	}
	if date == "" {
		date = r.NextDue
	}
	if amount == 0 {
		amount = r.Template.Amount
	}
	desc := r.Template.Description
	if desc == "" {
		desc = r.Name
	}
	t := Transaction{
		Kind: r.Template.Kind, AccountID: r.Template.AccountID, CounterAccountID: r.Template.CounterAccountID,
		Amount: amount, Currency: r.Template.Currency, CategoryID: r.Template.CategoryID,
		Description: desc, Tags: r.Template.Tags, Date: date,
		RecurringRuleID: r.ID, IsRecurringInstance: true,
	}
	out, err := l.Add(ctx, t)
	if err != nil {
		return Transaction{}, err
	}
	r.LastPosted = date
	r.Posted++
	if err := advance(&r); err != nil {
		return Transaction{}, err
	}
	return out, l.writeRule(ctx, r)
}

// Skip moves a rule past its next occurrence without posting it.
func (l *Ledger) Skip(ctx context.Context, id string) (Rule, error) {
	r, err := l.Rule(ctx, id)
	if err != nil {
		return Rule{}, err
	}
	if !r.Active {
		return Rule{}, fmt.Errorf("%s is not active", r.Name)
	}
	if err := advance(&r); err != nil {
		return Rule{}, err
	}
	return r, l.writeRule(ctx, r)
}

// Upcoming lists every occurrence due on or before until, overdue ones
// first, from the active rules.
func (l *Ledger) Upcoming(ctx context.Context, today, until string) ([]Due, error) {
	rules, err := l.Rules(ctx, false)
	if err != nil {
		return nil, err
	}
	now, err := time.Parse(dateFmt, today)
	if err != nil {
		return nil, err
	}
	var out []Due
	for _, r := range rules {
		d := r.NextDue
		posted := r.Posted
		for n := 0; d != "" && d <= until && n < 64; n++ {
			if r.EndDate != "" && d > r.EndDate {
				break
			}
			if r.OccurrenceCount > 0 && posted >= r.OccurrenceCount {
				break
			}
			due, _ := time.Parse(dateFmt, d)
			days := int(due.Sub(now).Hours() / 24)
			out = append(out, Due{
				RuleID: r.ID, Name: r.Name, Date: d, Kind: r.Template.Kind, Amount: r.Template.Amount,
				Currency: r.Template.Currency, AccountID: r.Template.AccountID,
				CounterAccountID: r.Template.CounterAccountID, CategoryID: r.Template.CategoryID,
				AutoPost: r.AutoPost, VariableAmount: r.VariableAmount, Overdue: d < today, DaysUntil: days,
			})
			posted++
			if d, err = nextDate(d, r); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Name < out[j].Name
	})
	if out == nil {
		out = []Due{}
	}
	return out, nil
}

// PostDue posts every occurrence of the auto-posting rules that is due on
// or before today, and reports how many it wrote.
func (l *Ledger) PostDue(ctx context.Context, today string) (int, error) {
	rules, err := l.Rules(ctx, false)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rules {
		if !r.AutoPost {
			continue
		}
		for guard := 0; guard < 400; guard++ {
			cur, err := l.Rule(ctx, r.ID)
			if err != nil {
				return n, err
			}
			if !cur.Active || cur.NextDue == "" || cur.NextDue > today {
				break
			}
			if _, err := l.Post(ctx, cur.ID, cur.NextDue, 0); err != nil {
				return n, fmt.Errorf("posting %s: %w", cur.Name, err)
			}
			n++
		}
	}
	return n, nil
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
