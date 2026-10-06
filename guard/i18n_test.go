// Copyright (c) 2015-2026 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/karamble/omarchy-omabudget/db"
)

// The language files are data, not code, so nothing compiles them and nothing
// would notice a key that is referenced and missing, a placeholder that moved,
// or a keyboard shortcut a translator wrote out of a label. These tests are
// what notices.

type langFile struct {
	Locale  string            `json:"locale"`
	Name    string            `json:"name"`
	Strings map[string]string `json:"strings"`
}

func readLang(t *testing.T, tag string) langFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "i18n", tag+".json"))
	if err != nil {
		t.Fatalf("read %s: %v", tag, err)
	}
	var doc langFile
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", tag, err)
	}
	if doc.Locale == "" || doc.Name == "" {
		t.Fatalf("%s: a language file declares its own locale and name", tag)
	}
	return doc
}

// languages are the files that must exist. English is the source of truth.
var languages = []string{"en", "de", "es", "ja", "pt-BR", "ru", "zh-CN"}

var keyCall = regexp.MustCompile(`I18n\.tf?\(`)

// keysIn reads the key literals out of one lookup, starting at its opening
// parenthesis and stopping at the comma that separates the key from the
// values.
//
// It reads the whole first argument rather than the first literal in it,
// because a call may pick between two keys with a conditional, as the rate
// count and the archive toast both do, and both keys have to exist. Matching
// only the literal that follows the parenthesis left those unchecked.
func keysIn(s string) []string {
	var out []string
	var lit strings.Builder
	depth, quote, esc := 0, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote {
			switch {
			case esc:
				lit.WriteByte(c)
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				quote = false
				out = append(out, lit.String())
				lit.Reset()
			default:
				lit.WriteByte(c)
			}
			continue
		}
		switch c {
		case '"':
			quote = true
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return out
			}
		case ',':
			if depth == 1 {
				return out
			}
		}
	}
	return out
}

// seedPrefix is the key family that names the seeded categories. These are not
// UI strings: the daemon reads them to rename database rows, so QML never asks
// for one and the other tests here would not look at them.
const seedPrefix = "seed.category."

// qmlKeys collects every key named by a literal in QML. A key built by
// concatenation shows up as its literal prefix, which is why prefixes are
// matched against the catalogue rather than compared for equality.
func qmlKeys(t *testing.T) (exact, prefixes []string) {
	t.Helper()
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".qml" {
			return err
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(b)
		for _, loc := range keyCall.FindAllStringIndex(text, -1) {
			for _, k := range keysIn(text[loc[1]-1:]) {
				if strings.HasSuffix(k, ".") {
					prefixes = append(prefixes, k)
				} else {
					exact = append(exact, k)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return exact, prefixes
}

// TestEveryKeyUsedInQMLExists fails on a key the UI asks for and English does
// not carry, which renders the key itself on screen.
func TestEveryKeyUsedInQMLExists(t *testing.T) {
	en := readLang(t, "en")
	exact, prefixes := qmlKeys(t)
	var missing []string
	for _, k := range exact {
		if _, ok := en.Strings[k]; !ok {
			missing = append(missing, k)
		}
	}
	for _, p := range prefixes {
		found := false
		for k := range en.Strings {
			if strings.HasPrefix(k, p) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, p+"*")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("keys used in QML with no English string: %v", missing)
	}
}

// TestTranslationsMatchEnglish keeps every other language a subset of English
// with the same placeholders. A missing key is allowed and falls back; a key
// English does not have is a typo, and a moved placeholder loses a value.
func TestTranslationsMatchEnglish(t *testing.T) {
	en := readLang(t, "en")
	arg := regexp.MustCompile(`%(\d)`)
	for _, tag := range languages[1:] {
		doc := readLang(t, tag)
		for k, v := range doc.Strings {
			src, ok := en.Strings[k]
			if !ok {
				t.Errorf("%s: %q is not a key English carries", tag, k)
				continue
			}
			want := append([]string(nil), arg.FindAllString(src, -1)...)
			got := append([]string(nil), arg.FindAllString(v, -1)...)
			sort.Strings(want)
			sort.Strings(got)
			if strings.Join(want, "") != strings.Join(got, "") {
				t.Errorf("%s: %q has placeholders %v, English has %v", tag, k, got, want)
			}
		}
	}
}

// shortcutLetter finds the single lowercase letters a label uses as keyboard
// keys. Only strings that use the run-of-spaces convention carry them, which
// is what keeps an English article like "a few" out of the comparison.
var shortcutLetter = regexp.MustCompile(`(?:^|\s)([a-z])(?:\s|$)`)

// shortcuts reads the standalone single letters out of a legend. Overlapping
// matches are why this scans rather than using FindAllString: in "j k move"
// the space after j is the same space before k, and one pass would miss k.
func shortcuts(s string) []string {
	fields := strings.Fields(s)
	var out []string
	for _, f := range fields {
		if len(f) == 1 && f[0] >= 'a' && f[0] <= 'z' {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// TestTranslationsKeepTheirShortcuts catches a translation that rewrote a
// keyboard legend and dropped the key it names, which silently breaks the
// discoverability of a binding that still works.
func TestTranslationsKeepTheirShortcuts(t *testing.T) {
	en := readLang(t, "en")
	runOfSpaces := regexp.MustCompile(`\s{3,}`)
	for _, tag := range languages[1:] {
		doc := readLang(t, tag)
		for k, v := range doc.Strings {
			src := en.Strings[k]
			if !runOfSpaces.MatchString(src) {
				continue
			}
			// A label whose shortcut was lifted into a placeholder is
			// already covered by the placeholder check, and its prose
			// still contains articles: "Arm a watch   %1" would other-
			// wise read its own "a" as a key the translation dropped.
			if strings.Contains(src, "%") {
				continue
			}
			// Both sides are read the same way. Asking only whether the
			// letter appears anywhere in the translation is no test at
			// all: "r" is inside "remover".
			want := shortcuts(src)
			got := shortcuts(v)
			for _, letter := range want {
				found := false
				for _, g := range got {
					if g == letter {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s: %q drops the shortcut %q (English has %v, this has %v)",
						tag, k, letter, want, got)
				}
			}
		}
	}
}

// TestLanguageFilesAreReachable keeps the catalogue the switcher offers and
// the files on disk from drifting apart: a tag offered with no file behind it
// silently falls back to English with no sign anything is wrong.
func TestLanguageFilesAreReachable(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "i18n", "I18n.qml"))
	if err != nil {
		t.Fatalf("read I18n.qml: %v", err)
	}
	offered := regexp.MustCompile(`\{\s*tag:\s*"([^"]+)"`).FindAllStringSubmatch(string(b), -1)
	if len(offered) == 0 {
		t.Fatal("the singleton offers no languages")
	}
	for _, m := range offered {
		if _, err := os.Stat(filepath.Join("..", "i18n", m[1]+".json")); err != nil {
			t.Errorf("the switcher offers %q with no file behind it", m[1])
		}
	}
	for _, tag := range languages {
		found := false
		for _, m := range offered {
			if m[1] == tag {
				found = true
			}
		}
		if !found {
			t.Errorf("%s.json exists but the switcher does not offer it", tag)
		}
	}
}

// TestSeedCategoryKeysMatchTheSeed holds the language files and the seeded
// taxonomy together, in both directions.
//
// Category names are rows rather than labels, so nothing else would notice a
// category added to the seed with no key to translate it by, or a key left
// behind naming a category the seed no longer plants.
func TestSeedCategoryKeysMatchTheSeed(t *testing.T) {
	en := readLang(t, "en")

	want := map[string]string{}
	for _, c := range db.SeededCategories() {
		want[seedPrefix+c.ID] = c.Name
	}

	var missing, wrong, extra []string
	for k, name := range want {
		switch got, ok := en.Strings[k]; {
		case !ok:
			missing = append(missing, k)
		case got != name:
			wrong = append(wrong, k+": the seed says "+name+", en.json says "+got)
		}
	}
	for k := range en.Strings {
		if strings.HasPrefix(k, seedPrefix) {
			if _, ok := want[k]; !ok {
				extra = append(extra, k)
			}
		}
	}

	for _, s := range [...]struct {
		what string
		list []string
	}{
		{"the seed plants these with no English string", missing},
		{"these disagree with the seed", wrong},
		{"these name a category the seed does not plant", extra},
	} {
		if len(s.list) > 0 {
			sort.Strings(s.list)
			t.Errorf("%s: %v", s.what, s.list)
		}
	}
}

// TestEverySeedCategoryIsTranslated requires every language to name every
// seeded category.
//
// The subset rule in TestTranslationsMatchEnglish is deliberately relaxed for
// this family. Elsewhere a missing key falls back to English and costs one
// label; here it costs a category, and a tree standing half in one language is
// what issue #3 reported in the first place.
func TestEverySeedCategoryIsTranslated(t *testing.T) {
	for _, tag := range languages {
		doc := readLang(t, tag)
		var missing []string
		for _, c := range db.SeededCategories() {
			if v, ok := doc.Strings[seedPrefix+c.ID]; !ok || strings.TrimSpace(v) == "" {
				missing = append(missing, c.ID)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s: %d seeded categories with no name: %v", tag, len(missing), missing)
		}
	}
}

// TestTranslatedSiblingsStayDistinct holds, in every language, the invariant
// UpdateCategory enforces: two categories under one parent must not share a
// name.
//
// The translation pass writes straight to SQL and checks this itself, so a
// language that collapses two categories into one name does not corrupt
// anything. It refuses the entire pass, which on a user's machine looks like
// the feature being broken rather than one string being wrong.
func TestTranslatedSiblingsStayDistinct(t *testing.T) {
	seeded := db.SeededCategories()
	for _, tag := range languages {
		doc := readLang(t, tag)
		seen := map[string]string{}
		for _, c := range seeded {
			name, ok := doc.Strings[seedPrefix+c.ID]
			if !ok {
				continue
			}
			key := c.Parent + "\x00" + strings.ToLower(strings.TrimSpace(name))
			if other, dup := seen[key]; dup {
				t.Errorf("%s: %s and %s are both %q under the same parent", tag, other, c.ID, name)
			}
			seen[key] = c.ID
		}
	}
}
