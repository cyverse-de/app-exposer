package common

import (
	"regexp"
	"strings"

	"github.com/gosimple/slug"
)

var leadingLabelReplacerRegexp = regexp.MustCompile("^[^0-9A-Za-z]+")
var trailingLabelReplacerRegexp = regexp.MustCompile("[^0-9A-Za-z]+$")

// labelReplacerFn returns a function that can be used to replace invalid leading and trailing characters
// in label values. Hyphens are replaced by the letter "h". Underscores are replaced by the letter "u".
// Other characters in the match are replaced by the empty string. The prefix and suffix are placed before
// and after the replacement, respectively.
func labelReplacerFn(prefix, suffix string) func(string) string {
	replacementFor := map[rune]string{
		'-': "h",
		'_': "u",
	}

	return func(match string) string {
		runes := []rune(match)
		elems := make([]string, len(runes))
		for i, c := range runes {
			elems[i] = replacementFor[c]
		}
		return prefix + strings.Join(elems, "-") + suffix
	}
}

// LabelValueString returns a version of the given string that may be used as a value in a Kubernetes
// label. See: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/. Leading and
// trailing underscores and hyphens are replaced by sequences of `u` and `h`, separated by hyphens.
// These sequences are separated from the main part of the label value by `-xxx-`. This is kind of
// hokey, but it makes it at least fairly unlikely that we'll encounter collisions.
func LabelValueString(str string) string {
	slug.MaxLength = 63
	str = leadingLabelReplacerRegexp.ReplaceAllStringFunc(str, labelReplacerFn("", "-xxx-"))
	str = trailingLabelReplacerRegexp.ReplaceAllStringFunc(str, labelReplacerFn("-xxx-", ""))
	return slug.Make(str)
}
