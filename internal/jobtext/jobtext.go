// Package jobtext renders a job into the exact text freehire's own
// cmd/embed feeds to TEI, so volunteer-computed embeddings are comparable
// to the rest of production's search index — not just internally
// self-consistent. Ported from internal/search/search/{plaintext,embed}.go
// in the hire repo; keep it byte-for-byte aligned with that source, since
// any drift here means promoted vectors silently diverge from what
// cmd/embed would have produced for the same job.
//
// Known limitation (design.md Non-Goals): hire's real pipeline splits a
// long description into multiple chunks (chunkText, 2000-rune budget) and
// embeds each separately. This package renders the WHOLE stripped
// description as one unit — correct for jobs that would only ever produce
// a single chunk anyway (the common case), but not equivalent to
// production for a description long enough to need more than one.
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

// FormatForEmbedding renders a job's title, company, and HTML description
// into the "passage: {title} at {company}. {description}" text e5 expects
// on the corpus side. title and company are used verbatim, matching
// jobChunkPassages — only the description is HTML-stripped.
func FormatForEmbedding(title, company, description string) string {
	return "passage: " + title + " at " + company + ". " + stripToPlainText(description)
}
