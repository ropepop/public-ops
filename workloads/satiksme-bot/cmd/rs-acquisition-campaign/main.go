package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"satiksmebot/internal/acquisition"
)

const defaultStatePath = "./state/rs-acquisition/campaign.db"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "collect-recent":
		return collect(ctx, args[1:], stdout, acquisition.SourceRecentActive)
	case "collect-members":
		return collect(ctx, args[1:], stdout, acquisition.SourceMemberList)
	case "import-candidates":
		return importCandidates(ctx, args[1:], stdout)
	case "plan-day":
		return planDay(ctx, args[1:], stdout)
	case "mark-sent":
		return markSent(ctx, args[1:], stdout)
	case "invalidate-pending-drafts":
		return invalidatePendingDrafts(ctx, args[1:], stdout)
	case "approve-token":
		return approveToken(ctx, args[1:], stdout)
	case "record-reply":
		return recordReply(ctx, args[1:], stdout)
	case "sender-info":
		return senderInfo(ctx, args[1:], stdout)
	case "send-test":
		return sendTest(ctx, args[1:], stdout)
	case "daemon":
		return runDaemon(ctx, args[1:], stdout)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runDaemon(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	enabled := fs.Bool("enabled", envBool("RS_ACQUISITION_ENABLED", false), "Enable production campaign loop")
	once := fs.Bool("once", false, "Run exactly one daemon cycle")
	statePath := fs.String("state", envOr("RS_ACQUISITION_STATE_PATH", defaultStatePath), "Private SQLite state path")
	apiID := fs.Int("api-id", envInt("RS_ACQUISITION_API_ID", envInt("SATIKSME_CHAT_ANALYZER_API_ID", 0)), "Telegram API ID")
	apiHash := fs.String("api-hash", envOr("RS_ACQUISITION_API_HASH", os.Getenv("SATIKSME_CHAT_ANALYZER_API_HASH")), "Telegram API hash")
	sourceSessionFile := fs.String("source-session-file", envOr("RS_ACQUISITION_SOURCE_SESSION_FILE", envOr("SATIKSME_CHAT_ANALYZER_SESSION_FILE", "./state/chat-analyzer.session")), "MTProto source chat session file")
	senderSessionFile := fs.String("sender-session-file", envOr("RS_ACQUISITION_SENDER_SESSION_FILE", "./state/rs-acquisition/iamhdzs.session"), "MTProto outreach sender session file")
	chatID := fs.String("chat", envOr("RS_ACQUISITION_CHAT_ID", os.Getenv("SATIKSME_CHAT_ANALYZER_CHAT_ID")), "Source Telegram chat descriptor")
	expectSender := fs.String("expect-sender", envOr("RS_ACQUISITION_EXPECT_SENDER", "iamhdzs"), "Expected outreach sender username")
	adminMode := fs.String("admin-mode", envOr("RS_ACQUISITION_ADMIN_MODE", "bot"), "Admin approval transport: bot or mtproto")
	adminUsername := fs.String("admin-username", envOr("RS_ACQUISITION_ADMIN_USERNAME", ""), "Telegram username that receives MTProto admin approvals")
	adminToken := fs.String("admin-bot-token", envOr("RS_ACQUISITION_ADMIN_BOT_TOKEN", os.Getenv("RS_ACQUISITION_ALERT_BOT_TOKEN")), "Telegram Bot API token for admin approvals")
	adminChatID := fs.String("admin-chat-id", envOr("RS_ACQUISITION_ADMIN_CHAT_ID", os.Getenv("RS_ACQUISITION_ALERT_CHAT_ID")), "Telegram admin chat ID")
	grantBotUsername := fs.String("grant-bot-username", envOr("RS_ACQUISITION_GRANT_BOT_USERNAME", "rs_bilete_bot"), "Telegram bot username that receives accepted-user grant commands")
	timezone := fs.String("timezone", envOr("RS_ACQUISITION_TIMEZONE", envOr("TZ", "Europe/Riga")), "Campaign day timezone")
	dailyLimit := fs.Int("daily-limit", envInt("RS_ACQUISITION_DAILY_LIMIT", 10), "Maximum approved first contacts per day")
	dailyRegistrations := fs.Int("daily-registrations", envInt("RS_ACQUISITION_DAILY_REGISTRATIONS", 4), "Free daily registrations to offer")
	groupName := fs.String("group-name", envOr("RS_ACQUISITION_GROUP_NAME", "Rīgas Zaķi"), "Group name mentioned in outreach")
	pollInterval := fs.Duration("poll-interval", envDuration("RS_ACQUISITION_POLL_INTERVAL", time.Minute), "Delay between daemon cycles")
	recentLimit := fs.Int("recent-limit", envInt("RS_ACQUISITION_RECENT_LIMIT", 100), "Recent active candidate collection limit")
	memberLimit := fs.Int("member-limit", envInt("RS_ACQUISITION_MEMBER_LIMIT", 500), "Member-list candidate collection limit")
	includeMembers := fs.Bool("include-members", envBool("RS_ACQUISITION_INCLUDE_MEMBERS", false), "Also collect member-list candidates after recent active users")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*enabled && !*once {
		_, _ = fmt.Fprintln(stdout, "RS acquisition daemon disabled; set RS_ACQUISITION_ENABLED=true to run.")
		return nil
	}
	loc, err := time.LoadLocation(*timezone)
	if err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	admin, err := buildAdminGateway(*adminMode, *apiID, *apiHash, *senderSessionFile, *adminUsername, *adminToken, *adminChatID, store)
	if err != nil {
		return err
	}
	var grant acquisition.GrantGateway
	if strings.TrimSpace(*grantBotUsername) != "" {
		grant, err = acquisition.NewMTProtoGrantGateway(acquisition.MTProtoGrantGatewayConfig{
			APIID:       *apiID,
			APIHash:     *apiHash,
			SessionFile: *senderSessionFile,
			BotUsername: *grantBotUsername,
		})
		if err != nil {
			return err
		}
	}
	outreach := acquisition.NewMTProtoOutreach(acquisition.MTProtoOutreachConfig{
		APIID:       *apiID,
		APIHash:     *apiHash,
		SessionFile: *senderSessionFile,
	})
	sourceCfg := acquisition.TelegramConfig{
		APIID:       *apiID,
		APIHash:     *apiHash,
		SessionFile: *sourceSessionFile,
		ChatID:      *chatID,
		Limit:       *recentLimit,
		PageSize:    100,
		PageDelay:   3 * time.Second,
	}
	memberCfg := sourceCfg
	memberCfg.Limit = *memberLimit
	daemon := acquisition.CampaignDaemon{
		Store: store,
		Config: acquisition.DaemonConfig{
			Location:           loc,
			DailyLimit:         *dailyLimit,
			DailyRegistrations: *dailyRegistrations,
			GroupName:          *groupName,
			ExpectedSender:     *expectSender,
		},
		Collector: acquisition.TelegramCandidateCollector{
			RecentConfig:   sourceCfg,
			MemberConfig:   memberCfg,
			IncludeMembers: *includeMembers,
		},
		Admin:    admin,
		Outreach: outreach,
		Replies:  outreach,
		Grant:    grant,
	}
	if *once {
		result, err := daemon.RunOnce(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	}
	ticker := time.NewTicker(*pollInterval)
	defer ticker.Stop()
	for {
		result, err := daemon.RunOnce(ctx)
		if err != nil {
			_ = writeJSON(stdout, map[string]any{"level": "error", "error": err.Error(), "at": time.Now().UTC()})
		} else {
			_ = writeJSON(stdout, map[string]any{"level": "info", "result": result, "at": time.Now().UTC()})
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func buildAdminGateway(mode string, apiID int, apiHash string, senderSessionFile string, adminUsername string, adminToken string, adminChatID string, store *acquisition.Store) (acquisition.AdminGateway, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "bot":
		return acquisition.NewAdminBotGateway(acquisition.AdminBotConfig{Token: adminToken, ChatID: adminChatID})
	case "mtproto":
		return acquisition.NewMTProtoAdminGateway(acquisition.MTProtoAdminConfig{
			APIID:       apiID,
			APIHash:     apiHash,
			SessionFile: senderSessionFile,
			Username:    adminUsername,
		}, store)
	default:
		return nil, fmt.Errorf("--admin-mode must be bot or mtproto")
	}
}

func collect(ctx context.Context, args []string, stdout io.Writer, source acquisition.CandidateSource) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	statePath := fs.String("state", defaultStatePath, "Private SQLite state path")
	apiID := fs.Int("api-id", envInt("SATIKSME_CHAT_ANALYZER_API_ID", 0), "Telegram API ID")
	apiHash := fs.String("api-hash", os.Getenv("SATIKSME_CHAT_ANALYZER_API_HASH"), "Telegram API hash")
	sessionFile := fs.String("session-file", envOr("SATIKSME_CHAT_ANALYZER_SESSION_FILE", "./state/chat-analyzer.session"), "MTProto session file")
	chatID := fs.String("chat", os.Getenv("SATIKSME_CHAT_ANALYZER_CHAT_ID"), "Telegram chat descriptor")
	limit := fs.Int("limit", 100, "Maximum users to collect")
	pageSize := fs.Int("page-size", 100, "Telegram page size")
	pageDelay := fs.Duration("page-delay", 3*time.Second, "Delay between Telegram pages")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := acquisition.TelegramConfig{
		APIID:       *apiID,
		APIHash:     *apiHash,
		SessionFile: *sessionFile,
		ChatID:      *chatID,
		Limit:       *limit,
		PageSize:    *pageSize,
		PageDelay:   *pageDelay,
	}
	var candidates []acquisition.Candidate
	var err error
	if source == acquisition.SourceRecentActive {
		candidates, err = acquisition.CollectRecentActive(ctx, cfg)
	} else {
		candidates, err = acquisition.CollectMembers(ctx, cfg)
	}
	if err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.UpsertCandidates(ctx, candidates, now); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"source":     source,
		"collected":  len(candidates),
		"state":      *statePath,
		"updatedAt":  now,
		"candidates": candidates,
	})
}

func importCandidates(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("import-candidates", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	statePath := fs.String("state", defaultStatePath, "Private SQLite state path")
	inputPath := fs.String("input", "", "JSON file containing candidate objects")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*inputPath) == "" {
		return fmt.Errorf("--input is required")
	}
	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		return err
	}
	var candidates []acquisition.Candidate
	if err := json.Unmarshal(raw, &candidates); err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.UpsertCandidates(ctx, candidates, now); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"imported":  len(candidates),
		"state":     *statePath,
		"updatedAt": now,
	})
}

func planDay(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("plan-day", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	statePath := fs.String("state", defaultStatePath, "Private SQLite state path")
	timezone := fs.String("timezone", envOr("TZ", "Europe/Riga"), "Campaign day timezone")
	dailyLimit := fs.Int("daily-limit", 10, "Maximum new first contacts per day")
	dailyRegistrations := fs.Int("daily-registrations", 4, "Free daily transport registrations to offer")
	groupName := fs.String("group-name", "Rīgas Zaķi", "Group name mentioned in outreach")
	if err := fs.Parse(args); err != nil {
		return err
	}
	loc, err := time.LoadLocation(*timezone)
	if err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	plan, err := acquisition.BuildDailyPlan(ctx, store, acquisition.DailyPlanOptions{
		Now:                time.Now().UTC(),
		Location:           loc,
		DailyLimit:         *dailyLimit,
		DailyRegistrations: *dailyRegistrations,
		GroupName:          *groupName,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, plan)
}

func markSent(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("mark-sent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	statePath := fs.String("state", defaultStatePath, "Private SQLite state path")
	timezone := fs.String("timezone", envOr("TZ", "Europe/Riga"), "Campaign day timezone")
	userID := fs.Int64("user-id", 0, "Telegram user ID")
	text := fs.String("text", "", "Approved first-contact text that was sent")
	textFile := fs.String("text-file", "", "File containing approved first-contact text that was sent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *userID <= 0 {
		return fmt.Errorf("--user-id is required")
	}
	draftText, err := readTextValue(*text, *textFile)
	if err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	now := time.Now().UTC()
	loc, err := time.LoadLocation(*timezone)
	if err != nil {
		return err
	}
	day := acquisition.DayKey(now, loc)
	if err := store.RecordFirstContactForDay(ctx, *userID, draftText, day, now); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"userId":     *userID,
		"day":        day,
		"status":     acquisition.StatusContacted,
		"recordedAt": now,
	})
}

func invalidatePendingDrafts(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("invalidate-pending-drafts", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	statePath := fs.String("state", envOr("RS_ACQUISITION_STATE_PATH", defaultStatePath), "Private SQLite state path")
	reason := fs.String("reason", "copy_updated", "Reason stored in the audit log")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	now := time.Now().UTC()
	invalidated, err := store.InvalidatePendingDrafts(ctx, *reason, now)
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"invalidated": invalidated,
		"reason":      strings.TrimSpace(*reason),
		"state":       *statePath,
		"updatedAt":   now,
	})
}

func approveToken(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("approve-token", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	statePath := fs.String("state", envOr("RS_ACQUISITION_STATE_PATH", defaultStatePath), "Private SQLite state path")
	token := fs.String("token", "", "Pending approval token to send")
	apiID := fs.Int("api-id", envInt("RS_ACQUISITION_API_ID", envInt("SATIKSME_CHAT_ANALYZER_API_ID", 0)), "Telegram API ID")
	apiHash := fs.String("api-hash", envOr("RS_ACQUISITION_API_HASH", os.Getenv("SATIKSME_CHAT_ANALYZER_API_HASH")), "Telegram API hash")
	senderSessionFile := fs.String("sender-session-file", envOr("RS_ACQUISITION_SENDER_SESSION_FILE", "./state/rs-acquisition/iamhdzs.session"), "MTProto outreach sender session file")
	expectSender := fs.String("expect-sender", envOr("RS_ACQUISITION_EXPECT_SENDER", "iamhdzs"), "Expected outreach sender username")
	adminMode := fs.String("admin-mode", envOr("RS_ACQUISITION_ADMIN_MODE", "bot"), "Admin alert transport: bot or mtproto")
	adminUsername := fs.String("admin-username", envOr("RS_ACQUISITION_ADMIN_USERNAME", ""), "Telegram username that receives MTProto admin alerts")
	adminToken := fs.String("admin-bot-token", envOr("RS_ACQUISITION_ADMIN_BOT_TOKEN", os.Getenv("RS_ACQUISITION_ALERT_BOT_TOKEN")), "Telegram Bot API token for admin alerts")
	adminChatID := fs.String("admin-chat-id", envOr("RS_ACQUISITION_ADMIN_CHAT_ID", os.Getenv("RS_ACQUISITION_ALERT_CHAT_ID")), "Telegram admin chat ID")
	timezone := fs.String("timezone", envOr("RS_ACQUISITION_TIMEZONE", envOr("TZ", "Europe/Riga")), "Campaign day timezone")
	dailyLimit := fs.Int("daily-limit", envInt("RS_ACQUISITION_DAILY_LIMIT", 10), "Maximum approved first contacts per day")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cleanToken := strings.TrimSpace(*token)
	if cleanToken == "" {
		return fmt.Errorf("--token is required")
	}
	loc, err := time.LoadLocation(*timezone)
	if err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	admin, err := buildAdminGateway(*adminMode, *apiID, *apiHash, *senderSessionFile, *adminUsername, *adminToken, *adminChatID, store)
	if err != nil {
		return err
	}
	outreach := acquisition.NewMTProtoOutreach(acquisition.MTProtoOutreachConfig{
		APIID:       *apiID,
		APIHash:     *apiHash,
		SessionFile: *senderSessionFile,
	})
	now := time.Now().UTC()
	daemon := acquisition.CampaignDaemon{
		Store: store,
		Config: acquisition.DaemonConfig{
			Now:            func() time.Time { return now },
			Location:       loc,
			DailyLimit:     *dailyLimit,
			ExpectedSender: *expectSender,
		},
		Admin:    admin,
		Outreach: outreach,
	}
	result, err := daemon.ProcessDecision(ctx, acquisition.AdminDecision{Token: cleanToken, Action: acquisition.AdminApprove})
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"state":       *statePath,
		"processedAt": now,
		"result":      result,
	})
}

func recordReply(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("record-reply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	statePath := fs.String("state", defaultStatePath, "Private SQLite state path")
	userID := fs.Int64("user-id", 0, "Telegram user ID")
	text := fs.String("text", "", "Reply text")
	textFile := fs.String("text-file", "", "File containing reply text")
	alertToken := fs.String("alert-bot-token", os.Getenv("RS_ACQUISITION_ALERT_BOT_TOKEN"), "Telegram Bot API token for unsafe-reply alerts")
	alertChatID := fs.String("alert-chat-id", os.Getenv("RS_ACQUISITION_ALERT_CHAT_ID"), "Telegram chat ID for unsafe-reply alerts")
	requireAlert := fs.Bool("require-alert", true, "Fail unsafe-reply handling when Telegram alert delivery is not configured")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *userID <= 0 {
		return fmt.Errorf("--user-id is required")
	}
	replyText, err := readTextValue(*text, *textFile)
	if err != nil {
		return err
	}
	store, err := acquisition.OpenStore(*statePath)
	if err != nil {
		return err
	}
	defer store.Close()
	now := time.Now().UTC()
	outcome, err := store.RecordReply(ctx, *userID, replyText, now)
	if err != nil {
		return err
	}
	alertSent := false
	if outcome.Decision.AlertAdmin && strings.TrimSpace(*alertToken) != "" && strings.TrimSpace(*alertChatID) != "" {
		alert := fmt.Sprintf("RS acquisition stopped contact %d: %s", *userID, outcome.Decision.Reason)
		if err := sendTelegramAlert(ctx, "https://api.telegram.org", *alertToken, *alertChatID, alert); err != nil {
			return err
		}
		alertSent = true
	}
	if outcome.Decision.AlertAdmin && !alertSent && *requireAlert {
		return fmt.Errorf("unsafe reply was recorded, but alert delivery is not configured; set RS_ACQUISITION_ALERT_BOT_TOKEN and RS_ACQUISITION_ALERT_CHAT_ID or pass --require-alert=false for dry runs")
	}
	return writeJSON(stdout, map[string]any{
		"userId":     *userID,
		"recordedAt": now,
		"outcome":    outcome,
		"alertSent":  alertSent,
	})
}

func sendTest(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("send-test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	apiID := fs.Int("api-id", envInt("RS_ACQUISITION_API_ID", envInt("SATIKSME_CHAT_ANALYZER_API_ID", 0)), "Telegram API ID")
	apiHash := fs.String("api-hash", envOr("RS_ACQUISITION_API_HASH", os.Getenv("SATIKSME_CHAT_ANALYZER_API_HASH")), "Telegram API hash")
	sessionFile := fs.String("sender-session-file", envOr("RS_ACQUISITION_SENDER_SESSION_FILE", "./state/rs-acquisition/iamhdzs.session"), "MTProto sender session file")
	to := fs.String("to", "", "Target Telegram username")
	confirmTo := fs.String("confirm-to", "", "Must match --to before a message is sent")
	expectSender := fs.String("expect-sender", envOr("RS_ACQUISITION_EXPECT_SENDER", "iamhdzs"), "Expected sender username for the session")
	text := fs.String("text", "", "Test message text; a safe default is used when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	message := strings.TrimSpace(*text)
	if message == "" {
		message = acquisition.DefaultDirectTestMessage(*expectSender, *to)
	}
	result, err := acquisition.SendDirectTestMessage(ctx, acquisition.DirectTestMessageOptions{
		APIID:                 *apiID,
		APIHash:               *apiHash,
		SessionFile:           *sessionFile,
		TargetUsername:        *to,
		ConfirmTargetUsername: *confirmTo,
		ExpectSenderUsername:  *expectSender,
		Message:               message,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func senderInfo(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sender-info", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	apiID := fs.Int("api-id", envInt("RS_ACQUISITION_API_ID", envInt("SATIKSME_CHAT_ANALYZER_API_ID", 0)), "Telegram API ID")
	apiHash := fs.String("api-hash", envOr("RS_ACQUISITION_API_HASH", os.Getenv("SATIKSME_CHAT_ANALYZER_API_HASH")), "Telegram API hash")
	sessionFile := fs.String("sender-session-file", envOr("RS_ACQUISITION_SENDER_SESSION_FILE", "./state/rs-acquisition/iamhdzs.session"), "MTProto sender session file")
	expectSender := fs.String("expect-sender", envOr("RS_ACQUISITION_EXPECT_SENDER", ""), "Expected sender username for the session")
	if err := fs.Parse(args); err != nil {
		return err
	}
	outreach := acquisition.NewMTProtoOutreach(acquisition.MTProtoOutreachConfig{
		APIID:       *apiID,
		APIHash:     *apiHash,
		SessionFile: *sessionFile,
	})
	info, err := outreach.SenderInfo(ctx)
	if err != nil {
		return err
	}
	expected := cleanUsernameLocal(*expectSender)
	if expected != "" && cleanUsernameLocal(info.Username) != expected {
		return fmt.Errorf("sender session is @%s, want @%s", cleanUsernameLocal(info.Username), expected)
	}
	return writeJSON(stdout, info)
}

func readTextValue(text string, textFile string) (string, error) {
	if strings.TrimSpace(textFile) != "" {
		raw, err := os.ReadFile(textFile)
		if err != nil {
			return "", err
		}
		text = string(raw)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("text or text-file is required")
	}
	return text, nil
}

func sendTelegramAlert(ctx context.Context, baseURL string, token string, chatID string, text string) error {
	payload, err := json.Marshal(map[string]string{
		"chat_id": chatID,
		"text":    text,
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(baseURL, "/") + "/bot" + strings.TrimSpace(token) + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("telegram alert failed with HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: rs-acquisition-campaign <command> [flags]

Commands:
  collect-recent      Read recent active users from the configured Telegram chat into private state
  collect-members     Read member-list candidates into private state
  import-candidates   Import candidate JSON into private state
  plan-day            Print the human-review draft batch for today
  mark-sent           Mark one human-approved first contact as sent
  invalidate-pending-drafts
                      Reject every unsent approval draft so new copy is generated
  approve-token       Send one pending approval token through the normal outreach path
  record-reply        Classify a user reply, stop/alert if unsafe, print grant command on consent
  sender-info         Print the authorized outreach Telegram account id and username
  send-test           Send one explicitly confirmed test DM from the outreach account
  daemon              Run the production admin-approved campaign service loop`)
}

func envOr(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "on"
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return value
}

func cleanUsernameLocal(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	return strings.ToLower(username)
}
