package services

import (
	"strings"

	"github.com/JohannesKaufmann/dom"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
)

var defaultMDConverter *converter.Converter

func init() {
	defaultMDConverter = newMarkdownConverter()
}

func newMarkdownConverter() *converter.Converter {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
		),
	)

	// Custom rule 1: Drop <div class="icon">...</div> entirely (used in teasers for FontAwesome)
	conv.Register.RendererFor("div", converter.TagTypeBlock, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		if dom.HasClass(node, "icon") {
			// Don't render icon containers or their children
			return converter.RenderSuccess
		}
		// For other divs (e.g. .lightbox-article, .article-header, .article-body),
		// let standard block handling unwrap their children.
		return converter.RenderTryNext
	}, converter.PriorityEarly)

	// Custom rule 2: Drop FontAwesome <i> tags completely
	conv.Register.RendererFor("i", converter.TagTypeInline, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		if dom.HasClass(node, "fa-solid") || dom.HasClass(node, "fa-brands") || dom.HasClass(node, "fa-regular") || dom.HasClass(node, "fa") {
			return converter.RenderSuccess
		}
		return converter.RenderTryNext
	}, converter.PriorityEarly)

	return conv
}

// HTMLToMarkdown converts an HTML fragment or document into clean CommonMark/GFM markdown.
// If the input is empty or conversion fails, it fails open by returning the original string.
func HTMLToMarkdown(htmlInput string) string {
	trimmed := strings.TrimSpace(htmlInput)
	if trimmed == "" {
		return ""
	}

	md, err := defaultMDConverter.ConvertString(trimmed)
	if err != nil {
		// Fail open: fallback to original string rather than breaking callers
		return htmlInput
	}

	return strings.TrimSpace(md)
}
