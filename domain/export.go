package domain

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/karamble/omarchy-omabudget/money"
)

// Journal renders the ledger as plain-text accounting, one entry per
// transaction with the postings balanced, so the data leaves in a form
// other tools read. Accounts and categories become account paths.
func (l *Ledger) Journal(ctx context.Context) (string, error) {
	accounts, err := l.Accounts(ctx)
	if err != nil {
		return "", err
	}
	index, err := l.CategoryIndex(ctx)
	if err != nil {
		return "", err
	}
	byID := map[string]Account{}
	for _, a := range accounts {
		byID[a.ID] = a
	}
	list, err := l.Transactions(ctx, Filter{})
	if err != nil {
		return "", err
	}
	// Oldest first, as a journal reads.
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "; OMABUDGET journal, base currency %s\n\n", l.base)
	for _, a := range accounts {
		if a.OpeningBalance == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s * Opening balance\n    %s    %s %s\n    equity:opening balances\n\n",
			a.OpeningDate, accountPath(a), money.New(a.OpeningBalance, a.Currency).Format(), a.Currency)
	}
	for _, t := range list {
		desc := t.Description
		if desc == "" {
			desc = index.Name(t.CategoryID)
			if t.Kind == Transfer {
				desc = "Transfer"
			}
		}
		mark := "*"
		if t.Status == Pending {
			mark = "!"
		}
		fmt.Fprintf(&b, "%s %s %s\n", t.Date, mark, desc)
		if len(t.Tags) > 0 {
			fmt.Fprintf(&b, "    ; tags: %s\n", strings.Join(t.Tags, ", "))
		}
		if t.Notes != "" {
			fmt.Fprintf(&b, "    ; %s\n", strings.ReplaceAll(t.Notes, "\n", " "))
		}
		acct := byID[t.AccountID]
		switch t.Kind {
		case Transfer:
			counter := byID[t.CounterAccountID]
			if t.CounterAmount != nil && counter.Currency != acct.Currency {
				fmt.Fprintf(&b, "    %s    %s %s @@ %s %s\n", accountPath(counter),
					money.New(*t.CounterAmount, counter.Currency).Format(), counter.Currency,
					money.New(-t.Amount, acct.Currency).Format(), acct.Currency)
			} else {
				fmt.Fprintf(&b, "    %s    %s %s\n", accountPath(counter), money.New(-t.Amount, acct.Currency).Format(), acct.Currency)
			}
			fmt.Fprintf(&b, "    %s    %s %s\n", accountPath(acct), money.New(t.Amount, acct.Currency).Format(), acct.Currency)
		default:
			if len(t.Splits) > 0 {
				for _, s := range t.Splits {
					fmt.Fprintf(&b, "    %s    %s %s\n", categoryPath(index, s.CategoryID, t.Kind), money.New(-s.Amount, t.Currency).Format(), t.Currency)
				}
			} else {
				fmt.Fprintf(&b, "    %s    %s %s\n", categoryPath(index, t.CategoryID, t.Kind), money.New(-t.Amount, t.Currency).Format(), t.Currency)
			}
			fmt.Fprintf(&b, "    %s    %s %s\n", accountPath(acct), money.New(t.Amount, t.Currency).Format(), t.Currency)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// accountPath is assets:name or liabilities:name.
func accountPath(a Account) string {
	root := "assets"
	if a.Type.Liability() {
		root = "liabilities"
	}
	return root + ":" + slug(a.Name)
}

// categoryPath is expenses:parent:child or income:parent:child.
func categoryPath(index CategoryIndex, id string, kind Kind) string {
	root := "expenses"
	if kind == Income {
		root = "income"
	}
	c, ok := index[id]
	if !ok {
		return root + ":uncategorised"
	}
	if c.ParentID != "" {
		return root + ":" + slug(index[c.ParentID].Name) + ":" + slug(c.Name)
	}
	return root + ":" + slug(c.Name)
}

// slug lowercases and joins words with hyphens; colons would split a path.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// CSV renders every live transaction as rows with a header.
func (l *Ledger) CSV(ctx context.Context) (string, error) {
	accounts, err := l.Accounts(ctx)
	if err != nil {
		return "", err
	}
	names := map[string]string{}
	for _, a := range accounts {
		names[a.ID] = a.Name
	}
	index, err := l.CategoryIndex(ctx)
	if err != nil {
		return "", err
	}
	list, err := l.Transactions(ctx, Filter{})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Write([]string{"id", "date", "kind", "amount", "currency", "baseAmount", "account", "counterAccount", "category", "description", "notes", "status", "tags"})
	for i := len(list) - 1; i >= 0; i-- {
		t := list[i]
		w.Write([]string{
			t.ID, t.Date, string(t.Kind), money.New(t.Amount, t.Currency).Format(), t.Currency,
			money.New(t.BaseAmount, l.base).Format(), names[t.AccountID], names[t.CounterAccountID],
			index.Name(t.CategoryID), t.Description, t.Notes, string(t.Status), strings.Join(t.Tags, ";"),
		})
	}
	w.Flush()
	return b.String(), w.Error()
}
