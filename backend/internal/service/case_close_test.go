package service

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"cylawcase/internal/constants"
	"cylawcase/internal/dto"
	"cylawcase/internal/model"
	"cylawcase/internal/repository"
	"cylawcase/internal/util"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestServices(t *testing.T) (*gorm.DB, *CaseService, *DocumentService, *BillingService) {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Client{}, &model.Case{},
		&model.Document{}, &model.Billing{}, &model.AuditLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	caseRepo := repository.NewCaseRepository(db)
	clientRepo := repository.NewClientRepository(db)
	userRepo := repository.NewUserRepository(db)
	documentRepo := repository.NewDocumentRepository(db)
	billingRepo := repository.NewBillingRepository(db)

	client := &model.Client{Name: "测试客户"}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	lawyer := &model.User{Username: "lawyer_t", Role: constants.RoleLawyer}
	if err := db.Create(lawyer).Error; err != nil {
		t.Fatalf("create lawyer: %v", err)
	}

	caseSvc := NewCaseService(caseRepo, clientRepo, userRepo, documentRepo, billingRepo, logger)
	documentSvc := NewDocumentService(documentRepo, caseRepo, logger)
	billingSvc := NewBillingService(billingRepo, caseRepo, clientRepo, logger)
	return db, caseSvc, documentSvc, billingSvc
}

func seedHearingCase(t *testing.T, db *gorm.DB) uint64 {
	t.Helper()
	c := &model.Case{CaseNo: "TC-CLOSE-1", Title: "结案校验案", CaseType: constants.CaseTypeCivil,
		Status: constants.CaseStatusHearing, ClientID: 1, LeadLawyerID: 1}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("create case: %v", err)
	}
	return c.ID
}

func assertAppErrorCode(t *testing.T, err error, wantCode int) *util.AppError {
	t.Helper()
	var appErr *util.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %v", err)
	}
	if appErr.Code != wantCode {
		t.Fatalf("error code = %d, want %d (message=%s)", appErr.Code, wantCode, appErr.Message)
	}
	return appErr
}

func assertCloseBlockDetails(t *testing.T, err *util.AppError) *dto.CloseBlockDetails {
	t.Helper()
	details, ok := err.Details.(*dto.CloseBlockDetails)
	if !ok {
		t.Fatalf("unexpected details type %T", err.Details)
	}
	return details
}

func reloadCase(t *testing.T, db *gorm.DB, id uint64) model.Case {
	t.Helper()
	var c model.Case
	if err := db.First(&c, id).Error; err != nil {
		t.Fatalf("reload case: %v", err)
	}
	return c
}

// 既没有判决书、也有待支付账单：任何角色（含管理员）结案都被整次拒绝。
func TestCloseBlocked_MissingBoth_AdminNotExempt(t *testing.T) {
	db, caseSvc, _, billingSvc := newTestServices(t)
	caseID := seedHearingCase(t, db)
	if _, err := billingSvc.Create(caseID, 1, constants.BillingTypeAttorneyFee, 1500.5, ""); err != nil {
		t.Fatalf("create billing: %v", err)
	}
	if _, err := billingSvc.Create(caseID, 1, constants.BillingTypeCourtFee, 300, ""); err != nil {
		t.Fatalf("create billing: %v", err)
	}

	for _, role := range []string{constants.RoleAdmin, constants.RoleLawyer} {
		_, err := caseSvc.ChangeStatus(caseID, role, constants.CaseStatusClosed)
		appErr := assertAppErrorCode(t, err, constants.CodeCaseCloseBlocked)
		details := assertCloseBlockDetails(t, appErr)
		if len(details.MissingMaterials) != 1 || details.MissingMaterials[0] != dto.CloseRequirementJudgment {
			t.Fatalf("role=%s missing_materials = %v, want [judgment]", role, details.MissingMaterials)
		}
		if details.PendingBillingCount != 2 {
			t.Fatalf("role=%s pending count = %d, want 2", role, details.PendingBillingCount)
		}
		if details.PendingBillingAmount != 1800.50 {
			t.Fatalf("role=%s pending amount = %v, want 1800.50", role, details.PendingBillingAmount)
		}
	}

	// 案件状态、结案日期均未变动。
	c := reloadCase(t, db, caseID)
	if c.Status != constants.CaseStatusHearing {
		t.Fatalf("case status changed to %s, want hearing", c.Status)
	}
	if c.CloseDate != nil {
		t.Fatalf("close_date should stay nil, got %v", c.CloseDate)
	}
	// 账单也没有被动过。
	var pending int64
	db.Model(&model.Billing{}).Where("case_id = ? AND status = ?", caseID, constants.BillingStatusPending).Count(&pending)
	if pending != 2 {
		t.Fatalf("pending billings = %d, want 2", pending)
	}
}

// 只缺判决书：返回材料缺失，待支付笔数为 0。
func TestCloseBlocked_MissingJudgmentOnly(t *testing.T) {
	db, caseSvc, documentSvc, billingSvc := newTestServices(t)
	caseID := seedHearingCase(t, db)
	if _, err := billingSvc.Create(caseID, 1, constants.BillingTypeAttorneyFee, 1000, ""); err != nil {
		t.Fatalf("create billing: %v", err)
	}
	if _, err := billingSvc.MarkPaid(1); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	// 上传一份非判决书文档，不应被当作判决书。
	if _, err := documentSvc.Create(caseID, 1, "证据材料", constants.DocTypeEvidence, "/tmp/e.pdf"); err != nil {
		t.Fatalf("create evidence: %v", err)
	}

	_, err := caseSvc.ChangeStatus(caseID, constants.RoleAdmin, constants.CaseStatusClosed)
	appErr := assertAppErrorCode(t, err, constants.CodeCaseCloseBlocked)
	details := assertCloseBlockDetails(t, appErr)
	if len(details.MissingMaterials) != 1 || details.MissingMaterials[0] != dto.CloseRequirementJudgment {
		t.Fatalf("missing_materials = %v, want [judgment]", details.MissingMaterials)
	}
	if details.PendingBillingCount != 0 || details.PendingBillingAmount != 0 {
		t.Fatalf("pending stats = (%d,%v), want (0,0)", details.PendingBillingCount, details.PendingBillingAmount)
	}

	c := reloadCase(t, db, caseID)
	if c.Status != constants.CaseStatusHearing || c.CloseDate != nil {
		t.Fatalf("case mutated after rejection: status=%s close_date=%v", c.Status, c.CloseDate)
	}
}

// 只有待支付账单：判决书已齐，账单笔数与金额体现在明细中。
func TestCloseBlocked_PendingBillingsOnly(t *testing.T) {
	db, caseSvc, documentSvc, billingSvc := newTestServices(t)
	caseID := seedHearingCase(t, db)
	if _, err := documentSvc.Create(caseID, 1, "一审判决书", constants.DocTypeJudgment, "/tmp/j.pdf"); err != nil {
		t.Fatalf("create judgment: %v", err)
	}
	if _, err := billingSvc.Create(caseID, 1, constants.BillingTypeTravelFee, 299.99, ""); err != nil {
		t.Fatalf("create billing: %v", err)
	}

	_, err := caseSvc.ChangeStatus(caseID, constants.RoleAdmin, constants.CaseStatusClosed)
	appErr := assertAppErrorCode(t, err, constants.CodeCaseCloseBlocked)
	details := assertCloseBlockDetails(t, appErr)
	if len(details.MissingMaterials) != 0 {
		t.Fatalf("missing_materials = %v, want empty", details.MissingMaterials)
	}
	if details.PendingBillingCount != 1 || details.PendingBillingAmount != 299.99 {
		t.Fatalf("pending stats = (%d,%v), want (1,299.99)", details.PendingBillingCount, details.PendingBillingAmount)
	}
}

// 两条都满足：允许结案并写入结案日期。
func TestCloseAllowed_WhenBothSatisfied(t *testing.T) {
	db, caseSvc, documentSvc, billingSvc := newTestServices(t)
	caseID := seedHearingCase(t, db)
	if _, err := documentSvc.Create(caseID, 1, "判决书", constants.DocTypeJudgment, "/tmp/j.pdf"); err != nil {
		t.Fatalf("create judgment: %v", err)
	}
	if _, err := billingSvc.Create(caseID, 1, constants.BillingTypeAttorneyFee, 500, ""); err != nil {
		t.Fatalf("create billing: %v", err)
	}
	if _, err := billingSvc.MarkPaid(1); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	// 作废账单不阻塞结案（不是 pending）。
	if _, err := billingSvc.Create(caseID, 1, constants.BillingTypeOther, 10, ""); err != nil {
		t.Fatalf("create billing: %v", err)
	}
	if _, err := billingSvc.Void(2); err != nil {
		t.Fatalf("void billing: %v", err)
	}

	c, err := caseSvc.ChangeStatus(caseID, constants.RoleLawyer, constants.CaseStatusClosed)
	if err != nil {
		t.Fatalf("close should succeed: %v", err)
	}
	if c.Status != constants.CaseStatusClosed || c.CloseDate == nil {
		t.Fatalf("case not closed properly: status=%s close_date=%v", c.Status, c.CloseDate)
	}

	check, err := caseSvc.CloseCheck(caseID)
	if err != nil {
		t.Fatalf("close check: %v", err)
	}
	if !check.CanClose || len(check.BlockingReasons) != 0 || len(check.MissingMaterials) != 0 {
		t.Fatalf("unexpected check result: %+v", check)
	}
}

// 已结案案件重复提交 closed 不再触发前置校验（幂等，无判决书的历史案件不会报错）。
func TestCloseIdempotent_AlreadyClosed(t *testing.T) {
	db, caseSvc, _, _ := newTestServices(t)
	c := &model.Case{CaseNo: "TC-CLOSE-2", Title: "已结案", CaseType: constants.CaseTypeCivil,
		Status: constants.CaseStatusClosed, ClientID: 1, LeadLawyerID: 1}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("create closed case: %v", err)
	}
	got, err := caseSvc.ChangeStatus(c.ID, constants.RoleAdmin, constants.CaseStatusClosed)
	if err != nil {
		t.Fatalf("re-close should be allowed: %v", err)
	}
	if got.Status != constants.CaseStatusClosed {
		t.Fatalf("status = %s, want closed", got.Status)
	}
}
