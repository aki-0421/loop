package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFallbackPRTextUsesJapaneseTemplateLanguage(t *testing.T) {
	root := t.TempDir()
	templatePath := filepath.Join(root, ".github", "PULL_REQUEST_TEMPLATE.md")
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	template := "## 概要\n\n## 確認\n\n- [ ] テスト済み\n"
	if err := os.WriteFile(templatePath, []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	gotTemplate := readPullRequestTemplate(root)
	title := fallbackPRTitle("feat/jp-weather", gotTemplate)
	body := fallbackPRBody("feat/jp-weather", gotTemplate)

	if title != "feat/jp-weather を統合" {
		t.Fatalf("title = %q", title)
	}
	for _, want := range []string{"## 概要", "## 確認", "loop による補足", "対象ブランチ"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestFallbackPRTextDefaultsToEnglishWithoutTemplate(t *testing.T) {
	title := fallbackPRTitle("feat/app-shell", "")
	body := fallbackPRBody("feat/app-shell", "")

	if title != "Integrate feat/app-shell" {
		t.Fatalf("title = %q", title)
	}
	for _, want := range []string{"## Summary", "## Verification"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}
