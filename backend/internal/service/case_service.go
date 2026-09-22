package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"cylawcase/internal/constants"
	"cylawcase/internal/model"
	"cylawcase/internal/repository"
	"cylawcase/internal/util"
)

// CaseService 案件业务逻辑。
type CaseService struct {
	repo         *repository.CaseRepository
	clientRepo   *repository.ClientRepository
	userRepo     *repository.UserRepository
	documentRepo *repository.DocumentRepository
	billingRepo  *repository.BillingRepository
	logger       *slog.Logger
}

// NewCaseService 构造案件服务。
func NewCaseService(repo *repository.CaseRepository, clientRepo *repository.ClientRepository,
	userRepo *repository.UserRepository, documentRepo *repository.DocumentRepository,
	billingRepo *repository.BillingRepository, logger *slog.Logger) *CaseService {
	return &CaseService{repo: repo, clientRepo: clientRepo, userRepo: userRepo,
		documentRepo: documentRepo, billingRepo: billingRepo, logger: logger}
}

// CaseCloseBlockInfo 结案校验未通过时的阻塞详情。
type CaseCloseBlockInfo struct {
	MissingMaterials []string `json:"missing_materials"`
	PendingBillCount int64    `json:"pending_bill_count"`
	PendingBillTotal float64  `json:"pending_bill_total"`
}

// Blocked 是否存在阻塞项。
func (b *CaseCloseBlockInfo) Blocked() bool {
	return len(b.MissingMaterials) > 0 || b.PendingBillCount > 0
}

// Message 生成可读的阻塞原因说明。
func (b *CaseCloseBlockInfo) Message() string {
	var parts []string
	if len(b.MissingMaterials) > 0 {
		names := make([]string, 0, len(b.MissingMaterials))
		for _, m := range b.MissingMaterials {
			names = append(names, constants.DocTypeText[m])
		}
		parts = append(parts, "缺少"+strings.Join(names, "、"))
	}
	if b.PendingBillCount > 0 {
		parts = append(parts, fmt.Sprintf("待支付账单 %d 笔，合计 ¥%s", b.PendingBillCount, util.FormatAmount(b.PendingBillTotal)))
	}
	return constants.MsgCaseCloseBlocked + "：" + strings.Join(parts, "；")
}

// Create 创建案件。
func (s *CaseService) Create(clientID, leadLawyerID uint64, title, caseType, summary string,
	acceptDate *time.Time, coLawyerIDs []uint64) (*model.Case, error) {
	if !constants.IsValidCaseType(caseType) {
		return nil, util.NewAppError(constants.CodeValidationFailed, "Case[case_type="+caseType+"] create: invalid type")
	}
	if _, err := s.clientRepo.FindByID(clientID); err != nil {
		return nil, util.Wrap(err, "Case[client_id=%d] create: client not found", clientID)
	}
	if _, err := s.userRepo.FindByID(leadLawyerID); err != nil {
		return nil, util.Wrap(err, "Case[lead_lawyer_id=%d] create: lawyer not found", leadLawyerID)
	}
	co := jsonCoLawyers(coLawyerIDs)
	c := &model.Case{
		CaseNo:       genCaseNo(),
		Title:        title,
		CaseType:     caseType,
		Status:       constants.CaseStatusFiled,
		AcceptDate:   acceptDate,
		Summary:      summary,
		ClientID:     clientID,
		LeadLawyerID: leadLawyerID,
		CoLawyerIDs:  co,
	}
	if err := s.repo.Create(c); err != nil {
		s.logger.Error(constants.LogCaseCreateFailed, "error", err.Error())
		return nil, util.Wrap(err, "Case[title=%s] create failed", title)
	}
	s.logger.Info(constants.LogCaseCreateSuccess, "case_id", c.ID, "case_no", c.CaseNo)
	return c, nil
}

// Update 更新案件信息。
func (s *CaseService) Update(id uint64, title, summary string, coLawyerIDs []uint64) (*model.Case, error) {
	c, err := s.repo.FindByID(id)
	if err != nil {
		return nil, util.Wrap(err, "Case[id=%d] update find failed", id)
	}
	if title != "" {
		c.Title = title
	}
	if summary != "" {
		c.Summary = summary
	}
	if coLawyerIDs != nil {
		c.CoLawyerIDs = jsonCoLawyers(coLawyerIDs)
	}
	if err := s.repo.Update(c); err != nil {
		return nil, util.Wrap(err, "Case[id=%d] update save failed", id)
	}
	s.logger.Info(constants.LogCaseUpdateSuccess, "case_id", c.ID)
	return c, nil
}

// ChangeStatus 案件状态流转。
func (s *CaseService) ChangeStatus(id uint64, operatorRole string, status string) (*model.Case, error) {
	c, err := s.repo.FindByID(id)
	if err != nil {
		return nil, util.Wrap(err, "Case[id=%d] status change find failed", id)
	}
	if !constants.IsValidCaseStatus(status) {
		return nil, util.NewAppError(constants.CodeValidationFailed, "Case[id="+u64(id)+"] status invalid: "+status)
	}
	if operatorRole != constants.RoleAdmin && !canFlow(c.Status, status) {
		return nil, util.NewAppError(constants.CodeCaseStatusConflict, "Case[id="+u64(id)+"] status conflict: "+c.Status+" -> "+status)
	}
	// 结案前置校验：对所有角色（含管理员）生效，任一条件不满足则整次拒绝，不改动任何数据。
	if status == constants.CaseStatusClosed && c.Status != constants.CaseStatusClosed {
		block, err := s.closeBlockInfo(id)
		if err != nil {
			return nil, util.Wrap(err, "Case[id=%d] close check failed", id)
		}
		if block.Blocked() {
			s.logger.Warn(constants.LogCaseCloseBlocked, "case_id", id,
				"missing_materials", block.MissingMaterials, "pending_bill_count", block.PendingBillCount)
			return nil, util.NewAppErrorWithData(constants.CodeCaseCloseBlocked, block.Message(), block)
		}
	}
	c.Status = status
	if status == constants.CaseStatusClosed && c.CloseDate == nil {
		now := time.Now()
		c.CloseDate = &now
	}
	if err := s.repo.Update(c); err != nil {
		s.logger.Error(constants.LogCaseStatusChangeFailed, "error", err.Error())
		return nil, util.Wrap(err, "Case[id=%d] status change save failed", id)
	}
	s.logger.Info(constants.LogCaseStatusChangeSuccess, "case_id", c.ID, "status", status)
	return c, nil
}

// Assign 分配主办律师。
func (s *CaseService) Assign(id, leadLawyerID uint64, coLawyerIDs []uint64) (*model.Case, error) {
	c, err := s.repo.FindByID(id)
	if err != nil {
		return nil, util.Wrap(err, "Case[id=%d] assign find failed", id)
	}
	if _, err := s.userRepo.FindByID(leadLawyerID); err != nil {
		return nil, util.Wrap(err, "Case[id=%d] assign failed: lawyer not match", id)
	}
	c.LeadLawyerID = leadLawyerID
	if coLawyerIDs != nil {
		c.CoLawyerIDs = jsonCoLawyers(coLawyerIDs)
	}
	if err := s.repo.Update(c); err != nil {
		s.logger.Error(constants.LogCaseAssignFailed, "error", err.Error())
		return nil, util.Wrap(err, "Case[id=%d] assign save failed", id)
	}
	s.logger.Info(constants.LogCaseAssignSuccess, "case_id", c.ID, "lead_lawyer_id", leadLawyerID)
	return c, nil
}

// List 分页查询案件。
func (s *CaseService) List(page, pageSize int, caseType, status string, lawyerID uint64, startDate, endDate *time.Time) ([]model.Case, int64, error) {
	return s.repo.List(page, pageSize, caseType, status, lawyerID, startDate, endDate)
}

// Get 案件详情。
func (s *CaseService) Get(id uint64) (*model.Case, error) {
	return s.repo.FindByID(id)
}

// closeBlockInfo 汇总结案阻塞项：缺少判决书、存在待支付账单。
func (s *CaseService) closeBlockInfo(caseID uint64) (*CaseCloseBlockInfo, error) {
	info := &CaseCloseBlockInfo{MissingMaterials: []string{}}
	judgmentCount, err := s.documentRepo.CountByCaseAndType(caseID, constants.DocTypeJudgment)
	if err != nil {
		return nil, err
	}
	if judgmentCount == 0 {
		info.MissingMaterials = append(info.MissingMaterials, constants.DocTypeJudgment)
	}
	pendingCount, pendingTotal, err := s.billingRepo.PendingStatsByCase(caseID)
	if err != nil {
		return nil, err
	}
	info.PendingBillCount = pendingCount
	info.PendingBillTotal = pendingTotal
	return info, nil
}

// canFlow 案件状态机：filed->investigating->hearing->closed->archived，允许回退到上一步。
func canFlow(from, to string) bool {
	idx := map[string]int{constants.CaseStatusFiled: 0, constants.CaseStatusInvestigating: 1,
		constants.CaseStatusHearing: 2, constants.CaseStatusClosed: 3, constants.CaseStatusArchived: 4}
	a, okA := idx[from]
	b, okB := idx[to]
	if !okA || !okB {
		return false
	}
	return b == a+1 || b == a-1 || b == a
}

func jsonCoLawyers(ids []uint64) model.CoLawyerJSON {
	raw, _ := json.Marshal(ids)
	return model.CoLawyerJSON(raw)
}

func genCaseNo() string {
	return fmt.Sprintf("CY%d%04d", time.Now().Year(), time.Now().UnixNano()%10000)
}

func u64(v uint64) string {
	return fmt.Sprintf("%d", v)
}
