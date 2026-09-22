package service

import (
	"testing"

	"cylawcase/internal/constants"
	"cylawcase/internal/dto"
)

func TestCanFlow(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{constants.CaseStatusFiled, constants.CaseStatusInvestigating, true},
		{constants.CaseStatusInvestigating, constants.CaseStatusHearing, true},
		{constants.CaseStatusHearing, constants.CaseStatusClosed, true},
		{constants.CaseStatusClosed, constants.CaseStatusArchived, true},
		{constants.CaseStatusFiled, constants.CaseStatusClosed, false},
		{constants.CaseStatusClosed, constants.CaseStatusFiled, false},
		{constants.CaseStatusInvestigating, constants.CaseStatusFiled, true},
	}
	for _, tc := range cases {
		if got := canFlow(tc.from, tc.to); got != tc.want {
			t.Errorf("canFlow(%s->%s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestContains(t *testing.T) {
	if !contains(constants.CaseTypeValues, constants.CaseTypeLabor) {
		t.Error("labor should be in case types")
	}
	if contains(constants.CaseTypeValues, "bogus") {
		t.Error("bogus should not be in case types")
	}
}

func TestU64(t *testing.T) {
	if u64(42) != "42" {
		t.Error("u64(42) != 42")
	}
}

func TestStatusValidators(t *testing.T) {
	if !constants.IsValidCaseStatus(constants.CaseStatusArchived) {
		t.Error("archived should be valid")
	}
	if constants.IsValidCaseStatus("bogus") {
		t.Error("bogus should be invalid")
	}
	if !constants.IsValidBillingStatus(constants.BillingStatusInvoiced) {
		t.Error("invoiced should be valid")
	}
	if !constants.IsValidBillingType(constants.BillingTypeTravelFee) {
		t.Error("travel_fee should be valid")
	}
}

func TestBuildCloseCheckResult(t *testing.T) {
	cases := []struct {
		name            string
		hasJudgment     bool
		pendingCount    int64
		pendingTotal    float64
		wantCanClose    bool
		wantMissing     []string
		wantReasonCount int
	}{
		{
			name:            "both satisfied",
			hasJudgment:     true,
			pendingCount:    0,
			pendingTotal:    0,
			wantCanClose:    true,
			wantMissing:     nil,
			wantReasonCount: 0,
		},
		{
			name:            "missing judgment only",
			hasJudgment:     false,
			pendingCount:    0,
			pendingTotal:    0,
			wantCanClose:    false,
			wantMissing:     []string{dto.CloseRequirementJudgment},
			wantReasonCount: 1,
		},
		{
			name:            "pending billings only",
			hasJudgment:     true,
			pendingCount:    3,
			pendingTotal:    12500.5,
			wantCanClose:    false,
			wantMissing:     nil,
			wantReasonCount: 1,
		},
		{
			name:            "both missing",
			hasJudgment:     false,
			pendingCount:    2,
			pendingTotal:    800,
			wantCanClose:    false,
			wantMissing:     []string{dto.CloseRequirementJudgment},
			wantReasonCount: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCloseCheckResult(tc.hasJudgment, tc.pendingCount, tc.pendingTotal)
			if got.CanClose != tc.wantCanClose {
				t.Errorf("CanClose = %v, want %v", got.CanClose, tc.wantCanClose)
			}
			if len(got.MissingMaterials) != len(tc.wantMissing) {
				t.Errorf("MissingMaterials = %v, want %v", got.MissingMaterials, tc.wantMissing)
			}
			for _, m := range tc.wantMissing {
				found := false
				for _, g := range got.MissingMaterials {
					if g == m {
						found = true
					}
				}
				if !found {
					t.Errorf("MissingMaterials %v should contain %q", got.MissingMaterials, m)
				}
			}
			if got.PendingBillingCount != tc.pendingCount {
				t.Errorf("PendingBillingCount = %d, want %d", got.PendingBillingCount, tc.pendingCount)
			}
			if got.PendingBillingAmount != tc.pendingTotal {
				t.Errorf("PendingBillingAmount = %v, want %v", got.PendingBillingAmount, tc.pendingTotal)
			}
			if len(got.BlockingReasons) != tc.wantReasonCount {
				t.Errorf("BlockingReasons = %v, want %d reasons", got.BlockingReasons, tc.wantReasonCount)
			}
		})
	}
}

func TestRoundAmount(t *testing.T) {
	cases := map[float64]float64{
		100.123:     100.12,
		100.126:     100.13,
		0:           0,
		-1.005:      -1.0, // 浮点表示下四舍五入
		1234567.891: 1234567.89,
	}
	for in, want := range cases {
		if got := roundAmount(in); got != want {
			t.Errorf("roundAmount(%v) = %v, want %v", in, got, want)
		}
	}
}
