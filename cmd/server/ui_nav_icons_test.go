package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The nav draws each icon as a CSS mask of --ico. A link without its own --ico
// rule renders as a solid square, so every nav link needs a rule and the SVG
// it points to must exist.
func TestEveryNavLinkHasAnIcon(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("templates", "base.html"))
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	nav := regexp.MustCompile(`(?s)\{\{define "nav"\}\}(.*?)\{\{end\}\}\{\{define`).FindSubmatch(base)
	if nav == nil {
		t.Fatal("nav template not found in base.html")
	}
	hrefs := regexp.MustCompile(`<a href="(/[^"]*)"`).FindAllSubmatch(nav[1], -1)
	if len(hrefs) == 0 {
		t.Fatal("no nav links found")
	}
	seen := map[string]bool{}
	for _, m := range hrefs {
		href := string(m[1])
		if seen[href] {
			continue
		}
		seen[href] = true
		rule := regexp.MustCompile(`\.nav a\[href="` + regexp.QuoteMeta(href) + `"\]\{--ico:url\("/static/icons/([a-z0-9-]+\.svg)"\)\}`).FindSubmatch(css)
		if rule == nil {
			t.Errorf("nav link %s has no --ico rule in app.css", href)
			continue
		}
		if _, err := os.Stat(filepath.Join("static", "icons", string(rule[1]))); err != nil {
			t.Errorf("nav link %s points to missing icon %s", href, rule[1])
		}
	}
}
