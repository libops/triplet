package types

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	creativeCommonsLicense = regexp.MustCompile(`^/licenses/(by|by-sa|by-nc|by-nc-sa|by-nd|by-nc-nd)/(1\.0|2\.0|2\.5|3\.0|4\.0)/$`)
	creativeCommonsPorted  = regexp.MustCompile(`^/licenses/(by|by-sa|by-nc|by-nc-sa|by-nd|by-nc-nd)/(1\.0|2\.0|2\.5|3\.0)/[a-z]{2}/$`)
	creativeCommonsPD      = regexp.MustCompile(`^/publicdomain/(zero|mark)/1\.0/$`)
	rightsStatement        = regexp.MustCompile(`^/vocab/(InC|InC-OW-EU|InC-EDU|InC-NC|InC-RUU|NoC-CR|NoC-NC|NoC-OKLR|NoC-US|CNE|UND|NKC)/1\.0/$`)
)

// NormalizeRights returns the Image API 3.0 `rights` value for a Creative
// Commons or RightsStatements.org URI. It reports false for anything else.
//
// The specs define these URIs with the http scheme, so https spellings are
// rewritten and a missing trailing slash is added.
func NormalizeRights(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", false
	}
	path := u.Path
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	switch strings.ToLower(u.Hostname()) {
	case "creativecommons.org":
		if creativeCommonsLicense.MatchString(path) || creativeCommonsPorted.MatchString(path) || creativeCommonsPD.MatchString(path) {
			return "http://creativecommons.org" + path, true
		}
	case "rightsstatements.org":
		if rightsStatement.MatchString(path) {
			return "http://rightsstatements.org" + path, true
		}
	}
	return "", false
}
