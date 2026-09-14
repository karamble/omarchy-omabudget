package alerts

import (
	"fmt"
	"sort"
	"strings"
)

// CatalogueMarkdown renders Catalogue() for a terminal, so the printed list
// cannot name a path the daemon does not have.
func CatalogueMarkdown() string {
	leaves := Catalogue()

	groups := map[string][]Leaf{}
	var order []string
	for _, l := range leaves {
		name := l.Path
		if i := strings.Index(name, "."); i > 0 {
			name = name[:i]
		}
		if _, seen := groups[name]; !seen {
			order = append(order, name)
		}
		groups[name] = append(groups[name], l)
	}
	sort.Strings(order)

	var b strings.Builder
	fmt.Fprintf(&b, "%d paths. A number takes `crosses`. Text and bool take `becomes`. "+
		"A list takes `appears`, `disappears`, `count` and, where it carries a timestamp, "+
		"`ages`; it filters with `--where` on the fields shown, and two entries are the "+
		"same entry when their identity fields match.\n", len(leaves))

	for _, name := range order {
		fmt.Fprintf(&b, "\n### %s\n", name)
		for _, l := range groups[name] {
			ops := make([]string, 0, len(l.Operators))
			for _, op := range l.Operators {
				ops = append(ops, string(op))
			}
			fmt.Fprintf(&b, "- `%s` %s: %s. %s.", l.Path, l.Kind,
				strings.Join(ops, ", "), capitalise(l.Describes))
			if len(l.Fields) > 0 {
				fmt.Fprintf(&b, " Fields: %s.", strings.Join(l.Fields, ", "))
			}
			if len(l.Identity) > 0 {
				fmt.Fprintf(&b, " Identity: %s.", strings.Join(l.Identity, ", "))
			}
			if len(l.TimeFields) > 0 {
				fmt.Fprintf(&b, " Time fields: %s.", strings.Join(l.TimeFields, ", "))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
