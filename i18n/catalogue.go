// Package i18n reads the language files that ship beside it.
//
// The files are read twice over. QML loads them from disk to translate the
// interface, which is the whole of what they were written for. This package
// embeds them so the daemon can read one family of keys out of the same files:
// the names of the categories the seed plants, which are database rows rather
// than labels and so have to be translated by renaming them.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Embedded rather than read from disk, because a Go process here cannot find
// the plugin directory: the daemon is launched with a cleared environment and
// no argument naming its own path, and `make install` does not copy this
// directory at all. The cost is that a language file edited by hand is live
// for the interface and stale for the daemon until the plugin is rebuilt.
//
//go:embed *.json
var files embed.FS

// SeedPrefix is the key family naming the categories the seed plants.
// Everything else in a language file is interface text that never reaches Go.
const SeedPrefix = "seed.category."

type langFile struct {
	Locale  string            `json:"locale"`
	Name    string            `json:"name"`
	Strings map[string]string `json:"strings"`
}

func read(tag string) (langFile, error) {
	b, err := files.ReadFile(tag + ".json")
	if err != nil {
		return langFile{}, fmt.Errorf("language %q: %w", tag, err)
	}
	var doc langFile
	if err := json.Unmarshal(b, &doc); err != nil {
		return langFile{}, fmt.Errorf("language %q: %w", tag, err)
	}
	return doc, nil
}

// Tags lists the shipped languages, sorted. It is derived from the files that
// were embedded rather than from a list beside them, so the two cannot drift.
func Tags() []string {
	entries, err := fs.Glob(files, "*.json")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(path.Base(e), ".json"))
	}
	sort.Strings(out)
	return out
}

// SeedNames gives the seeded category names in one language, keyed by category
// id. An unknown tag is an error rather than an empty result: renaming every
// category to nothing is not a reasonable reading of a typo.
func SeedNames(tag string) (map[string]string, error) {
	doc, err := read(tag)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(doc.Strings))
	for k, v := range doc.Strings {
		if id, ok := strings.CutPrefix(k, SeedPrefix); ok {
			out[id] = v
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("language %q carries no category names", tag)
	}
	return out, nil
}

// AllSeedNames gives every name a category id carries in any shipped language,
// keyed by id.
//
// This is what lets the translation pass tell a name it planted from one the
// user typed: a row still carrying one of these is still the seeded category,
// whichever language it was last translated into, and anything else is the
// user's own name and is left alone. It is also what makes the pass reversible,
// since the way back to English is the same comparison.
func AllSeedNames() map[string][]string {
	out := map[string][]string{}
	for _, tag := range Tags() {
		names, err := SeedNames(tag)
		if err != nil {
			continue
		}
		for id, name := range names {
			if !slicesContainsFold(out[id], name) {
				out[id] = append(out[id], name)
			}
		}
	}
	return out
}

func slicesContainsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}
