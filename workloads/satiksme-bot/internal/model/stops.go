package model

import "strings"

func NormalizeStopAlias(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	trimmed := strings.TrimLeft(value, "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

func StopAliasEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || NormalizeStopAlias(a) == NormalizeStopAlias(b)
}

func FindStopByAnyID(catalog *Catalog, stopID string) (Stop, bool) {
	if catalog == nil {
		return Stop{}, false
	}
	stopID = strings.TrimSpace(stopID)
	if stopID == "" {
		return Stop{}, false
	}
	for _, stop := range catalog.Stops {
		if strings.TrimSpace(stop.ID) == stopID || strings.TrimSpace(stop.LiveID) == stopID {
			return stop, true
		}
	}
	for _, stop := range catalog.Stops {
		if StopAliasEqual(stop.ID, stopID) || StopAliasEqual(stop.LiveID, stopID) {
			return stop, true
		}
	}
	return Stop{}, false
}

func StopNameLookup(catalog *Catalog) map[string]string {
	stopNames := map[string]string{}
	if catalog == nil {
		return stopNames
	}
	for _, stop := range catalog.Stops {
		registerStopNameAlias(stopNames, stop.ID, stop.Name)
		registerStopNameAlias(stopNames, stop.LiveID, stop.Name)
		registerStopNameAlias(stopNames, NormalizeStopAlias(stop.ID), stop.Name)
		registerStopNameAlias(stopNames, NormalizeStopAlias(stop.LiveID), stop.Name)
	}
	return stopNames
}

func registerStopNameAlias(stopNames map[string]string, key, name string) {
	key = strings.TrimSpace(key)
	name = strings.TrimSpace(name)
	if key == "" || name == "" {
		return
	}
	if _, exists := stopNames[key]; exists {
		return
	}
	stopNames[key] = name
}
