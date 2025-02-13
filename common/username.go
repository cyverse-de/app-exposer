package common

import (
	"fmt"
	"strings"
)

func FixUsername(username, suffix string) string {
	var userSuffix string
	if strings.HasPrefix(suffix, "@") {
		userSuffix = suffix
	} else {
		userSuffix = fmt.Sprintf("@%s", suffix)
	}
	if strings.HasSuffix(userSuffix, username) {
		return username
	}
	return fmt.Sprintf("%s%s", username, userSuffix)
}
