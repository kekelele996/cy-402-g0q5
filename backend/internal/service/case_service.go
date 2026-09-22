package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"cylawcase/internal/constants"
	"cylawcase/internal/dto"
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
	return &CaseService{
		repo:         repo,
		clientRepo:   clientRepo,
		userRepo:     userRepo,
		documentRepo: documentRepo,
		billingRepo:  billingRepo,
		logger:       logger,
	}
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
	// 进入结案状态前必须通过两条前置校验：已上传判决书、无待支付账单。
	// 管理员同样不能豁免；被拒时不写库，案件状态、结案日期与账单均保持不变。
	if status == constants.CaseStatusClosed && c.Status != constants.CaseStatusClosed {
		check, err := s.CloseCheck(id)
		if err != nil {
			return nil, err
		}
		if !check.CanClose {
			details := &dto.CloseBlockDetails{
				MissingMaterials:     check.MissingMaterials,
				PendingBillingCount:  check.PendingBillingCount,
				PendingBillingAmount: check.PendingBillingAmount,
			}
			return nil, util.NewAppErrorWithDetails(constants.CodeCaseCloseBlocked,
				constants.MsgCaseCloseBlocked+"："+joinReasons(check.BlockingReasons), details)
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

// CloseCheck 结案前置校验：检查判决书是否已上传、名下是否存在待支付账单。
// 该方法只读，不改变案件状态、结案日期与任何账单。
func (s *CaseService) CloseCheck(id uint64) (*dto.CloseCheckResult, error) {
	if _, err := s.repo.FindByID(id); err != nil {
		return nil, util.Wrap(err, "Case[id=%d] close check find failed", id)
	}
	judgmentCount, err := s.documentRepo.CountByCaseAndType(id, constants.DocTypeJudgment)
	if err != nil {
		return nil, util.Wrap(err, "Case[id=%d] close check: count judgment failed", id)
	}
	pendingCount, pendingTotal, err := s.billingRepo.CountAndSumPendingByCase(id)
	if err != nil {
		return nil, util.Wrap(err, "Case[id=%d] close check: count pending billings failed", id)
	}
	pendingTotal = roundAmount(pendingTotal)
	return buildCloseCheckResult(judgmentCount > 0, pendingCount, pendingTotal), nil
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

// buildCloseCheckResult 根据两项检查的原始结果组装结案校验结论与阻塞文案。
func buildCloseCheckResult(hasJudgment bool, pendingCount int64, pendingTotal float64) *dto.CloseCheckResult {
	result := &dto.CloseCheckResult{
		HasJudgment:          hasJudgment,
		PendingBillingCount:  pendingCount,
		PendingBillingAmount: pendingTotal,
	}
	if !hasJudgment {
		result.MissingMaterials = append(result.MissingMaterials, dto.CloseRequirementJudgment)
		result.BlockingReasons = append(result.BlockingReasons, "缺少材料：判决书")
	}
	if pendingCount > 0 {
		result.BlockingReasons = append(result.BlockingReasons,
			fmt.Sprintf("存在 %d 笔待支付账单，金额合计 %s 元", pendingCount, util.FormatAmount(pendingTotal)))
	}
	result.CanClose = len(result.BlockingReasons) == 0
	return result
}

// roundAmount 金额保留两位小数，避免数据库 numeric 聚合产生浮点尾差。
func roundAmount(v float64) float64 {
	return math.Round(v*100) / 100
}

// joinReasons 拼接阻塞原因文案。
func joinReasons(reasons []string) string {
	out := ""
	for i, r := range reasons {
		if i > 0 {
			out += "；"
		}
		out += r
	}
	return out
}
