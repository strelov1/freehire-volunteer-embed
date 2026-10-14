package verify

import "testing"

func TestCosine_IdenticalVectors_ReturnsOne(t *testing.T) {
	v := []float64{1, 2, 3}
	got := Cosine(v, v)
	if got < 0.999999 {
		t.Errorf("Cosine(v, v) = %v, want ~1.0", got)
	}
}

func TestCosine_OrthogonalVectors_ReturnsZero(t *testing.T) {
	got := Cosine([]float64{1, 0}, []float64{0, 1})
	if got > 0.0001 || got < -0.0001 {
		t.Errorf("Cosine orthogonal = %v, want ~0.0", got)
	}
}
