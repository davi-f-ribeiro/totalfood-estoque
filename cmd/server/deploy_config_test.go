package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every environment variable the app service receives in compose.yaml, and
// every BOOTSTRAP_ variable named in the deploy docs, must be read by the Go
// code. A renamed variable otherwise fails silently: the bootstrap user is never
// created and nothing reports it.
func TestDeployConfigNamesOnlyVariablesTheServerReads(t *testing.T) {
	root := filepath.Join("..", "..")
	source := serverSourceForConfigTest(t)
	reads := func(name string) bool { return strings.Contains(source, `"`+name+`"`) }

	compose, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	appEnv := composeAppEnvironment(t, string(compose))
	if len(appEnv) == 0 {
		t.Fatal("no environment found for the app service in compose.yaml")
	}
	for _, name := range appEnv {
		if !reads(name) {
			t.Errorf("compose.yaml passes %s to the app, but the server never reads it", name)
		}
	}

	bootstrap := regexp.MustCompile(`BOOTSTRAP_[A-Z_]+`)
	for _, doc := range []string{"compose.yaml", "README.md", filepath.Join("docs", "deploy-render-neon.md")} {
		text, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range uniqueSorted(bootstrap.FindAllString(string(text), -1)) {
			if !reads(name) {
				t.Errorf("%s names %s, but the server never reads it", doc, name)
			}
		}
	}
}

func serverSourceForConfigTest(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String()
}

// composeAppEnvironment returns the keys under services.app.environment.
// compose.yaml is small and regular, so indentation is enough to find them.
func composeAppEnvironment(t *testing.T, compose string) []string {
	t.Helper()
	key := regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_]*):`)
	var names []string
	inApp, inEnv := false, false
	envIndent := -1
	for _, line := range strings.Split(compose, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := key.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent := len(m[1])
		switch {
		case indent == 2:
			inApp, inEnv = m[2] == "app", false
		case inApp && indent == 4:
			inEnv = m[2] == "environment"
			envIndent = -1
		case inApp && inEnv:
			if envIndent < 0 {
				envIndent = indent
			}
			if indent == envIndent {
				names = append(names, m[2])
			}
		}
	}
	return names
}

func uniqueSorted(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
