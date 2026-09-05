package services

import (
	"os"
	"testing"
)

func TestHTMLMaskingAndCollapsing(t *testing.T) {
	htmlInput := `<div class="lightbox-article">
    <div class="article-header">
        <h2>Islamische Finanzethik &amp; Moderne Investments</h2>
        <p class="subtitle">Zwischen Fiqh-Dogma, mathematischem Risikomanagement und der Kraft der Absicht (Niyyah)</p>
    </div>
    
    <div class="article-body">
        <p>Wir leben in einer Epoche tiefgreifender ökonomischer Umbrüche.</p>
        <hr>
        <h3>1. Das Fundament: Wovor die islamische Finanzethik wirklich schützen will</h3>
        <ul>
            <li><strong>Riba (Zins):</strong> Die Erzielung eines garantierten, risikolosen Geldertrags.</li>
            <li><strong>Gharar:</strong> Übermäßige Ungewissheit.</li>
        </ul>
    </div>
</div>`

	masked, tokenMap, stats := MaskHTML(htmlInput)

	if len(tokenMap) == 0 {
		t.Fatalf("expected non-empty tokenMap")
	}

	t.Logf("Original len: %d, Masked len: %d, Saved: %d (%.2f%%), Tokens: %d, Collapsed: %d",
		stats.OriginalLength, stats.MaskedLength, stats.CharactersSaved, stats.PercentSaved, stats.TokenCount, stats.CollapsedCount)

	if stats.MaskedLength >= stats.OriginalLength {
		t.Errorf("expected masked length (%d) to be less than original length (%d)", stats.MaskedLength, stats.OriginalLength)
	}

	if stats.CollapsedCount == 0 {
		t.Errorf("expected neighboring tags to be collapsed")
	}

	// Test unmasking
	unmasked := UnmaskHTML(masked, tokenMap)
	if unmasked != htmlInput {
		t.Fatalf("unmasked HTML does not match original!\nGot:\n%s\nExpected:\n%s", unmasked, htmlInput)
	}
}

func TestHTMLMaskingTolerance(t *testing.T) {
	tokenMap := map[string]string{
		"{{M0}}": "<div class=\"test\">",
		"{{M1}}": "</div>",
	}

	// Model outputs with space or lower-case variation
	translated := "Here is some content: {{ m0 }} This is English text. {{  M1  }}"
	unmasked := UnmaskHTML(translated, tokenMap)

	expected := "Here is some content: <div class=\"test\"> This is English text. </div>"
	if unmasked != expected {
		t.Errorf("expected %q, got %q", expected, unmasked)
	}
}

func TestValidateMaskTokens(t *testing.T) {
	tokenMap := map[string]string{
		"{{M0}}": "<h1>",
		"{{M1}}": "</h1>",
		"{{M2}}": "<p>",
		"{{M3}}": "</p>",
	}

	completeText := "{{M0}} Title {{M1}} {{M2}} Body {{M3}}"
	missing, err := ValidateMaskTokens(completeText, tokenMap)
	if err != nil || len(missing) > 0 {
		t.Errorf("expected valid, got error: %v, missing: %v", err, missing)
	}

	truncatedText := "{{M0}} Title {{M1}} {{M2}} Body (cut off"
	missing, err = ValidateMaskTokens(truncatedText, tokenMap)
	if err == nil {
		t.Errorf("expected error for truncated text, got nil")
	}
	if len(missing) != 1 || missing[0] != "{{M3}}" {
		t.Errorf("expected {{M3}} missing, got %v", missing)
	}
}

func TestCleanLLMTranslationOutput(t *testing.T) {
	raw := "```html\nI understand, I have to translate this document. Here is the translated document:\n{{M0}}This is the translated title{{M1}}\n{{M2}}This is the translated body{{M3}}\nHope this helps!\n```"

	cleaned := CleanLLMTranslationOutput(raw, "{{M0}}", "{{M3}}")
	expected := "{{M0}}This is the translated title{{M1}}\n{{M2}}This is the translated body{{M3}}"

	if cleaned != expected {
		t.Errorf("expected:\n%q\ngot:\n%q", expected, cleaned)
	}
}

func TestFullArticleMaskingRoundtrip(t *testing.T) {
	bytes, err := os.ReadFile("../../frontend/content/islamic-finance-modern-investments_de.html")
	if err != nil {
		t.Skip("skipping full article test: file not found")
	}

	htmlContent := string(bytes)
	masked, tokenMap, stats := MaskHTML(htmlContent)

	t.Logf("Full Article Stats -> Original: %d bytes, Masked: %d bytes, Saved: %d bytes (%.2f%%), Tokens: %d, Collapsed: %d",
		stats.OriginalLength, stats.MaskedLength, stats.CharactersSaved, stats.PercentSaved, stats.TokenCount, stats.CollapsedCount)

	if stats.CharactersSaved <= 0 {
		t.Errorf("expected character savings on full article")
	}

	unmasked := UnmaskHTML(masked, tokenMap)
	if unmasked != htmlContent {
		t.Fatalf("unmasked full article does not match original byte-for-byte!")
	}
}
