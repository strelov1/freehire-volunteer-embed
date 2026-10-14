package jobtext

import (
	"reflect"
	"strings"
	"testing"
)

func TestFormatChunksForEmbedding_PlainTextDescription_AddsTitleCompanyPrefix(t *testing.T) {
	got := FormatChunksForEmbedding("Senior Backend Engineer", "Acme", "Build great APIs.")
	want := []string{"passage: Senior Backend Engineer at Acme. Build great APIs."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatChunksForEmbedding_StripsHTMLTags(t *testing.T) {
	got := FormatChunksForEmbedding("Engineer", "Acme", "<p>Build APIs.</p><p>Ship fast.</p>")
	want := []string{"passage: Engineer at Acme. Build APIs.\nShip fast."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatChunksForEmbedding_DecodesHTMLEntitiesInDescriptionOnly(t *testing.T) {
	// title/company are used verbatim (hire's own jobChunkPassages does the
	// same — only the description goes through HTML stripping/unescaping).
	got := FormatChunksForEmbedding("Engineer", "R&D Co", "<p>R&amp;D work.</p>")
	want := []string{"passage: Engineer at R&D Co. R&D work."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestFormatChunksForEmbedding_EmptyDescription_YieldsNoPassages matches hire's
// own jobChunkPassages: a job whose description strips to nothing yields no
// passages at all, not one passage of just the "passage: title at company. " prefix.
func TestFormatChunksForEmbedding_EmptyDescription_YieldsNoPassages(t *testing.T) {
	got := FormatChunksForEmbedding("Engineer", "Acme", "")
	if got != nil {
		t.Errorf("got %q, want nil", got)
	}
}

// TestFormatChunksForEmbedding_LongDescription_YieldsOnePassagePerChunk proves the
// title/company prefix is repeated on EVERY chunk (each chunk is an independently
// scored passage, matching hire's own jobChunkPassages), and that chunking a long
// description actually produces more than one passage.
func TestFormatChunksForEmbedding_LongDescription_YieldsOnePassagePerChunk(t *testing.T) {
	var paras []string
	for i := 0; i < 40; i++ {
		paras = append(paras, strings.Repeat("word ", 20)+"end of paragraph.")
	}
	description := strings.Join(paras, "\n")

	got := FormatChunksForEmbedding("Engineer", "Acme", description)
	if len(got) <= 1 {
		t.Fatalf("len(got) = %d, want > 1 for a description well past the chunk budget", len(got))
	}
	for i, passage := range got {
		if !strings.HasPrefix(passage, "passage: Engineer at Acme. ") {
			t.Errorf("passage %d = %q, missing the title/company prefix", i, passage)
		}
	}
}
