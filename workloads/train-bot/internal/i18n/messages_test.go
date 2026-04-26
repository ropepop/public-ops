package i18n

import "testing"

func TestButtonKeysExistInAllLanguages(t *testing.T) {
	buttonKeys := []string{
		"btn_main_checkin",
		"btn_main_my_ride",
		"btn_main_report",
		"btn_main_settings",
		"btn_main_help",
		"btn_open_reports_channel",
		"btn_open_my_ride",
		"btn_report_inspection",
		"btn_checkin_confirm",
		"btn_view_status",
		"btn_subscribe",
		"btn_unsubscribe",
		"btn_checkin_by_station",
		"btn_checkin_by_time_today",
		"btn_checkin_search_route",
		"btn_checkin_favorites",
		"btn_checkin_cancel",
		"btn_checkin_more_results",
		"btn_checkin_refine_search",
		"btn_checkin_retry_search",
		"btn_start_checkin",
		"btn_back",
		"btn_main",
		"btn_windows",
		"btn_stations",
		"btn_origin",
		"btn_destinations",
		"btn_refresh",
		"btn_mute_30m",
		"btn_checkout",
		"btn_undo",
		"btn_report_started",
		"btn_report_in_car",
		"btn_report_ended",
		"btn_cancel",
		"btn_confirm",
		"btn_save_route",
		"btn_remove_favorite",
		"btn_settings_toggle_alerts",
		"btn_settings_toggle_style",
		"btn_settings_enable_alerts",
		"btn_settings_disable_alerts",
	}

	for _, key := range buttonKeys {
		if _, ok := enMessages[key]; !ok {
			t.Fatalf("missing key %q in EN messages", key)
		}
		if _, ok := lvMessages[key]; !ok {
			t.Fatalf("missing key %q in LV messages", key)
		}
	}
}

func TestMainInputPlaceholderKeyExistsInAllLanguages(t *testing.T) {
	if _, ok := enMessages["main_input_placeholder"]; !ok {
		t.Fatalf("missing key %q in EN messages", "main_input_placeholder")
	}
	if _, ok := lvMessages["main_input_placeholder"]; !ok {
		t.Fatalf("missing key %q in LV messages", "main_input_placeholder")
	}
}

func TestCheckInPromptKeysExistInAllLanguages(t *testing.T) {
	keys := []string{
		"checkin_entry_prompt",
		"checkin_station_prompt",
		"checkin_route_origin_prompt",
		"checkin_route_dest_prompt",
		"checkin_station_matches",
		"checkin_route_origin_matches",
		"checkin_route_dest_matches",
		"checkin_station_no_match",
		"checkin_route_origin_no_match",
		"checkin_route_dest_no_match",
		"checkin_window_departures",
		"checkin_station_departures",
		"checkin_route_departures",
		"station_checkin_disabled",
		"favorite_routes_title",
		"favorite_routes_empty",
		"undo_checkout_expired",
		"undo_checkout_restored",
		"route_origin_picker_title",
		"route_origin_picker_filtered",
		"route_origin_search_prompt",
		"route_dest_none",
		"route_dest_picker_title",
		"route_dest_picker_filtered",
		"route_dest_search_prompt",
		"train_times_line",
		"relative_now",
		"relative_one_min",
		"relative_many_mins",
	}

	for _, key := range keys {
		if _, ok := enMessages[key]; !ok {
			t.Fatalf("missing key %q in EN messages", key)
		}
		if _, ok := lvMessages[key]; !ok {
			t.Fatalf("missing key %q in LV messages", key)
		}
	}
}

func TestWebAppKeysExistInAllLanguages(t *testing.T) {
	keys := []string{
		"app_title",
		"app_public_dashboard_eyebrow",
		"app_public_train_eyebrow",
		"app_public_station_eyebrow",
		"app_public_train_title",
		"app_public_train_note",
		"app_public_map_eyebrow",
		"app_public_map_title",
		"app_public_map_note",
		"app_auth_required_body",
		"app_status_error_with_code",
		"app_open_departures",
		"app_open_station_search",
		"app_dashboard_intro",
		"app_status_hint",
		"app_status_empty",
		"app_section_sightings",
		"app_report_sighting",
		"app_report_deduped",
		"app_report_cooldown",
		"app_report_notice",
		"app_unsubscribed",
		"app_favorite_saved",
		"app_favorite_removed",
		"app_search_complete",
		"app_refresh_success",
		"app_status_loaded",
		"app_route_loaded",
		"app_public_station_title",
		"app_public_station_note",
		"app_public_station_search_label",
		"app_public_station_search_placeholder",
		"app_public_station_matches",
		"app_public_station_no_matches",
		"app_public_station_prompt",
		"app_public_station_selected",
		"app_public_station_last",
		"app_public_station_upcoming",
		"app_public_station_empty",
		"app_public_station_last_empty",
		"app_public_station_upcoming_empty",
		"app_public_station_search_success",
		"app_public_station_departures_loaded",
		"app_section_map",
		"app_map_loaded",
		"app_recent_platform_sightings",
		"app_station_sighting_empty",
		"app_station_sighting_title",
		"app_station_sighting_note",
		"app_station_sighting_destination_label",
		"app_station_sighting_destination_any",
		"app_station_sighting_submit",
		"app_station_sighting_select_departure",
		"app_station_sighting_success",
		"app_station_sighting_deduped",
		"app_station_sighting_cooldown",
		"app_station_sighting_matched",
		"app_station_sighting_unmatched",
		"app_sightings_empty",
		"app_sightings_choose_station",
		"app_map_prompt",
		"app_map_empty",
		"app_map_missing_coords",
		"app_network_map_title",
		"app_network_map_note",
		"app_network_map_empty",
		"app_network_map_activity_title",
		"app_network_map_activity_empty",
		"app_network_map_show_all",
		"app_public_network_map_title",
		"app_public_network_map_note",
		"app_map_popup_destination",
		"app_map_popup_status",
		"app_map_popup_age",
		"app_map_popup_seen_at",
		"app_map_tag_now",
		"app_stop_list",
		"app_view_stops_map",
		"settings_alerts_label",
		"settings_alert_style_label",
		"settings_language_label",
		"settings_reports_channel_label",
		"settings_style_detailed_option",
		"settings_style_discreet_option",
		"link_reports_channel",
		"app_relative_now",
		"app_relative_one_min",
		"app_relative_many_mins",
	}

	for _, key := range keys {
		if _, ok := enMessages[key]; !ok {
			t.Fatalf("missing key %q in EN messages", key)
		}
		if _, ok := lvMessages[key]; !ok {
			t.Fatalf("missing key %q in LV messages", key)
		}
	}
}

func TestLatvianStationSightingSelectionCopy(t *testing.T) {
	if got := lvMessages["app_station_sighting_select_departure"]; got != "Izmantot paziņošanai" {
		t.Fatalf("unexpected LV copy for app_station_sighting_select_departure: %q", got)
	}
}
