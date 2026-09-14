// Package guard holds the release guard.
//
// The marketplace refuses plugins that ship or expose instructions aimed at
// coding agents, whether installed, printed or embedded in a binary. This test
// walks the whole repository and fails when such content reappears, so a
// release cannot regress into it.
package guard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// skipDirs are not part of a release.
var skipDirs = map[string]bool{
	".git": true,
	"bin":  true,
}

// forbiddenNames are filenames that are an instruction channel by convention,
// whatever they contain.
var forbiddenNames = []string{
	"agents.md",
	"skill.md",
	"claude.md",
	"agent.md",
}

// forbiddenDirs are the roots a plugin must never write to or vendor.
var forbiddenDirs = []string{
	".claude",
	".agents",
	"skills",
}

// directive matches prose addressed to an agent rather than to a person.
//
// The phrases are assembled from fragments so this file does not match itself.
var directive = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`for cod` + `ing agents`,
	`you are an ` + `agent`,
	`before ` + `acting`,
	`the ` + `agent must`,
	`instructions for ` + `agents`,
	`agent-` + `facing guide`,
	// UI copy counts too: a button tooltip reading "the guide an agent reads"
	// shipped in QML through the first version of this guard.
	`guide an ` + `agent`,
	`an ` + `agent reads`,
	`print the ` + `guide`,
}, "|"))

// embedMarkdown catches a Go file embedding markdown into a binary, which is
// how a printed guide survives a deleted file.
var embedMarkdown = regexp.MustCompile(`go:embed\s+\S*\.md`)

// frontMatter catches a leading YAML block with a name and description, which
// is the shape that makes a markdown file auto-load as a skill.
var frontMatter = regexp.MustCompile(`(?s)\A---\r?\n.*?\bname:.*?\bdescription:.*?\r?\n---`)

func TestNoAgentInstructionSurface(t *testing.T) {
	root := ".."
	self, err := filepath.Abs("guard_test.go")
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			for _, bad := range forbiddenDirs {
				if strings.EqualFold(d.Name(), bad) {
					t.Errorf("%s: a plugin must not ship a %s directory", path, bad)
					return filepath.SkipDir
				}
			}
			return nil
		}

		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if abs == self {
			return nil
		}

		name := strings.ToLower(d.Name())
		for _, bad := range forbiddenNames {
			if name == bad {
				t.Errorf("%s: %s is an agent instruction channel", path, bad)
			}
		}

		switch filepath.Ext(name) {
		case ".md", ".go", ".qml", ".json", ".txt":
		default:
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)

		if m := directive.FindString(text); m != "" {
			t.Errorf("%s: reads as an instruction to an agent (%q)", path, m)
		}
		if m := embedMarkdown.FindString(text); m != "" {
			t.Errorf("%s: embeds markdown into a binary (%q)", path, m)
		}
		if frontMatter.MatchString(text) {
			t.Errorf("%s: has skill front matter", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// helperName is the binary the QML shells out to, and dispatchFile is where its
// verbs are defined.
const (
	helperName   = "omabudget"
	dispatchFile = "../cmd/omabudget/main.go"
)

var (
	caseVerbs  = regexp.MustCompile(`case\s+((?:"[a-z][a-z0-9-]*"\s*,?\s*)+):`)
	quotedWord = regexp.MustCompile(`"([a-z][a-z0-9-]*)"`)
	// The "./" prefix is what distinguishes an invocation from prose naming the
	// binary in a comment.
	shellCall = regexp.MustCompile(`\./bin/` + helperName + `\s+([a-z][a-z0-9-]*)`)
	argvCall  = regexp.MustCompile(`helperPath\s*,\s*"([a-z][a-z0-9-]*)"`)
	comment   = regexp.MustCompile(`(?m)^\s*//.*$`)
)

// TestQMLVerbsExist fails when the panel invokes a command the binary does not
// implement.
//
// Deleting a verb and leaving the button that calls it is silent: the panel
// still builds, still lints, and the failure only appears as an error inside a
// terminal the user opened. Removing the agent guide left exactly that behind.
func TestQMLVerbsExist(t *testing.T) {
	src, err := os.ReadFile(dispatchFile)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, m := range caseVerbs.FindAllStringSubmatch(string(src), -1) {
		for _, w := range quotedWord.FindAllStringSubmatch(m[1], -1) {
			known[w[1]] = true
		}
	}
	if len(known) == 0 {
		t.Fatalf("%s: found no verbs, the dispatch pattern has drifted", dispatchFile)
	}

	entries, err := filepath.Glob("../*.qml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := comment.ReplaceAllString(string(body), "")
		for _, re := range []*regexp.Regexp{shellCall, argvCall} {
			for _, m := range re.FindAllStringSubmatch(text, -1) {
				if !known[m[1]] {
					t.Errorf("%s: calls %q, which %s does not implement",
						path, m[1], helperName)
				}
			}
		}
	}
}

var (
	manifestVersion = regexp.MustCompile(`"version"\s*:\s*"([^"]+)"`)
	makefileVersion = regexp.MustCompile(`(?m)^VERSION\s*\?=\s*(\S+)`)
)

// TestVersionsAgree keeps the three places a version lives from drifting.
//
// The marketplace shows the manifest's version, the Makefile stamps the
// binary's, and a release tag names both. Nothing else checks they match, and a
// listing claiming one version while the binary reports another is the kind of
// thing nobody notices until someone is comparing them for a reason.
//
// Neither file is read through git, so this works in a fresh clone and offline.
func TestVersionsAgree(t *testing.T) {
	manifest, err := os.ReadFile("../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m := manifestVersion.FindSubmatch(manifest)
	if m == nil {
		t.Fatal("manifest.json has no version field")
	}

	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	mk := makefileVersion.FindSubmatch(makefile)
	if mk == nil {
		t.Fatal("the Makefile has no VERSION")
	}

	if string(m[1]) != string(mk[1]) {
		t.Errorf("manifest.json says %q, the Makefile says %q", m[1], mk[1])
	}
}
