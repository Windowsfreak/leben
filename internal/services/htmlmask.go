package services

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

type MaskStats struct {
	OriginalLength  int
	MaskedLength    int
	CharactersSaved int
	PercentSaved    float64
	TokenCount      int
	CollapsedCount  int
}

// Regex to identify HTML tags, comments, script/style blocks
var (
	tagOrCommentRegex = regexp.MustCompile(`(?s)(<!--.*?-->|<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>|<[^>]+>)`)
	tokenMatcherRegex = regexp.MustCompile(`(?i)\{\{\s*M(\d+)\s*\}\}`)
)

// MaskHTML masks all HTML tags and collapses adjacent tags/whitespace into single Mustache tokens {{M#}}.
func MaskHTML(htmlContent string) (maskedText string, tokenMap map[string]string, stats MaskStats) {
	tokenMap = make(map[string]string)
	stats.OriginalLength = len(htmlContent)

	if strings.TrimSpace(htmlContent) == "" {
		return "", tokenMap, stats
	}

	// 1. Locate all tag matches
	matches := tagOrCommentRegex.FindAllStringIndex(htmlContent, -1)
	if len(matches) == 0 {
		stats.MaskedLength = len(htmlContent)
		return htmlContent, tokenMap, stats
	}

	type chunk struct {
		isTag bool
		start int
		end   int
	}

	var chunks []chunk
	lastIdx := 0

	for _, m := range matches {
		start, end := m[0], m[1]
		if start > lastIdx {
			// text between tags
			chunks = append(chunks, chunk{
				isTag: false,
				start: lastIdx,
				end:   start,
			})
		}
		chunks = append(chunks, chunk{
			isTag: true,
			start: start,
			end:   end,
		})
		lastIdx = end
	}

	if lastIdx < len(htmlContent) {
		chunks = append(chunks, chunk{
			isTag: false,
			start: lastIdx,
			end:   len(htmlContent),
		})
	}

	var resultBuilder strings.Builder
	tokenIdx := 0
	numCollapsed := 0

	i := 0
	for i < len(chunks) {
		if !chunks[i].isTag {
			text := htmlContent[chunks[i].start:chunks[i].end]
			resultBuilder.WriteString(text)
			i++
			continue
		}

		// Found a tag chunk. Look ahead to collapse contiguous tags and pure whitespace between them.
		rawStart := chunks[i].start
		rawEnd := chunks[i].end
		tagsInBlock := 1

		j := i + 1
		for j < len(chunks) {
			if chunks[j].isTag {
				rawEnd = chunks[j].end
				tagsInBlock++
				j++
			} else {
				// Check if the text chunk is purely whitespace (formatting / newlines / spaces)
				text := htmlContent[chunks[j].start:chunks[j].end]
				if isPureWhitespace(text) && j+1 < len(chunks) && chunks[j+1].isTag {
					// It's whitespace between tags: include it in the collapsed block
					rawEnd = chunks[j+1].end
					tagsInBlock++
					j += 2
				} else {
					break
				}
			}
		}

		rawHTML := htmlContent[rawStart:rawEnd]
		token := fmt.Sprintf("{{M%d}}", tokenIdx)
		tokenMap[token] = rawHTML

		resultBuilder.WriteString(token)
		tokenIdx++
		if tagsInBlock > 1 {
			numCollapsed += (tagsInBlock - 1)
		}

		i = j
	}

	maskedText = resultBuilder.String()
	stats.MaskedLength = len(maskedText)
	stats.CharactersSaved = stats.OriginalLength - stats.MaskedLength
	if stats.OriginalLength > 0 {
		stats.PercentSaved = float64(stats.CharactersSaved) / float64(stats.OriginalLength) * 100.0
	}
	stats.TokenCount = tokenIdx
	stats.CollapsedCount = numCollapsed

	return maskedText, tokenMap, stats
}

func isPureWhitespace(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// UnmaskHTML restores all {{M#}} tokens in the translated text back to the original HTML tag sequences.
func UnmaskHTML(translatedText string, tokenMap map[string]string) string {
	if len(tokenMap) == 0 {
		return translatedText
	}

	// Case-insensitive token replacement with space tolerance
	replaced := tokenMatcherRegex.ReplaceAllStringFunc(translatedText, func(match string) string {
		sub := tokenMatcherRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idxStr := sub[1]
		key := fmt.Sprintf("{{M%s}}", idxStr)
		if orig, ok := tokenMap[key]; ok {
			return orig
		}
		return match
	})

	return replaced
}

// ValidateMaskTokens verifies that the translated text contains all required tokens,
// especially the starting and closing tokens, ensuring no truncation occurred.
func ValidateMaskTokens(translatedText string, tokenMap map[string]string) (missing []string, err error) {
	for token := range tokenMap {
		sub := tokenMatcherRegex.FindStringSubmatch(token)
		if len(sub) >= 2 {
			pattern := fmt.Sprintf(`(?i)\{\{\s*M%s\s*\}\}`, sub[1])
			re := regexp.MustCompile(pattern)
			if !re.MatchString(translatedText) {
				missing = append(missing, token)
			}
		}
	}

	if len(missing) > 0 {
		return missing, fmt.Errorf("translation is incomplete or truncated: %d tokens missing (%v)", len(missing), missing)
	}

	return nil, nil
}

// CleanLLMTranslationOutput cleans LLM response by removing markdown blocks, preambles, and filler.
func CleanLLMTranslationOutput(rawResponse string, firstToken string, lastToken string) string {
	s := strings.TrimSpace(rawResponse)

	// Remove markdown backticks wrapper
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 && strings.HasPrefix(lines[0], "```") {
			lines = lines[1:]
		}
		if len(lines) >= 1 && strings.HasPrefix(lines[len(lines)-1], "```") {
			lines = lines[:len(lines)-1]
		}
		s = strings.Join(lines, "\n")
		s = strings.TrimSpace(s)
	}

	// If first token is present and preceded by conversational preambles ("Here is the translation:", "I understand..."),
	// trim everything before the first token.
	if firstToken != "" {
		sub := tokenMatcherRegex.FindStringSubmatch(firstToken)
		if len(sub) >= 2 {
			re := regexp.MustCompile(fmt.Sprintf(`(?i)\{\{\s*M%s\s*\}\}`, sub[1]))
			loc := re.FindStringIndex(s)
			if loc != nil && loc[0] > 0 {
				preceding := strings.TrimSpace(s[:loc[0]])
				lowerPre := strings.ToLower(preceding)
				if strings.HasPrefix(lowerPre, "here is") ||
					strings.HasPrefix(lowerPre, "here's") ||
					strings.HasPrefix(lowerPre, "i understand") ||
					strings.HasPrefix(lowerPre, "translation:") ||
					strings.HasPrefix(lowerPre, "translated:") ||
					strings.HasPrefix(lowerPre, "sure") ||
					strings.HasPrefix(lowerPre, "certainly") ||
					len(preceding) < 150 {
					s = s[loc[0]:]
				}
			}
		}
	}

	// If last token is present and followed by conversational post-amble ("Hope this helps!", "Let me know if..."),
	// trim everything after the last token.
	if lastToken != "" {
		sub := tokenMatcherRegex.FindStringSubmatch(lastToken)
		if len(sub) >= 2 {
			re := regexp.MustCompile(fmt.Sprintf(`(?i)\{\{\s*M%s\s*\}\}`, sub[1]))
			locs := re.FindAllStringIndex(s, -1)
			if len(locs) > 0 {
				lastLoc := locs[len(locs)-1]
				after := strings.TrimSpace(s[lastLoc[1]:])
				lowerAfter := strings.ToLower(after)
				if strings.HasPrefix(lowerAfter, "note:") ||
					strings.HasPrefix(lowerAfter, "let me know") ||
					strings.HasPrefix(lowerAfter, "i hope") ||
					strings.HasPrefix(lowerAfter, "here is") ||
					len(after) < 150 {
					s = s[:lastLoc[1]]
				}
			}
		}
	}

	return strings.TrimSpace(s)
}
