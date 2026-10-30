package types

import "testing"

func TestNormalizeRights(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://creativecommons.org/licenses/by/4.0/", "http://creativecommons.org/licenses/by/4.0/"},
		{"https://creativecommons.org/licenses/by-nc-sa/3.0/", "http://creativecommons.org/licenses/by-nc-sa/3.0/"},
		{"https://creativecommons.org/licenses/by/4.0", "http://creativecommons.org/licenses/by/4.0/"},
		{"  https://creativecommons.org/licenses/by-nd/2.5/\n", "http://creativecommons.org/licenses/by-nd/2.5/"},
		{"HTTPS://CreativeCommons.org/licenses/by/4.0/", "http://creativecommons.org/licenses/by/4.0/"},
		{"https://creativecommons.org/licenses/by/3.0/de/", "http://creativecommons.org/licenses/by/3.0/de/"},
		{"https://creativecommons.org/publicdomain/zero/1.0/", "http://creativecommons.org/publicdomain/zero/1.0/"},
		{"https://creativecommons.org/publicdomain/mark/1.0/", "http://creativecommons.org/publicdomain/mark/1.0/"},
		{"https://rightsstatements.org/vocab/InC/1.0/", "http://rightsstatements.org/vocab/InC/1.0/"},
		{"http://rightsstatements.org/vocab/InC-OW-EU/1.0/", "http://rightsstatements.org/vocab/InC-OW-EU/1.0/"},
		{"http://rightsstatements.org/vocab/NoC-OKLR/1.0", "http://rightsstatements.org/vocab/NoC-OKLR/1.0/"},
		{"http://rightsstatements.org/vocab/UND/1.0/", "http://rightsstatements.org/vocab/UND/1.0/"},

		{"", ""},
		{"https://example.org/licenses/by/4.0/", ""},
		{"https://creativecommons.org/licenses/by/9.0/", ""},
		{"https://creativecommons.org/licenses/by/4.0/de/", ""},
		{"https://creativecommons.org/licenses/by/4.0/legalcode", ""},
		{"https://creativecommons.org/licenses/by/4.0/?x=1", ""},
		{"https://creativecommons.org/licenses/by/4.0/#top", ""},
		{"https://creativecommons.org:8443/licenses/by/4.0/", ""},
		{"https://user@creativecommons.org/licenses/by/4.0/", ""},
		{"https://creativecommons.org.evil.test/licenses/by/4.0/", ""},
		{"ftp://creativecommons.org/licenses/by/4.0/", ""},
		{"https://creativecommons.org/licenses/by/4.0/x/", ""},
		{"https://creativecommons.org/publicdomain/zero/2.0/", ""},
		{"http://rightsstatements.org/vocab/Nope/1.0/", ""},
		{"http://rightsstatements.org/page/InC/1.0/", ""},
		{"http://rightsstatements.org/vocab/InC/2.0/", ""},
		{"creativecommons.org/licenses/by/4.0/", ""},
		{"https://www.creativecommons.org/licenses/by/4.0/", ""},
		{"CC BY 4.0", ""},
	}
	for _, tc := range cases {
		got, ok := NormalizeRights(tc.in)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("NormalizeRights(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
		}
	}
}
