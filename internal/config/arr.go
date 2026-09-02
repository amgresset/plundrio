package config

import (
	"fmt"
	"strings"
)

// ParseArrApps parses the arr_apps setting (env PLDR_ARR_APPS).
//
// Format: comma-separated "name=url|apikey" entries, e.g.
//
//	sonarr=http://192.168.0.20:8989|abcd1234,radarr=http://192.168.0.20:7878|ef567890
//
// An empty string yields no apps (the feature stays off).
func ParseArrApps(s string) ([]ArrApp, error) {
	var apps []ArrApp
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, rest, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("arr app %q: expected name=url|apikey", entry)
		}
		url, key, ok := strings.Cut(rest, "|")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(url) == "" || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("arr app %q: expected name=url|apikey", name)
		}
		apps = append(apps, ArrApp{
			Name:   strings.TrimSpace(name),
			URL:    strings.TrimRight(strings.TrimSpace(url), "/"),
			APIKey: strings.TrimSpace(key),
		})
	}
	return apps, nil
}
