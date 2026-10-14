package services

import (
	"strings"
	"testing"
)

func TestHTMLToMarkdown_Teaser(t *testing.T) {
	htmlTeaser := `<div class="icon"><i class="fa-solid fa-chart-line"></i></div>
<h3>Finanzielle Souveränität</h3>
<p><b>Rendite schlägt Inflation</b><br />Strategien für den systematischen Vermögensaufbau mit ETFs und Sachwerten.</p>`

	md := HTMLToMarkdown(htmlTeaser)

	// Should strip <div class="icon">
	if strings.Contains(md, "fa-solid") || strings.Contains(md, "icon") {
		t.Errorf("Expected icon to be stripped, got: %s", md)
	}

	// Should contain header
	if !strings.Contains(md, "### Finanzielle Souveränität") {
		t.Errorf("Expected ### heading, got: %s", md)
	}

	// Should contain bold catchphrase
	if !strings.Contains(md, "**Rendite schlägt Inflation**") {
		t.Errorf("Expected bold catchphrase, got: %s", md)
	}

	// Should contain description
	if !strings.Contains(md, "Strategien für den systematischen Vermögensaufbau") {
		t.Errorf("Expected description, got: %s", md)
	}
}

func TestHTMLToMarkdown_Article(t *testing.T) {
	htmlArticle := `<div class="lightbox-article">
    <div class="article-header">
        <h2>Über mich – Björn Eberhardt</h2>
        <p class="subtitle">IT-Spezialist, Finanzstratege und Bewegungscoach</p>
    </div>
    
    <div class="article-body">
        <p>Ich bin Björn Eberhardt, 37 Jahre alt, geboren und aufgewachsen in Hamburg.</p>
        
        <h3>Mein Fundament</h3>
        <ul>
            <li><strong>IT-Beratung & Systemarchitektur:</strong> Über 15 Jahre Erfahrung.</li>
            <li><strong>Finanzcoaching:</strong> Unkonventionelle Renditequellen.</li>
        </ul>
        
        <blockquote>
            "Es geht nicht darum, in einer Schublade zu stecken."
        </blockquote>

        <table>
            <thead>
                <tr><th>Säule</th><th>Fokus</th></tr>
            </thead>
            <tbody>
                <tr><td>Kopf</td><td>IT & Logik</td></tr>
                <tr><td>Körper</td><td>Parkour</td></tr>
            </tbody>
        </table>
    </div>
</div>`

	md := HTMLToMarkdown(htmlArticle)

	if !strings.Contains(md, "## Über mich – Björn Eberhardt") {
		t.Errorf("Expected ## heading, got: %s", md)
	}
	if !strings.Contains(md, "### Mein Fundament") {
		t.Errorf("Expected ### heading, got: %s", md)
	}
	if !strings.Contains(md, "**IT-Beratung & Systemarchitektur:**") {
		t.Errorf("Expected bold list item, got: %s", md)
	}
	if !strings.Contains(md, ">") || !strings.Contains(md, "Schublade") {
		t.Errorf("Expected blockquote, got: %s", md)
	}
	// Check table converted
	if !strings.Contains(md, "Säule") || !strings.Contains(md, "Fokus") || !strings.Contains(md, "|-") {
		t.Errorf("Expected markdown table, got: %s", md)
	}
}

func TestHTMLToMarkdown_EmptyAndFailOpen(t *testing.T) {
	if HTMLToMarkdown("") != "" {
		t.Errorf("Expected empty string for empty input")
	}
	if HTMLToMarkdown("   ") != "" {
		t.Errorf("Expected empty string for whitespace input")
	}
}
