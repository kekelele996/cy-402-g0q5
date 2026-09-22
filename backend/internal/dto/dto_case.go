package dto

import "time"

// CaseCreateRequest 创建案件请求。
type CaseCreateRequest struct {
	ClientID     uint64   `json:"client_id" binding:"required"`
	LeadLawyerID uint64   `json:"lead_lawyer_id" binding:"required"`
	Title        string   `json:"title" binding:"required,max=200"`
	CaseType     string   `json:"case_type" binding:"required,oneof=civil criminal administrative commercial labor"`
	Summary      string   `json:"summary"`
	AcceptDate   *string  `json:"accept_date"`
	CoLawyerIDs  []uint64 `json:"co_lawyer_ids"`
}

// CaseUpdateRequest 更新案件请求。
type CaseUpdateRequest struct {
	Title       string   `json:"title" binding:"max=200"`
	Summary     string   `json:"summary"`
	CoLawyerIDs []uint64 `json:"co_lawyer_ids"`
}

// CaseStatusRequest 状态流转请求。
type CaseStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=filed investigating hearing closed archived"`
}

// CaseAssignRequest 分配律师请求。
type CaseAssignRequest struct {
	LeadLawyerID uint64   `json:"lead_lawyer_id" binding:"required"`
	CoLawyerIDs  []uint64 `json:"co_lawyer_ids"`
}

// ParseAcceptDate 解析接受日期字符串。
func ParseAcceptDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CloseRequirement 结案前置条件标识。
const (
	CloseRequirementJudgment         = "judgment"           // 已上传判决书
	CloseRequirementNoPendingBilling = "no_pending_billing" // 无待支付账单
)

// CloseCheckResult 结案前置校验结果。
type CloseCheckResult struct {
	// CanClose 两条前置条件是否全部满足。
	CanClose bool `json:"can_close"`
	// HasJudgment 案件是否已上传判决书。
	HasJudgment bool `json:"has_judgment"`
	// MissingMaterials 缺少的材料类型列表（目前仅可能出现 judgment）。
	MissingMaterials []string `json:"missing_materials"`
	// PendingBillingCount 待支付账单笔数。
	PendingBillingCount int64 `json:"pending_billing_count"`
	// PendingBillingAmount 待支付账单金额合计。
	PendingBillingAmount float64 `json:"pending_billing_amount"`
	// BlockingReasons 面向前端展示的阻塞原因文案列表。
	BlockingReasons []string `json:"blocking_reasons"`
}

// CloseBlockDetails 结案被拒时返回的结构化明细。
type CloseBlockDetails struct {
	// MissingMaterials 缺少的材料类型列表（如 ["judgment"]）。
	MissingMaterials []string `json:"missing_materials"`
	// PendingBillingCount 待支付账单笔数。
	PendingBillingCount int64 `json:"pending_billing_count"`
	// PendingBillingAmount 待支付账单金额合计。
	PendingBillingAmount float64 `json:"pending_billing_amount"`
}
