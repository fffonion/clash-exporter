package collector

import "testing"

func TestGeoSiteMatcherLookupFiltersBroadCodes(t *testing.T) {
	m := &geoSiteMatcherImpl{
		full: map[string]string{
			"exact.category.example": "CATEGORY-COMPANIES",
		},
		suffix: map[string]string{
			"calendar.google.com": "CATEGORY-COMPANIES",
			"google.com":          "GOOGLE",
			"cn":                  "TLD-CN",
		},
	}

	if got := m.lookup("calendar.google.com"); got != "google" {
		t.Fatalf("lookup(calendar.google.com) = %q, want google", got)
	}
	if got := m.lookup("example.cn"); got != "" {
		t.Fatalf("lookup(example.cn) = %q, want empty", got)
	}
	if got := m.lookup("exact.category.example"); got != "" {
		t.Fatalf("lookup(exact.category.example) = %q, want empty", got)
	}
}

func TestIsSpecificGeoSite(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		{name: "company", code: "XIAOMI", want: true},
		{name: "company subcategory", code: "GOOGLE-PLAY", want: true},
		{name: "country", code: "CN", want: false},
		{name: "tld country", code: "TLD-CN", want: false},
		{name: "broad category", code: "CATEGORY-COMPANIES", want: false},
		{name: "geolocation", code: "GEOLOCATION-!CN", want: false},
		{name: "generic geosite", code: "GEOSITE-GFW", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSpecificGeoSite(tt.code); got != tt.want {
				t.Fatalf("isSpecificGeoSite(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}
