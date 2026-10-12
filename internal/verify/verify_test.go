package verify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/config"
)

func testCfg() config.Config {
	return config.Config{
		FullCheckThreshold: 20,
		CheckDivisorStage1: 10,
		CheckDivisorStage2: 30,
	}
}

func teiServerReturning(vec []float64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{vec})
	}))
}

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

func TestEvaluate_BelowFullCheckThreshold_PerformsCheck_Matches_PassesCheck(t *testing.T) {
	server := teiServerReturning([]float64{1, 1, 1})
	defer server.Close()

	outcome := Evaluate(context.Background(), server.URL, testCfg(), 0, 0, "job text", []float64{1, 1, 1})
	if outcome != OutcomeVerifiedPassedCheck {
		t.Errorf("outcome = %v, want OutcomeVerifiedPassedCheck", outcome)
	}
}

func TestEvaluate_CheckedButMismatched_Rejected(t *testing.T) {
	server := teiServerReturning([]float64{1, 0, 0})
	defer server.Close()

	outcome := Evaluate(context.Background(), server.URL, testCfg(), 0, 0, "job text", []float64{0, 1, 0})
	if outcome != OutcomeRejectedFailedCheck {
		t.Errorf("outcome = %v, want OutcomeRejectedFailedCheck", outcome)
	}
}

func TestEvaluate_TEIUnreachable_Deferred(t *testing.T) {
	outcome := Evaluate(context.Background(), "http://127.0.0.1:1", testCfg(), 0, 0, "job text", []float64{1, 1, 1})
	if outcome != OutcomeDeferredTEIUnavailable {
		t.Errorf("outcome = %v, want OutcomeDeferredTEIUnavailable", outcome)
	}
}

func TestEvaluate_NotSelectedForCheck_VerifiedWithoutNetworkCall(t *testing.T) {
	calledTEI := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledTEI = true
		_ = json.NewEncoder(w).Encode([][]float64{{1, 1, 1}})
	}))
	defer server.Close()

	cfg := testCfg()
	// Past the full-check phase, on a long clean streak, and this particular
	// totalChecked is not a multiple of either divisor: should skip.
	outcome := Evaluate(context.Background(), server.URL, cfg, 61, 2*cfg.FullCheckThreshold+1, "job text", []float64{9, 9, 9})
	if outcome != OutcomeVerifiedWithoutCheck {
		t.Errorf("outcome = %v, want OutcomeVerifiedWithoutCheck", outcome)
	}
	if calledTEI {
		t.Error("expected no TEI call when the submission is not selected for checking")
	}
}
