package model

import "time"

type ChatAnalyzerMessageStatus string

const (
	ChatAnalyzerMessagePending   ChatAnalyzerMessageStatus = "pending"
	ChatAnalyzerMessageApplied   ChatAnalyzerMessageStatus = "applied"
	ChatAnalyzerMessageIgnored   ChatAnalyzerMessageStatus = "ignored"
	ChatAnalyzerMessageUncertain ChatAnalyzerMessageStatus = "uncertain"
	ChatAnalyzerMessageFailed    ChatAnalyzerMessageStatus = "failed"
	ChatAnalyzerMessageDryRun    ChatAnalyzerMessageStatus = "dry_run"
)

type ChatAnalyzerMessage struct {
	ID               string                    `json:"id"`
	ChatID           string                    `json:"chatId"`
	MessageID        int64                     `json:"messageId"`
	SenderID         int64                     `json:"senderId"`
	SenderStableID   string                    `json:"senderStableId"`
	SenderNickname   string                    `json:"senderNickname"`
	Text             string                    `json:"text"`
	MessageDate      time.Time                 `json:"messageDate"`
	ReceivedAt       time.Time                 `json:"receivedAt"`
	ReplyToMessageID int64                     `json:"replyToMessageId,omitempty"`
	Status           ChatAnalyzerMessageStatus `json:"status"`
	Attempts         int                       `json:"attempts"` // Durable state writes; logical retry attempts are carried in AnalysisJSON.
	AnalysisJSON     string                    `json:"analysisJson,omitempty"`
	AppliedActionID  string                    `json:"appliedActionId,omitempty"`
	AppliedTargetKey string                    `json:"appliedTargetKey,omitempty"`
	BatchID          string                    `json:"batchId,omitempty"`
	LastError        string                    `json:"lastError,omitempty"`
	ProcessedAt      time.Time                 `json:"processedAt,omitempty"`
}

type ChatAnalyzerBatchStatus string

const (
	ChatAnalyzerBatchRunning   ChatAnalyzerBatchStatus = "running"
	ChatAnalyzerBatchCompleted ChatAnalyzerBatchStatus = "completed"
	ChatAnalyzerBatchFailed    ChatAnalyzerBatchStatus = "failed"
)

type ChatAnalyzerBatch struct {
	ID            string                  `json:"id"`
	Status        ChatAnalyzerBatchStatus `json:"status"`
	DryRun        bool                    `json:"dryRun"`
	StartedAt     time.Time               `json:"startedAt"`
	FinishedAt    time.Time               `json:"finishedAt,omitempty"`
	MessageCount  int                     `json:"messageCount"`
	ReportCount   int                     `json:"reportCount"`
	VoteCount     int                     `json:"voteCount"`
	IgnoredCount  int                     `json:"ignoredCount"`
	WouldApply    int                     `json:"wouldApply"`
	AppliedCount  int                     `json:"appliedCount"`
	ErrorCount    int                     `json:"errorCount"`
	Model         string                  `json:"model"`
	SelectedModel string                  `json:"selectedModel,omitempty"`
	ResultJSON    string                  `json:"resultJson,omitempty"`
	Error         string                  `json:"error,omitempty"`
}

func ChatAnalyzerStableID(senderID int64) string {
	if senderID == 0 {
		return TelegramStableID(0)
	}
	return TelegramStableID(senderID)
}

func ChatAnalyzerReporterUserID(senderID int64) (int64, bool) {
	if senderID <= 0 {
		return 0, false
	}
	return senderID, true
}

func ChatAnalyzerReporterNickname(senderID int64) string {
	userID, ok := ChatAnalyzerReporterUserID(senderID)
	if !ok {
		return GenericNickname(0)
	}
	return GenericNickname(userID)
}
