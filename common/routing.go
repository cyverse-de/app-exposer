package common

import (
	"crypto/sha256"
	"fmt"
)

// Subdomain returns the subdomain to use for the given user ID and job invocation ID.
func Subdomain(userID, invocationID string) string {
	return fmt.Sprintf("a%x", sha256.Sum256(fmt.Appendf([]byte{}, "%s%s", userID, invocationID)))[0:9]
}
