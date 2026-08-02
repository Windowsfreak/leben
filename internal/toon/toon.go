package toon

import (
	"fmt"
	"strings"

	toongo "github.com/toon-format/toon-go"
)

type Item struct {
	Index   int    `json:"index" toon:"index"`
	Name    string `json:"name" toon:"name"`
	Title   string `json:"title" toon:"title"`
	Lang    string `json:"lang" toon:"lang"`
	Tags    string `json:"tags" toon:"tags"`
	Type    string `json:"type" toon:"type"`
	Date    string `json:"date" toon:"date"`
	Score   string `json:"score,omitempty" toon:"score,omitempty"`
	Summary string `json:"summary" toon:"summary"`
	Content string `json:"content,omitempty" toon:"content,omitempty"`
	Link    string `json:"link,omitempty" toon:"link,omitempty"`
}

type Payload struct {
	Header string `json:"header" toon:"header"`
	Query  string `json:"query" toon:"query"`
	Lang   string `json:"lang" toon:"lang"`
	Format string `json:"format" toon:"format"`
	Count  int    `json:"count" toon:"count"`
	Items  []Item `json:"items" toon:"items"`
}

func FormatTOON(query, lang, format string, items []Item) (string, error) {
	// Attempt encoding with toon-go
	p := Payload{
		Header: "Leben App LLM Search Results",
		Query:  query,
		Lang:   lang,
		Format: format,
		Count:  len(items),
		Items:  items,
	}

	encoded, err := toongo.Marshal(p)
	if err == nil && len(encoded) > 0 {
		return string(encoded), nil
	}

	// Fallback cleanly formatted TOON string if toon-go encoder is minimalistic
	var sb strings.Builder
	queryLabel := query
	if queryLabel == "" {
		queryLabel = "[Curated List]"
	}

	sb.WriteString("# Leben App LLM Search Results\n")
	sb.WriteString(fmt.Sprintf("# Query: %s | Lang: %s | Format: %s | Total: %d\n\n", queryLabel, lang, format, len(items)))

	for _, item := range items {
		if item.Score != "" {
			sb.WriteString(fmt.Sprintf("--- CARD %d (score: %s) ---\n", item.Index, item.Score))
			sb.WriteString(fmt.Sprintf("name: %s | lang: %s | type: %s | score: %s | date: %s\n", item.Name, item.Lang, item.Type, item.Score, item.Date))
		} else {
			sb.WriteString(fmt.Sprintf("--- CARD %d ---\n", item.Index))
			sb.WriteString(fmt.Sprintf("name: %s | lang: %s | type: %s | date: %s\n", item.Name, item.Lang, item.Type, item.Date))
		}
		sb.WriteString(fmt.Sprintf("title: %s\n", item.Title))
		sb.WriteString(fmt.Sprintf("tags: %s\n", item.Tags))

		if format != "min" {
			sb.WriteString(fmt.Sprintf("summary: %s\n", item.Summary))
		}
		if format == "full" && item.Content != "" {
			sb.WriteString(fmt.Sprintf("body:\n%s\n", strings.TrimSpace(item.Content)))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
