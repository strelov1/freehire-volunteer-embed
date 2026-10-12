package jobtext

import "testing"

func TestFormatForEmbedding_PlainTextDescription_AddsTitleCompanyPrefix(t *testing.T) {
	got := FormatForEmbedding("Senior Backend Engineer", "Acme", "Build great APIs.")
	want := "passage: Senior Backend Engineer at Acme. Build great APIs."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatForEmbedding_StripsHTMLTags(t *testing.T) {
	got := FormatForEmbedding("Engineer", "Acme", "<p>Build APIs.</p><p>Ship fast.</p>")
	want := "passage: Engineer at Acme. Build APIs.\nShip fast."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatForEmbedding_DecodesHTMLEntitiesInDescriptionOnly(t *testing.T) {
	// title/company are used verbatim (hire's own jobChunkPassages does the
	// same — only the description goes through HTML stripping/unescaping).
	got := FormatForEmbedding("Engineer", "R&D Co", "<p>R&amp;D work.</p>")
	want := "passage: Engineer at R&D Co. R&D work."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
