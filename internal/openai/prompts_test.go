package openai

import (
	"strings"
	"testing"
)

func TestRenderTemplate(t *testing.T) {
	r := RenderTemplate("hello {{A}} {{B}}", map[string]string{
		"A": "one",
		"B": "two",
	})
	if r != "hello one two" {
		t.Fatalf("unexpected render result: %s", r)
	}
}

func TestBuildBaseUserPrompt(t *testing.T) {
	prompt := BuildBaseUserPrompt("invoice", "{schema}", "doc text")
	for _, p := range []string{"Document type: invoice", "{schema}", "doc text"} {
		if !strings.Contains(prompt, p) {
			t.Fatalf("prompt missing expected text %q", p)
		}
	}
}
