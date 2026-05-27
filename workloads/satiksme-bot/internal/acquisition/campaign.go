package acquisition

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type CandidateSource string

const (
	SourceRecentActive CandidateSource = "recent_active"
	SourceMemberList   CandidateSource = "member_list"
)

type CandidateStatus string

const (
	StatusCandidate CandidateStatus = "candidate"
	StatusDrafted   CandidateStatus = "drafted"
	StatusSkipped   CandidateStatus = "skipped"
	StatusContacted CandidateStatus = "contacted"
	StatusConsented CandidateStatus = "consented"
	StatusDeclined  CandidateStatus = "declined"
	StatusStopped   CandidateStatus = "stopped"
	StatusGranted   CandidateStatus = "granted"
)

type Candidate struct {
	UserID        int64           `json:"userId"`
	AccessHash    int64           `json:"accessHash,omitempty"`
	Username      string          `json:"username,omitempty"`
	DisplayName   string          `json:"displayName,omitempty"`
	Language      string          `json:"language,omitempty"`
	Source        CandidateSource `json:"source"`
	LastActiveAt  time.Time       `json:"lastActiveAt,omitempty"`
	LastMessageID int64           `json:"lastMessageId,omitempty"`
	LastReplyID   int64           `json:"lastReplyMessageId,omitempty"`
	Status        CandidateStatus `json:"status,omitempty"`
	StopReason    string          `json:"stopReason,omitempty"`
}

type BatchOptions struct {
	Now                   time.Time
	AlreadyContactedToday int
	DailyLimit            int
}

type DraftOptions struct {
	DailyRegistrations int
	GroupName          string
}

type Draft struct {
	UserID   int64  `json:"userId"`
	Username string `json:"username,omitempty"`
	Language string `json:"language"`
	Text     string `json:"text"`
}

type ReplyAction string

const (
	ReplyNeutral    ReplyAction = "neutral"
	ReplyConsent    ReplyAction = "consent"
	ReplyDecline    ReplyAction = "decline"
	ReplyUnsafeStop ReplyAction = "unsafe_stop"
)

type ReplyDecision struct {
	Action         ReplyAction `json:"action"`
	Reason         string      `json:"reason,omitempty"`
	CanGrantAccess bool        `json:"canGrantAccess"`
	AlertAdmin     bool        `json:"alertAdmin"`
}

func SelectDailyBatch(candidates []Candidate, opts BatchOptions) []Candidate {
	dailyLimit := opts.DailyLimit
	if dailyLimit <= 0 {
		dailyLimit = 10
	}
	remaining := dailyLimit - opts.AlreadyContactedToday
	if remaining <= 0 {
		return nil
	}

	eligible := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !eligibleForFirstContact(candidate) {
			continue
		}
		eligible = append(eligible, normalizeCandidate(candidate))
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left := eligible[i]
		right := eligible[j]
		leftRank := sourceRank(left.Source)
		rightRank := sourceRank(right.Source)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if !left.LastActiveAt.Equal(right.LastActiveAt) {
			return left.LastActiveAt.After(right.LastActiveAt)
		}
		if left.LastMessageID != right.LastMessageID {
			return left.LastMessageID > right.LastMessageID
		}
		return candidateKey(left) < candidateKey(right)
	})
	if len(eligible) > remaining {
		eligible = eligible[:remaining]
	}
	return eligible
}

func DraftFirstContact(candidate Candidate, opts DraftOptions) Draft {
	registrations := opts.DailyRegistrations
	if registrations <= 0 {
		registrations = 4
	}
	group := strings.TrimSpace(opts.GroupName)
	if group == "" {
		group = "Rīgas Zaķi"
	}
	language := normalizeLanguage(candidate.Language)
	return Draft{
		UserID:   candidate.UserID,
		Username: cleanUsername(candidate.Username),
		Language: language,
		Text:     firstContactText(language, group, registrations),
	}
}

func ClassifyReply(text string) ReplyDecision {
	normalized := normalizeText(text)
	if normalized == "" {
		return ReplyDecision{Action: ReplyNeutral}
	}
	if containsAny(normalized, unsafeReplyNeedles) {
		return ReplyDecision{
			Action:     ReplyUnsafeStop,
			Reason:     "unsafe_or_secret_seeking_reply",
			AlertAdmin: true,
		}
	}
	if containsWordOrPhrase(normalized, declineNeedles) {
		return ReplyDecision{Action: ReplyDecline, Reason: "declined"}
	}
	if containsWordOrPhrase(normalized, consentNeedles) {
		return ReplyDecision{Action: ReplyConsent, Reason: "clear_consent", CanGrantAccess: true}
	}
	return ReplyDecision{Action: ReplyNeutral}
}

func GrantCommand(candidate Candidate, dailyRegistrations int) string {
	limit := dailyRegistrations
	if limit <= 0 {
		limit = 4
	}
	if username := cleanUsername(candidate.Username); username != "" {
		return fmt.Sprintf("/admin add @%s %d", username, limit)
	}
	return fmt.Sprintf("/admin add %d %d", candidate.UserID, limit)
}

func InferLanguage(samples ...string) string {
	var cyrillic, latvian int
	for _, sample := range samples {
		for _, r := range sample {
			switch {
			case unicode.In(r, unicode.Cyrillic):
				cyrillic++
			case strings.ContainsRune("āčēģīķļņšūžĀČĒĢĪĶĻŅŠŪŽ", r):
				latvian++
			}
		}
	}
	if cyrillic > 0 {
		return "ru"
	}
	if latvian > 0 {
		return "lv"
	}
	return "lv"
}

func eligibleForFirstContact(candidate Candidate) bool {
	if candidate.UserID <= 0 {
		return false
	}
	switch candidate.Status {
	case "", StatusCandidate, StatusDrafted:
		return true
	default:
		return false
	}
}

func normalizeCandidate(candidate Candidate) Candidate {
	candidate.Username = cleanUsername(candidate.Username)
	if candidate.Status == "" {
		candidate.Status = StatusCandidate
	}
	if candidate.Language == "" {
		candidate.Language = "lv"
	}
	if candidate.Source == "" {
		candidate.Source = SourceMemberList
	}
	return candidate
}

func sourceRank(source CandidateSource) int {
	switch source {
	case SourceRecentActive:
		return 0
	default:
		return 1
	}
}

func candidateKey(candidate Candidate) string {
	if username := cleanUsername(candidate.Username); username != "" {
		return "@" + username
	}
	return strconv.FormatInt(candidate.UserID, 10)
}

func cleanUsername(username string) string {
	clean := strings.TrimSpace(username)
	clean = strings.TrimPrefix(clean, "@")
	return strings.Trim(clean, " ")
}

func normalizeLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "ru", "rus", "russian":
		return "ru"
	default:
		return "lv"
	}
}

func firstContactText(language string, group string, registrations int) string {
	switch normalizeLanguage(language) {
	case "ru":
		return fmt.Sprintf("Привет! Пишу, потому что ты в группе %s. Приглашаю попробовать @rs_bilete_bot: отправляешь 5-значный код из приложения Rīgas satiksme, бот возвращает QR. Сейчас доступ бесплатно на %d регистрации транспорта в день. Если хочешь, ответь «да», и я добавлю доступ.", group, registrations)
	default:
		return fmt.Sprintf("Čau! Rakstu, jo esi %s grupā. Aicinu izmēģināt @rs_bilete_bot: nosūti 5 ciparu kodu no Rīgas satiksme lietotnes, un bots atsūta QR. Tagad piekļuve ir bez maksas ar %d transporta reģistrācijām dienā. Ja gribi, atbildi “jā” un pievienošu piekļuvi.", group, registrations)
	}
}

func normalizeText(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer("“", "\"", "”", "\"", "«", "\"", "»", "\"", "\n", " ", "\t", " ")
	return strings.Join(strings.Fields(replacer.Replace(lower)), " ")
}

var unsafeReplyNeedles = []string{
	"ignore previous",
	"ignore instructions",
	"system prompt",
	"developer message",
	"hidden instruction",
	"jailbreak",
	"owner",
	"other account",
	"citu account",
	"api key",
	"api hash",
	"token",
	"session",
	"secret",
	"slepen",
	"parādi prompt",
	"покажи промпт",
	"секрет",
	"инструкц",
}

var consentNeedles = []string{
	"jā",
	"ja pievieno",
	"pievieno",
	"gribu",
	"ok",
	"yes",
	"add me",
	"i want",
	"да",
	"хочу",
	"добавь",
}

var declineNeedles = []string{
	"nē",
	"negribu",
	"no",
	"stop",
	"unsubscribe",
	"нет",
	"не хочу",
	"отстань",
}

func containsAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func containsWordOrPhrase(text string, needles []string) bool {
	padded := " " + text + " "
	for _, needle := range needles {
		needle = normalizeText(needle)
		if strings.Contains(needle, " ") {
			if strings.Contains(text, needle) {
				return true
			}
			continue
		}
		if strings.Contains(padded, " "+needle+" ") {
			return true
		}
	}
	return false
}
