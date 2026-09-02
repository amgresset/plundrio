package config

import "testing"

func TestParseArrApps(t *testing.T) {
	apps, err := ParseArrApps(" sonarr=http://h:8989/|k1 , radarr=http://h:7878|k2 ")
	if err != nil || len(apps) != 2 {
		t.Fatalf("unexpected: %v %v", apps, err)
	}
	if apps[0] != (ArrApp{"sonarr", "http://h:8989", "k1"}) || apps[1] != (ArrApp{"radarr", "http://h:7878", "k2"}) {
		t.Fatalf("bad parse: %+v", apps)
	}
	if apps, err := ParseArrApps(""); err != nil || apps != nil {
		t.Fatalf("empty should be no apps: %v %v", apps, err)
	}
	for _, bad := range []string{"sonarr", "sonarr=http://h", "=http://h|k", "sonarr=|k"} {
		if _, err := ParseArrApps(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}
