package db

import (
	"context"
	"database/sql"
	"strings"
)

// seedGroup is one top-level category and its children, spec section 2.
type seedGroup struct {
	name     string
	kind     string
	icon     string
	children []seedChild
}

// seedChild is a second-level category. rollover marks [R] in the spec; the
// [V] variable-amount marker is a recurring-rule property, not a category one.
type seedChild struct {
	name     string
	rollover bool
}

// Icons are Nerd Font glyphs written as real UTF-8. Never as escapes, which
// render blank.
var seedGroups = []seedGroup{
	{"Salary", "income", "󰄔", []seedChild{{"Primary salary", false}, {"Secondary salary", false}, {"Bonus", false}, {"Overtime", false}}},
	{"Self-employment", "income", "󰃖", []seedChild{{"Client invoices", false}, {"Royalties", false}}},
	{"Benefits", "income", "󰃭", []seedChild{{"Child benefit", false}, {"Social benefits", false}, {"Pension", false}, {"Sick pay", false}}},
	{"Investment income", "income", "󰄨", []seedChild{{"Interest", false}, {"Dividends", false}, {"Capital gains", false}, {"Rental income", false}}},
	{"Other income", "income", "󰆧", []seedChild{{"Gifts received", false}, {"Refunds & rebates", false}, {"Sale of used goods", false}, {"Tax refund", false}, {"Reimbursements", false}}},

	{"Housing", "expense", "󰋜", []seedChild{
		{"Rent", false}, {"Mortgage payment", false}, {"Property tax", false},
		{"Building fee", false}, {"Home insurance", false},
		{"Repairs & maintenance", true}, {"Renovation", true},
		{"Furniture & furnishings", false}, {"Garden & outdoor", false}}},
	{"Utilities", "expense", "󱐋", []seedChild{
		{"Electricity", false}, {"Gas", false}, {"Heating", false}, {"Water & sewage", false},
		{"Waste collection", false}, {"Internet", false}, {"Mobile phone", false}, {"TV / streaming bundle", false}}},
	{"Food", "expense", "󰄛", []seedChild{
		{"Groceries", false}, {"Household chemicals & hygiene", false}, {"Restaurants & takeaway", false},
		{"Coffee & snacks", false}, {"Work lunches", false}, {"Alcohol & tobacco", false}}},
	{"Transport", "expense", "󰄋", []seedChild{
		{"Fuel", false}, {"Public transport", false}, {"Vehicle insurance", true},
		{"Vehicle service & repairs", true}, {"Vehicle tax / inspection", false}, {"Parking & tolls", false},
		{"Taxi / rideshare", false}, {"Vehicle purchase / leasing", false}, {"Bicycle", false}}},
	{"Health", "expense", "󰋠", []seedChild{
		{"Doctor visits", false}, {"Medication & pharmacy", false}, {"Dental", false},
		{"Health insurance", false}, {"Optics", false}, {"Therapy", false}}},
	{"Personal", "expense", "󰀄", []seedChild{
		{"Clothing & footwear", false}, {"Hairdresser & beauty", false}, {"Sport & fitness", false},
		{"Hobbies", false}, {"Books & media", false}, {"Electronics & gadgets", false},
		{"Gifts given", false}, {"Charity & donations", false}}},
	{"Family & Education", "expense", "󰠖", []seedChild{
		{"Childcare / nursery", false}, {"School fees & supplies", false}, {"Tutoring & extracurricular", false},
		{"Children's clothing", false}, {"Toys", false}, {"Pocket money", false}, {"Courses & certification", false}}},
	{"Subscriptions & Services", "expense", "󰑐", []seedChild{
		{"Streaming", false}, {"Software & cloud", false}, {"Domains & hosting", false},
		{"Memberships", false}, {"News & publications", false}, {"Cloud storage / backup", false}}},
	// A household with an animal in it books more than a vet bill, and every
	// one of those would otherwise land in Health or Personal.
	{"Pets", "expense", "󰩃", []seedChild{
		{"Pet food", false}, {"Veterinary", true}, {"Medication & supplements", false},
		{"Pet insurance", false}, {"Grooming", false}, {"Toys & accessories", false},
		{"Boarding & sitting", true}, {"Training", false}}},
	{"Travel", "expense", "󰀝", []seedChild{
		{"Accommodation", false}, {"Transport (flights/rail)", false}, {"Food while travelling", false},
		{"Activities & tickets", false}, {"Travel insurance", false}, {"Roaming & connectivity", false},
		// The road half of a trip: the line above is flights and rail, and
		// Transport's own fuel is everyday motoring rather than travelling.
		{"Fuel & charging", false}}},
	{"Financial", "expense", "󰆟", []seedChild{
		{"Bank fees & commissions", false}, {"Card fees", false}, {"FX spread / conversion cost", false},
		{"Loan interest", false}, {"Loan principal repayment", false}, {"Credit card interest", false},
		{"Fines & penalties", false}, {"Tax payments", false}, {"Legal & accounting", false}}},
	// Savings are normally transfers; these exist for envelope users and are
	// excluded from statistics to avoid double counting, spec 2.12.
	{"Savings & Investments", "expense", "󰆘", []seedChild{
		{"Emergency fund", false}, {"Retirement contribution", false},
		{"Investment purchase", false}, {"Goal contribution", false}}},
}

// systemCategories cannot be deleted, spec 2.13. Transfer is a marker the UI
// never offers.
var systemCategories = []struct{ id, name, kind string }{
	{"sys-uncategorised", "Uncategorised", "expense"},
	{"sys-opening-balance", "Opening balance", "income"},
	{"sys-balance-adjustment", "Balance adjustment", "expense"},
	{"sys-transfer", "Transfer", "expense"},
}

// slug makes a stable id from a name, so seeding is idempotent across runs and
// a user's renames survive: the id never changes, only the name column does.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
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

// seedCategories loads the default taxonomy. INSERT OR IGNORE, keyed on the
// slug id, so running it again adds nothing and changes nothing.
func seedCategories(ctx context.Context, tx *sql.Tx) error {
	ins, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO categories
		(id, name, parent_id, kind, icon, is_system, default_budget_behaviour, exclude_from_statistics, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()

	for i, s := range systemCategories {
		if _, err := ins.ExecContext(ctx, s.id, s.name, nil, s.kind, "", 1, "untracked", 0, i); err != nil {
			return err
		}
	}

	order := 0
	for _, g := range seedGroups {
		order++
		gid := slug(g.name)
		excluded := 0
		if g.name == "Savings & Investments" {
			excluded = 1
		}
		if _, err := ins.ExecContext(ctx, gid, g.name, nil, g.kind, g.icon, 0, "monthly", excluded, order); err != nil {
			return err
		}
		for j, c := range g.children {
			behaviour := "monthly"
			if c.rollover {
				behaviour = "rollover"
			}
			cid := gid + "/" + slug(c.name)
			if _, err := ins.ExecContext(ctx, cid, c.name, gid, g.kind, "", 0, behaviour, excluded, j); err != nil {
				return err
			}
		}
	}
	return nil
}
