// Package guard holds the release guard.
//
// The marketplace refuses plugins that ship or expose instructions aimed at
// coding agents, whether installed, printed or embedded in a binary. This test
// walks the whole repository and fails when such content reappears, so a
// release cannot regress into it.
package guard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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

// outbound lists, per package, the symbols that open a connection outward.
// Using net/http for the loopback server is fine; dialling is not. The feed
// package's two entry points count too, so the fetch is reached from one
// place and a second caller is a failure here, not a quiet regression.
var outbound = map[string][]string{
	"net/http":   {"Get", "Head", "Post", "PostForm", "NewRequest", "NewRequestWithContext", "DefaultClient", "DefaultTransport", "Client", "Transport"},
	"net":        {"Dial", "DialTimeout", "DialTCP", "DialUDP", "DialIP", "DialUnix", "Dialer"},
	"crypto/tls": {"Dial", "DialWithDialer", "Dialer"},
	"github.com/karamble/omarchy-omabudget/feed": {"Fetch", "Download"},
}

// outboundFiles are the only files that may use them: the CLI's client,
// which talks to the loopback daemon, the rate fetch, which runs only when
// a person asks, and the server's constructor, which hands the fetch to the
// one handler that calls it.
var outboundFiles = []string{
	"client/client.go",
	"feed/fetch.go",
	"api/server.go",
}

// buildIgnored reports a file the build leaves out, such as a generator run
// by hand, which is not part of the daemon and may read a source directly.
func buildIgnored(t *testing.T, path string) bool {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if line == "//go:build ignore" {
			return true
		}
	}
	return false
}

// walkGo calls fn on every Go file the daemon is built from: not tests, not
// files the build ignores.
func walkGo(t *testing.T, fn func(rel, path string)) {
	t.Helper()
	root := ".."
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || buildIgnored(t, path) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(rel), path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOutboundNetworkIsConfined walks every Go file in the build as syntax,
// resolves import aliases, and fails when a dialling symbol appears outside
// the files allowed to. Each of those must itself be found using one, so a
// renamed file or a detector that stopped matching fails here rather than
// passing quietly.
func TestOutboundNetworkIsConfined(t *testing.T) {
	root := ".."
	hits := map[string][]string{}
	walkGo(t, func(rel, path string) {
		if uses := outboundUses(t, path); len(uses) > 0 {
			hits[rel] = uses
		}
	})

	allowed := map[string]bool{}
	for _, file := range outboundFiles {
		allowed[file] = true
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Errorf("%s is missing: update outboundFiles if it moved", file)
			continue
		}
		if len(hits[file]) == 0 {
			t.Errorf("%s opens no connection: the file or the detector has drifted", file)
		}
	}
	for file, uses := range hits {
		if !allowed[file] {
			t.Errorf("%s reaches outward through %s", file, strings.Join(uses, ", "))
		}
	}
}

// outboundUses parses one file and reports each dialling symbol it names,
// through whatever alias the package was imported under. A dot import of a
// watched package is a hit on its own.
func outboundUses(t *testing.T, file string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	aliases := map[string]string{}
	var uses []string
	for _, imp := range f.Imports {
		pkg := strings.Trim(imp.Path.Value, `"`)
		if _, watched := outbound[pkg]; !watched {
			continue
		}
		name := pkg[strings.LastIndex(pkg, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch name {
		case ".":
			uses = append(uses, "a dot import of "+pkg)
		case "_":
		default:
			aliases[name] = pkg
		}
	}
	if len(aliases) == 0 {
		return uses
	}
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkg, ok := aliases[id.Name]
		if !ok {
			return true
		}
		if slices.Contains(outbound[pkg], sel.Sel.Name) {
			use := id.Name + "." + sel.Sel.Name
			if !seen[use] {
				seen[use] = true
				uses = append(uses, use)
			}
		}
		return true
	})
	sort.Strings(uses)
	return uses
}

// fetchCaller is the one file that may call the server's fetch, and
// feedEntry the one file in the feed package that may build its client or
// read through it.
const (
	fetchCaller = "api/fx.go"
	feedEntry   = "feed/fetch.go"
)

// feedInternals are the feed package's own way to the network, which a new
// file in that package could reach without naming anything the outbound
// test watches.
var feedInternals = []string{"newClient", "fetchWith", "downloadWith"}

// TestFetchHasOneCaller pins the fetch to one call site. The server's fetch
// is a function field so a test can replace it; this is what keeps a
// second caller, on a timer or at start, from appearing without a test
// going red. The feed package's internals are held to their own file the
// same way.
func TestFetchHasOneCaller(t *testing.T) {
	callers := map[string]int{}
	internals := map[string][]string{}
	walkGo(t, func(rel, path string) {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if fn.Sel.Name == "fetch" {
					callers[rel]++
				}
			case *ast.Ident:
				if slices.Contains(feedInternals, fn.Name) && !slices.Contains(internals[rel], fn.Name) {
					internals[rel] = append(internals[rel], fn.Name)
				}
			}
			return true
		})
	})
	if callers[fetchCaller] != 1 {
		t.Errorf("%s calls the fetch %d times, want exactly one", fetchCaller, callers[fetchCaller])
	}
	for rel, n := range callers {
		if rel != fetchCaller {
			t.Errorf("%s calls the fetch %d times: the rate fetch is the only caller", rel, n)
		}
	}
	if len(internals[feedEntry]) == 0 {
		t.Errorf("%s reads through none of %s: the file or the detector has drifted", feedEntry, strings.Join(feedInternals, ", "))
	}
	for rel, names := range internals {
		if rel != feedEntry {
			t.Errorf("%s reaches the network through %s", rel, strings.Join(names, ", "))
		}
	}
}

// mcpFile is where the tools the MCP server offers are registered, and
// pinnedMCPTools is every tool it offers today.
const mcpFile = "../mcpserver/mcpserver.go"

var mcpToolName = regexp.MustCompile(`Name:\s*"(omabudget_[a-z_]+)"`)

var pinnedMCPTools = []string{
	"omabudget_health",
	"omabudget_catalogue",
	"omabudget_alerts",
	"omabudget_disarm",
	"omabudget_arm",
	"omabudget_edit",
	"omabudget_dashboard",
	"omabudget_transactions",
	"omabudget_add_transaction",
	"omabudget_budget",
	"omabudget_bills",
	"omabudget_spending",
}

// TestMCPToolsArePinned fails when the set of tools the MCP server offers
// differs from the pin, so adding one means editing this list in the same
// change. Nothing on that surface may touch the rate table or fetch.
func TestMCPToolsArePinned(t *testing.T) {
	src, err := os.ReadFile(mcpFile)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, m := range mcpToolName.FindAllStringSubmatch(string(src), -1) {
		found[m[1]] = true
	}
	if len(found) == 0 {
		t.Fatalf("%s: found no tools, the registration pattern has drifted", mcpFile)
	}
	pinned := map[string]bool{}
	for _, name := range pinnedMCPTools {
		pinned[name] = true
		if !found[name] {
			t.Errorf("%s no longer offers %s", mcpFile, name)
		}
	}
	for name := range found {
		if !pinned[name] {
			t.Errorf("%s offers %s, which is not pinned", mcpFile, name)
		}
		if strings.Contains(name, "rate") || strings.Contains(name, "fetch") {
			t.Errorf("%s reaches the rate table or the network", name)
		}
	}
}
