package update

import (
	"strconv"
	"strings"
)

// Version 语义化版本号 X.Y.Z。只表示正式版：带预发布后缀的版本（如 git describe 产生的
// v1.1.0-3-gabc123-dirty）和 "dev" 都解析失败，因此开发版永远不会被当成可自动更新的版本。
type Version struct{ Major, Minor, Patch int }

// ParseVersion 解析 "v1.2.3" 或 "1.2.3"。
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 6 || (len(p) > 1 && p[0] == '0') {
			return Version{}, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return Version{}, false
			}
		}
		n[i], _ = strconv.Atoi(p)
	}
	return Version{n[0], n[1], n[2]}, true
}

// Compare 返回 -1 / 0 / 1。
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return sign(v.Major - o.Major)
	case v.Minor != o.Minor:
		return sign(v.Minor - o.Minor)
	default:
		return sign(v.Patch - o.Patch)
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// String 形如 "1.2.3"。
func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// Tag 形如 "v1.2.3"。
func (v Version) Tag() string { return "v" + v.String() }
