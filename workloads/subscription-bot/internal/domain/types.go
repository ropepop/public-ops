package domain

import "time"

const (
	RoleMember   = "member"
	RoleOperator = "operator"

	PlanStatusActive = "active"

	MembershipInvited        = "invited"
	MembershipPendingPayment = "pending_payment"
	MembershipActive         = "active"
	MembershipGrace          = "grace"
	MembershipSuspended      = "suspended"
	MembershipRemoved        = "removed"

	InvoiceDraft     = "draft"
	InvoiceOpen      = "open"
	InvoiceDetected  = "detected"
	InvoiceConfirmed = "confirmed"
	InvoiceUnderpaid = "underpaid"
	InvoiceExpired   = "expired"
	InvoiceCancelled = "cancelled"

	CreditAvailable = "available"
	CreditApplied   = "applied"

	TicketOpen     = "open"
	TicketResolved = "resolved"
)

type User struct {
	ID         int64
	TelegramID int64
	Username   string
	Role       string
	Status     string
	CreatedAt  time.Time
}

type ServiceCatalogEntry struct {
	ServiceCode       string
	DisplayName       string
	Category          string
	SharingPolicyNote string
	AccessMode        string
	Status            string
}

type Plan struct {
	ID                string
	OwnerUserID       int64
	OwnerTelegramID   int64
	ServiceCode       string
	ServiceName       string
	Category          string
	TotalPriceMinor   int64
	PerSeatBaseMinor  int64
	PlatformFeeBps    int
	StableAsset       string
	BillingPeriod     string
	RenewalDate       time.Time
	SeatLimit         int
	AccessMode        string
	SharingPolicyNote string
	Status            string
	CreatedAt         time.Time
}

type PlanInvite struct {
	ID              string
	PlanID          string
	InviteCode      string
	CreatedByUserID int64
	Status          string
	CreatedAt       time.Time
}

type Membership struct {
	ID              string
	PlanID          string
	UserID          int64
	UserTelegramID  int64
	Username        string
	SeatStatus      string
	JoinedAt        time.Time
	GraceUntil      *time.Time
	RemovedAt       *time.Time
	LatestInvoiceID string
}

type Invoice struct {
	ID                 string
	MembershipID       string
	PlanID             string
	UserID             int64
	UserTelegramID     int64
	CycleStart         time.Time
	CycleEnd           time.Time
	DueAt              time.Time
	BaseMinor          int64
	FeeMinor           int64
	TotalMinor         int64
	CreditAppliedMinor int64
	PaidMinor          int64
	AnchorAsset        string
	PayAsset           string
	Network            string
	QuotedPayAmount    string
	QuoteRateLabel     string
	QuoteExpiresAt     *time.Time
	PaymentRef         string
	ProviderInvoiceID  string
	Status             string
	TxHash             string
	ReminderMask       int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (i Invoice) AmountDueMinor() int64 {
	due := i.TotalMinor - i.CreditAppliedMinor
	if due < 0 {
		return 0
	}
	return due
}

type Payment struct {
	ID               string
	InvoiceID        string
	AmountReceived   int64
	Asset            string
	Network          string
	TxHash           string
	Confirmations    int
	ReceivedAt       time.Time
	SettlementStatus string
}

type Credit struct {
	ID               string
	UserID           int64
	PlanID           string
	InvoiceID        string
	AmountMinor      int64
	RemainingMinor   int64
	Status           string
	Note             string
	CreatedAt        time.Time
	AppliedInvoiceID string
}

type SupportTicket struct {
	ID        string
	PlanID    string
	UserID    int64
	Subject   string
	Body      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SupportTicketView struct {
	Ticket          SupportTicket
	PlanServiceName string
	Username        string
	UserTelegramID  int64
	LatestInvoiceID string
	LatestDueMinor  int64
	LatestPaidMinor int64
	LatestStatus    string
}

type RenewalIssue struct {
	Kind            string
	PlanID          string
	PlanServiceName string
	MembershipID    string
	UserID          int64
	UserTelegramID  int64
	Username        string
	SeatStatus      string
	InvoiceID       string
	InvoiceStatus   string
	DueAt           *time.Time
	AmountDueMinor  int64
	PaidMinor       int64
}

type BotConversationState struct {
	UserID      int64
	TelegramID  int64
	Flow        string
	Step        string
	PayloadJSON string
	ExpiresAt   time.Time
	UpdatedAt   time.Time
}

type ProviderEvent struct {
	ID                int64
	ProviderName      string
	ExternalEventID   string
	EventType         string
	ProviderInvoiceID string
	PayloadJSON       string
	CreatedAt         time.Time
}

type DenylistEntry struct {
	ID              int64
	EntryType       string
	EntryValue      string
	Reason          string
	CreatedByUserID int64
	CreatedAt       time.Time
}

type PaymentAlert struct {
	EventName       string
	EntityID        string
	ProviderName    string
	ProviderInvoice string
	Detail          string
	CreatedAt       time.Time
}

type OwnerReimbursementSummary struct {
	OwnerUserID     int64
	OwnerTelegramID int64
	Username        string
	AmountMinor     int64
}

type Event struct {
	ID          int64
	EntityType  string
	EntityID    string
	EventName   string
	PayloadJSON string
	CreatedAt   time.Time
}

type Notification struct {
	TelegramID int64
	Message    string
}

type PlanView struct {
	Plan           Plan
	Membership     *Membership
	OpenInvoice    *Invoice
	MemberCount    int
	AvailableSeats int
	IsOwner        bool
}

type Ledger struct {
	Plan     Plan
	Invoices []Invoice
	Payments []Payment
	Credits  []Credit
	Events   []Event
}

type AdminOverview struct {
	UsersTotal          int
	PlansTotal          int
	OpenInvoicesTotal   int
	FailedRenewalsTotal int
	SupportOpenTotal    int
	PaymentAlertsTotal  int
	BlockedActorsTotal  int
	PayoutDueMinor      int64
}
