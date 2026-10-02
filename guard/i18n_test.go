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
var languages = []string{"en", "pt-BR"}

var keyCall = regexp.MustCompile(`I18n\.tf?\(\s*"([^"]+)"`)

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
		for _, m := range keyCall.FindAllStringSubmatch(string(b), -1) {
			if strings.HasSuffix(m[1], ".") {
				prefixes = append(prefixes, m[1])
			} else {
				exact = append(exact, m[1])
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
