// Package jobtext renders a job into the exact text(s) freehire's own
// cmd/embed feeds to TEI, so volunteer-computed embeddings are comparable
// to the rest of production's search index — not just internally
// self-consistent. Ported from internal/search/search/{plaintext,embed,chunk}.go
// in the hire repo; keep it byte-for-byte aligned with that source, since
// any drift here means a promoted chunk set silently diverges from what
// cmd/embed would have produced for the same job.
package jobtext

import (
	"html"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

var plainTextPolicy = bluemonday.StrictPolicy()

var blockBoundary = regexp.MustCompile(`(?i)</(h[1-6]|p|li|div|tr|td|th|table|blockquote|pre)\s*>|<(br|hr)\s*/?>`)

var collapseBlankLines = regexp.MustCompile(`\n[ \t]*\n(\n[ \t]*)*`)

// stripToPlainText renders job description HTML into plain prose,
// preserving paragraph/list/table-row boundaries as line breaks.
func stripToPlainText(rawHTML string) string {
	marked := blockBoundary.ReplaceAllString(rawHTML, "$0\n")
	text := html.UnescapeString(plainTextPolicy.Sanitize(marked))
	text = collapseBlankLines.ReplaceAllString(text, "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// FormatChunksForEmbedding renders a job's title, company, and HTML
// description into one or more "passage: {title} at {company}. {chunk}"
// texts e5 expects on the corpus side — one per chunk of the HTML-stripped
// description (see chunkText), matching hire's own jobChunkPassages
// byte-for-byte: every chunk carries the same prefix, since each becomes
// an independently-scored vector. A job whose description strips to
// nothing yields no passages at all, not one passage of just the prefix.
func FormatChunksForEmbedding(title, company, description string) []string {
	chunks := chunkText(stripToPlainText(description))
	if len(chunks) == 0 {
		return nil
	}
	prefix := "passage: " + title + " at " + company + ". "
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = prefix + c
	}
	return out
}
