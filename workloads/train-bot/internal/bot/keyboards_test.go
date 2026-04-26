package bot

import (
	"testing"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
)

func TestMainReplyKeyboardLocalizedEnglish(t *testing.T) {
	catalog := i18n.NewCatalog()
	kb := MainReplyKeyboardWithWebApp(domain.LanguageEN, catalog, "https://train-bot.jolkins.id.lv/app")

	rows, ok := kb["keyboard"].([][]map[string]any)
	if !ok {
		t.Fatalf("keyboard rows missing or wrong type: %T", kb["keyboard"])
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 keyboard rows, got %d", len(rows))
	}

	webApp, ok := rows[0][0]["web_app"].(map[string]string)
	if !ok {
		t.Fatalf("expected web_app payload in first row, got %T", rows[0][0]["web_app"])
	}
	if webApp["url"] != "https://train-bot.jolkins.id.lv/app" {
		t.Fatalf("unexpected web app url: %q", webApp["url"])
	}
	if rows[1][0]["text"] != catalog.T(domain.LanguageEN, "btn_main_settings") {
		t.Fatalf("unexpected row 1 col 0: %q", rows[1][0]["text"])
	}
	if rows[1][1]["text"] != catalog.T(domain.LanguageEN, "btn_main_help") {
		t.Fatalf("unexpected row 1 col 1: %q", rows[1][1]["text"])
	}

	placeholder, ok := kb["input_field_placeholder"].(string)
	if !ok {
		t.Fatalf("input_field_placeholder missing or wrong type: %T", kb["input_field_placeholder"])
	}
	if placeholder != catalog.T(domain.LanguageEN, "main_input_placeholder") {
		t.Fatalf("unexpected placeholder: %q", placeholder)
	}
}

func TestMainReplyKeyboardLocalizedLatvian(t *testing.T) {
	catalog := i18n.NewCatalog()
	kb := MainReplyKeyboard(domain.LanguageLV, catalog)

	rows, ok := kb["keyboard"].([][]map[string]any)
	if !ok {
		t.Fatalf("keyboard rows missing or wrong type: %T", kb["keyboard"])
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 keyboard row, got %d", len(rows))
	}

	if rows[0][0]["text"] != catalog.T(domain.LanguageLV, "btn_main_settings") {
		t.Fatalf("unexpected row 0 col 0: %q", rows[0][0]["text"])
	}
	if rows[0][1]["text"] != catalog.T(domain.LanguageLV, "btn_main_help") {
		t.Fatalf("unexpected row 0 col 1: %q", rows[0][1]["text"])
	}

	placeholder, ok := kb["input_field_placeholder"].(string)
	if !ok {
		t.Fatalf("input_field_placeholder missing or wrong type: %T", kb["input_field_placeholder"])
	}
	if placeholder != catalog.T(domain.LanguageLV, "main_input_placeholder") {
		t.Fatalf("unexpected placeholder: %q", placeholder)
	}
}

func TestMainReplyKeyboardWithIncidentsAddsDedicatedShortcutRow(t *testing.T) {
	catalog := i18n.NewCatalog()
	kb := MainReplyKeyboardWithWebAppAndIncidents(
		domain.LanguageEN,
		catalog,
		"https://train-bot.jolkins.id.lv/app",
		"https://train-bot.jolkins.id.lv/incidents",
	)

	rows, ok := kb["keyboard"].([][]map[string]any)
	if !ok {
		t.Fatalf("keyboard rows missing or wrong type: %T", kb["keyboard"])
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 keyboard rows, got %d", len(rows))
	}
	if rows[1][0]["text"] != catalog.T(domain.LanguageEN, "btn_open_incidents") {
		t.Fatalf("unexpected incidents row text: %q", rows[1][0]["text"])
	}
	webApp, ok := rows[1][0]["web_app"].(map[string]string)
	if !ok {
		t.Fatalf("expected incidents web_app payload, got %T", rows[1][0]["web_app"])
	}
	if webApp["url"] != "https://train-bot.jolkins.id.lv/incidents" {
		t.Fatalf("unexpected incidents web app url: %q", webApp["url"])
	}
}

func TestMainReplyKeyboardWithIncidentsUsesUpdatedLatvianLabel(t *testing.T) {
	catalog := i18n.NewCatalog()
	kb := MainReplyKeyboardWithWebAppAndIncidents(
		domain.LanguageLV,
		catalog,
		"https://train-bot.jolkins.id.lv/app",
		"https://train-bot.jolkins.id.lv/incidents",
	)

	rows, ok := kb["keyboard"].([][]map[string]any)
	if !ok {
		t.Fatalf("keyboard rows missing or wrong type: %T", kb["keyboard"])
	}
	if rows[1][0]["text"] != "🚨 Aktuāli" {
		t.Fatalf("unexpected incidents row text: %q", rows[1][0]["text"])
	}
}
