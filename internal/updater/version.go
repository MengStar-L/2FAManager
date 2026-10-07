package updater

import (
	"fmt"
	"regexp"
	"strings"
)

var semverPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

type version struct {
	core [3]string
	pre  []string
}

func parseVersion(s string) (version, error) {
	m := semverPattern.FindStringSubmatch(s)
	if m == nil {
		return version{}, fmt.Errorf("无效的版本号 %q", s)
	}
	v := version{core: [3]string{m[1], m[2], m[3]}}
	for group := 4; group <= 5; group++ {
		if m[group] == "" {
			continue
		}
		parts := strings.Split(m[group], ".")
		for _, p := range parts {
			if p == "" || (group == 4 && numeric(p) && len(p) > 1 && p[0] == '0') {
				return version{}, fmt.Errorf("无效的版本号 %q", s)
			}
		}
		if group == 4 {
			v.pre = parts
		}
	}
	return v, nil
}

func numeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func compareNumeric(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func (v version) compare(other version) int {
	for i := range v.core {
		if n := compareNumeric(v.core[i], other.core[i]); n != 0 {
			return n
		}
	}
	if len(v.pre) == 0 && len(other.pre) != 0 {
		return 1
	}
	if len(v.pre) != 0 && len(other.pre) == 0 {
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(other.pre); i++ {
		a, b := v.pre[i], other.pre[i]
		n := strings.Compare(a, b)
		switch {
		case numeric(a) && numeric(b):
			n = compareNumeric(a, b)
		case numeric(a) && !numeric(b):
			n = -1
		case !numeric(a) && numeric(b):
			n = 1
		}
		if n != 0 {
			return n
		}
	}
	if len(v.pre) < len(other.pre) {
		return -1
	}
	if len(v.pre) > len(other.pre) {
		return 1
	}
	return 0
}

// CompareVersions compares SemVer versions, accepting an optional v prefix.
// Build metadata does not affect ordering; prereleases sort before stable releases.
func CompareVersions(a, b string) (int, error) {
	av, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	return av.compare(bv), nil
}
