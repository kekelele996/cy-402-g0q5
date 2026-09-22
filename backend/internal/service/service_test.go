package service

import (
	"strings"
	"testing"

	"cylawcase/internal/constants"
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

func TestCaseCloseBlockInfoBlocked(t *testing.T) {
	cases := []struct {
		name string
		info CaseCloseBlockInfo
		want bool
	}{
		{"all satisfied", CaseCloseBlockInfo{MissingMaterials: []string{}}, false},
		{"missing judgment", CaseCloseBlockInfo{MissingMaterials: []string{constants.DocTypeJudgment}}, true},
		{"pending bills", CaseCloseBlockInfo{MissingMaterials: []string{}, PendingBillCount: 2, PendingBillTotal: 1000}, true},
		{"both missing", CaseCloseBlockInfo{MissingMaterials: []string{constants.DocTypeJudgment}, PendingBillCount: 1, PendingBillTotal: 88.5}, true},
	}
	for _, tc := range cases {
		if got := tc.info.Blocked(); got != tc.want {
			t.Errorf("%s: Blocked() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCaseCloseBlockInfoMessage(t *testing.T) {
	both := CaseCloseBlockInfo{MissingMaterials: []string{constants.DocTypeJudgment}, PendingBillCount: 2, PendingBillTotal: 1000}
	msg := both.Message()
	for _, want := range []string{"判决书", "2 笔", "1,000.00"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should contain %q", msg, want)
		}
	}
	ok := CaseCloseBlockInfo{MissingMaterials: []string{}}
	if strings.Contains(ok.Message(), "待支付账单") {
		t.Error("message should not mention pending bills when none")
	}
}
