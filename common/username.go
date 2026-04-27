package common

import (
	"fmt"
	"strings"
)

// FixUsername normalizes a username by ensuring it has the expected domain suffix.
// The suffix may be provided with or without a leading "@"; FixUsername will
// prepend "@" if necessary. If the username already ends with the suffix,
// it is returned unchanged.
func FixUsername(username, suffix string) string {
	var userSuffix string
	if strings.HasPrefix(suffix, "@") {
		userSuffix = suffix
	} else {
		userSuffix = fmt.Sprintf("@%s", suffix)
	}
	if strings.HasSuffix(username, userSuffix) {
		return username
	}
	return fmt.Sprintf("%s%s", username, userSuffix)
}
