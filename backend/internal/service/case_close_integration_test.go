package service

// 结案前置校验集成测试：用 SQLite 内存库（纯 Go 驱动，CGO_ENABLED=0 亦可运行）
// 验证结案校验的完整行为：阻塞拒绝、数据不变、管理员不例外、条件满足后放行。

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"cylawcase/internal/constants"
	"cylawcase/internal/model"
	"cylawcase/internal/repository"
	"cylawcase/internal/util"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCloseTest(t *testing.T) (*CaseService, *gorm.DB, uint64) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Client{}, &model.Case{}, &model.Document{}, &model.Billing{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	client := model.Client{Name: "张三"}
	lawyer := model.User{Username: "lawyer1", PasswordHash: "x", Role: constants.RoleLawyer}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&lawyer).Error; err != nil {
		t.Fatalf("seed lawyer: %v", err)
	}
	svc := NewCaseService(
		repository.NewCaseRepository(db),
		repository.NewClientRepository(db),
		repository.NewUserRepository(db),
		repository.NewDocumentRepository(db),
		repository.NewBillingRepository(db),
		slog.Default(),
	)
	return svc, db, client.ID
}

func newHearingCase(t *testing.T, db *gorm.DB, clientID uint64, caseNo string) model.Case {
	t.Helper()
	c := model.Case{
		CaseNo: caseNo, Title: "测试案件", CaseType: constants.CaseTypeCivil,
		Status: constants.CaseStatusHearing, ClientID: clientID, LeadLawyerID: 1,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("seed case: %v", err)
	}
	return c
}

func closeErr(t *testing.T, svc *CaseService, caseID uint64, role string) *util.AppError {
	t.Helper()
	_, err := svc.ChangeStatus(caseID, role, constants.CaseStatusClosed)
	if err == nil {
		t.Fatalf("expected close to be blocked (role=%s)", role)
	}
	var appErr *util.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %v", err)
	}
	return appErr
}

func TestCloseBlockedByJudgmentAndPendingBills(t *testing.T) {
	svc, db, clientID := setupCloseTest(t)
	c := newHearingCase(t, db, clientID, "CY20260001")

	// 两笔待支付 + 一笔已支付（已支付不应计入阻塞）。
	bills := []model.Billing{
		{BillNo: "B1", Amount: 1000.5, Status: constants.BillingStatusPending, CaseID: c.ID, ClientID: clientID},
		{BillNo: "B2", Amount: 2000, Status: constants.BillingStatusPending, CaseID: c.ID, ClientID: clientID},
		{BillNo: "B3", Amount: 999, Status: constants.BillingStatusPaid, CaseID: c.ID, ClientID: clientID},
	}
	for i := range bills {
		if err := db.Create(&bills[i]).Error; err != nil {
			t.Fatalf("seed billing: %v", err)
		}
	}

	appErr := closeErr(t, svc, c.ID, constants.RoleLawyer)
	if appErr.Code != constants.CodeCaseCloseBlocked {
		t.Fatalf("code = %d, want %d", appErr.Code, constants.CodeCaseCloseBlocked)
	}
	info, ok := appErr.Data.(*CaseCloseBlockInfo)
	if !ok {
		t.Fatalf("error data type = %T, want *CaseCloseBlockInfo", appErr.Data)
	}
	if len(info.MissingMaterials) != 1 || info.MissingMaterials[0] != constants.DocTypeJudgment {
		t.Errorf("missing_materials = %v, want [judgment]", info.MissingMaterials)
	}
	if info.PendingBillCount != 2 {
		t.Errorf("pending_bill_count = %d, want 2", info.PendingBillCount)
	}
	if info.PendingBillTotal != 3000.5 {
		t.Errorf("pending_bill_total = %v, want 3000.5", info.PendingBillTotal)
	}

	// 被拒后案件状态、结案日期、账单均不得变动。
	var after model.Case
	if err := db.First(&after, c.ID).Error; err != nil {
		t.Fatalf("reload case: %v", err)
	}
	if after.Status != constants.CaseStatusHearing {
		t.Errorf("status changed to %s after rejection", after.Status)
	}
	if after.CloseDate != nil {
		t.Errorf("close_date set to %v after rejection", after.CloseDate)
	}
	var billCount int64
	db.Model(&model.Billing{}).Where("case_id = ?", c.ID).Count(&billCount)
	if billCount != 3 {
		t.Errorf("billing count = %d after rejection, want 3", billCount)
	}
}

func TestCloseBlockedAppliesToAdmin(t *testing.T) {
	svc, db, clientID := setupCloseTest(t)
	c := newHearingCase(t, db, clientID, "CY20260002")

	// 管理员也不能例外放行（缺判决书即拒绝）。
	appErr := closeErr(t, svc, c.ID, constants.RoleAdmin)
	if appErr.Code != constants.CodeCaseCloseBlocked {
		t.Fatalf("admin close: code = %d, want %d", appErr.Code, constants.CodeCaseCloseBlocked)
	}
	var after model.Case
	db.First(&after, c.ID)
	if after.Status != constants.CaseStatusHearing || after.CloseDate != nil {
		t.Errorf("admin rejection mutated case: status=%s close_date=%v", after.Status, after.CloseDate)
	}
}

func TestCloseAllowedWhenReady(t *testing.T) {
	svc, db, clientID := setupCloseTest(t)
	c := newHearingCase(t, db, clientID, "CY20260003")

	doc := model.Document{Title: "一审判决书", FileType: constants.DocTypeJudgment, CaseID: c.ID, UploaderID: 1, UploadTime: time.Now()}
	if err := db.Create(&doc).Error; err != nil {
		t.Fatalf("seed judgment: %v", err)
	}
	// 已作废账单不算待支付。
	voidBill := model.Billing{BillNo: "B9", Amount: 500, Status: constants.BillingStatusVoid, CaseID: c.ID, ClientID: clientID}
	if err := db.Create(&voidBill).Error; err != nil {
		t.Fatalf("seed void billing: %v", err)
	}

	updated, err := svc.ChangeStatus(c.ID, constants.RoleLawyer, constants.CaseStatusClosed)
	if err != nil {
		t.Fatalf("close should succeed, got %v", err)
	}
	if updated.Status != constants.CaseStatusClosed {
		t.Errorf("status = %s, want closed", updated.Status)
	}
	if updated.CloseDate == nil {
		t.Error("close_date should be set on successful close")
	}
}

func TestCloseSucceedsAfterBlockersResolved(t *testing.T) {
	svc, db, clientID := setupCloseTest(t)
	c := newHearingCase(t, db, clientID, "CY20260004")

	bill := model.Billing{BillNo: "B10", Amount: 800, Status: constants.BillingStatusPending, CaseID: c.ID, ClientID: clientID}
	if err := db.Create(&bill).Error; err != nil {
		t.Fatalf("seed billing: %v", err)
	}
	closeErr(t, svc, c.ID, constants.RoleLawyer)

	// 补齐判决书并支付账单后允许结案。
	doc := model.Document{Title: "判决书", FileType: constants.DocTypeJudgment, CaseID: c.ID, UploaderID: 1, UploadTime: time.Now()}
	db.Create(&doc)
	bill.Status = constants.BillingStatusPaid
	db.Save(&bill)

	if _, err := svc.ChangeStatus(c.ID, constants.RoleLawyer, constants.CaseStatusClosed); err != nil {
		t.Fatalf("close should succeed after resolving blockers, got %v", err)
	}
}
