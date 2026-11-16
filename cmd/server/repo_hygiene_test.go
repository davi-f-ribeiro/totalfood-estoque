package main

import (
	"context"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Checks that keep the repository consistent with its contracts (5S:
// standardize and sustain). Each one fails on the drift it describes.

// Every Go file in this package must stay gofmt-formatted.
func TestGoFilesAreGofmtFormatted(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		formatted, err := format.Source(src)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if string(formatted) != string(src) {
			t.Errorf("%s is not gofmt-formatted; run gofmt -w cmd/server", f)
		}
	}
}

// Migrations are applied by file name, so a repeated number does not break
// anything, but it makes the order ambiguous to read. 033 was duplicated
// before this check existed and cannot be renamed (renaming would re-apply it).
func TestMigrationNumbersAreUnique(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok || len(prefix) != 3 {
			t.Errorf("migration %s must start with a 3-digit number and _", name)
			continue
		}
		seen[prefix] = append(seen[prefix], name)
	}
	for prefix, names := range seen {
		if len(names) > 1 && prefix != "033" {
			sort.Strings(names)
			t.Errorf("migration number %s is used by %v", prefix, names)
		}
	}
}

// registeredRoutes reads the method and path of every mux.HandleFunc in the
// package source, with path parameters normalized to {}.
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	routes := map[string]bool{}
	re := regexp.MustCompile(`HandleFunc\(\s*"([A-Z]+) (/[^"]*)"`)
	for _, src := range nonTestSources(t) {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			routes[m[1]+" "+normalizeRoute(m[2])] = true
		}
	}
	if len(routes) == 0 {
		t.Fatal("no routes found in source")
	}
	return routes
}

func normalizeRoute(p string) string {
	p, _, _ = strings.Cut(p, "?")
	p = strings.ReplaceAll(p, "{$}", "")
	p = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(p, "{}")
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

func nonTestSources(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}

// Every server route a document names in `METHOD /path` form must exist.
// Routes served by the ESP32 firmware (/api/queue/...) are not server routes.
func TestDocumentedRoutesExist(t *testing.T) {
	routes := registeredRoutes(t)
	docs, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	docs = append(docs, filepath.Join("..", "..", "README.md"))
	re := regexp.MustCompile("`((?:GET|POST|PATCH|PUT|DELETE)(?:/(?:GET|POST|PATCH|PUT|DELETE))*) (/[^`\\s]*)`")
	checked := 0
	for _, doc := range docs {
		text, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(text), -1) {
			path := m[2]
			if strings.HasPrefix(path, "/api/queue/") {
				continue
			}
			for _, method := range strings.Split(m[1], "/") {
				checked++
				if !routes[method+" "+normalizeRoute(path)] {
					t.Errorf("%s documents %s %s, but no such route is registered", filepath.Base(doc), method, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no documented routes found; the pattern no longer matches the docs")
	}
}

// Handlers turn some database errors into domain messages by looking for a
// constraint or index name in the error text. Renaming that constraint in a
// migration would silently turn the message into a 500, so every name the
// code looks for must exist in the migrated schema. Phrases with spaces are
// trigger or driver messages, not schema names.
func TestMatchedConstraintNamesExistInSchema(t *testing.T) {
	// Erros do banco são mapeados por violates(err, "nome exato"); um nome
	// digitado errado nunca casaria e a mensagem específica sumiria em silêncio.
	re := regexp.MustCompile(`violates\(err, "([^"]+)"\)`)
	names := map[string]bool{}
	for _, src := range nonTestSources(t) {
		if regexp.MustCompile(`strings\.Contains\((err\.Error\(\)|msg), "[a-z][a-z0-9_]*"\)`).MatchString(src) {
			t.Error("database errors must be matched with violates/raised/isForeignKeyViolation, not by message text")
		}
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			names[m[1]] = true
		}
	}
	if len(names) < 20 {
		t.Fatalf("found only %d names in violates(...); the pattern no longer matches the code", len(names))
	}
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	for name := range names {
		var found bool
		err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname=$1)
  OR EXISTS (SELECT 1 FROM pg_indexes i JOIN pg_class c ON c.relname=i.indexname JOIN pg_index x ON x.indexrelid=c.oid
             WHERE i.indexname=$1 AND x.indisunique)`, name).Scan(&found)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Errorf("code maps database errors by %q, but no constraint or unique index has exactly that name", name)
		}
	}
}
