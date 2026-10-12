package pgvec

import "testing"

func TestFormat_RendersBracketedCommaSeparatedValues(t *testing.T) {
	got := Format([]float64{0.1, 0.2, 0.3})
	want := "[0.1,0.2,0.3]"
	if got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}

func TestFormat_EmptySlice_RendersEmptyBrackets(t *testing.T) {
	got := Format([]float64{})
	want := "[]"
	if got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}
