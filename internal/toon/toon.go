package toon

import (
	"fmt"
	"strings"

	toongo "github.com/toon-format/toon-go"
	"github.com/windowsfreak/leben/internal/models"
)

type Payload struct {
	Header string           `json:"header" toon:"header"`
	Query  string           `json:"query" toon:"query"`
	Lang   string           `json:"lang" toon:"lang"`
	Format string           `json:"format" toon:"format"`
	Count  int              `json:"count" toon:"count"`
	Tiles  []models.TileDTO `json:"tiles" toon:"tiles"`
}

func FormatTOON(query, lang, format string, tiles []models.TileDTO) (string, error) {
	// Attempt encoding with toon-go
	p := Payload{
		Header: "Leben App LLM Search Results",
		Query:  query,
		Lang:   lang,
		Format: format,
		Count:  len(tiles),
		Tiles:  tiles,
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
	sb.WriteString(fmt.Sprintf("# Query: %s | Lang: %s | Format: %s | Total: %d\n\n", queryLabel, lang, format, len(tiles)))

	for _, tile := range tiles {
		if tile.Score != nil {
			sb.WriteString(fmt.Sprintf("--- CARD %d (score: %.2f) ---\n", tile.Index, *tile.Score))
			sb.WriteString(fmt.Sprintf("name: %s | lang: %s | type: %s | score: %.2f | date: %s\n", tile.Name, tile.Lang, tile.Type, *tile.Score, tile.Date))
		} else {
			sb.WriteString(fmt.Sprintf("--- CARD %d ---\n", tile.Index))
			sb.WriteString(fmt.Sprintf("name: %s | lang: %s | type: %s | date: %s\n", tile.Name, tile.Lang, tile.Type, tile.Date))
		}
		sb.WriteString(fmt.Sprintf("title: %s\n", tile.Title))
		sb.WriteString(fmt.Sprintf("tags: %s\n", tile.Tags))

		if format != "min" {
			sb.WriteString(fmt.Sprintf("summary: %s\n", tile.Summary))
		}
		if format == "full" && tile.Content != "" {
			sb.WriteString(fmt.Sprintf("body:\n%s\n", strings.TrimSpace(tile.Content)))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
