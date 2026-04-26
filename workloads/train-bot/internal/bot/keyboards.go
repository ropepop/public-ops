package bot

import (
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
)

func MainReplyKeyboard(lang domain.Language, catalog *i18n.Catalog) map[string]any {
	return MainReplyKeyboardWithWebApp(lang, catalog, "")
}

func MainReplyKeyboardWithWebApp(lang domain.Language, catalog *i18n.Catalog, appLaunchURL string) map[string]any {
	return MainReplyKeyboardWithWebAppAndIncidents(lang, catalog, appLaunchURL, "")
}

func MainReplyKeyboardWithWebAppAndIncidents(lang domain.Language, catalog *i18n.Catalog, appLaunchURL, incidentsURL string) map[string]any {
	rows := make([][]map[string]any, 0, 4)
	if appLaunchURL != "" {
		rows = append(rows, []map[string]any{WebAppInlineButton(catalog.T(lang, "btn_open_app"), appLaunchURL)})
	}
	if incidentsURL != "" {
		rows = append(rows, []map[string]any{WebAppInlineButton(catalog.T(lang, "btn_open_incidents"), incidentsURL)})
	}
	rows = append(rows,
		[]map[string]any{{"text": catalog.T(lang, "btn_main_settings")}, {"text": catalog.T(lang, "btn_main_help")}},
	)
	return map[string]any{
		"keyboard":                rows,
		"resize_keyboard":         true,
		"is_persistent":           true,
		"input_field_placeholder": catalog.T(lang, "main_input_placeholder"),
	}
}

func InlineButton(text string, callback string) map[string]string {
	return map[string]string{"text": text, "callback_data": callback}
}

func InlineKeyboard(rows ...[]map[string]string) map[string]any {
	return map[string]any{"inline_keyboard": rows}
}

func InlineButtonAny(text string, callback string) map[string]any {
	return map[string]any{"text": text, "callback_data": callback}
}

func WebAppInlineButton(text string, url string) map[string]any {
	return map[string]any{
		"text": text,
		"web_app": map[string]string{
			"url": url,
		},
	}
}

func URLInlineButton(text string, url string) map[string]any {
	return map[string]any{
		"text": text,
		"url":  url,
	}
}

func InlineKeyboardAny(rows ...[]map[string]any) map[string]any {
	return map[string]any{"inline_keyboard": rows}
}
