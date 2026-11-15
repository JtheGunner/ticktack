package review

import "regexp"

func mustPattern(p string) *regexp.Regexp { return regexp.MustCompile(p) }
