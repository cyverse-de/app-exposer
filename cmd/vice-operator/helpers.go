package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// mustEnsure runs fn with a 30-second timeout and fatally exits on error.
// This eliminates the repetitive context-create / defer-cancel / log.Fatalf
// boilerplate for each startup ensure call.
func mustEnsure(resource string, fn func(ctx context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := fn(ctx); err != nil {
		log.Fatalf("failed to ensure %s: %v", resource, err)
	}
}

// parseSelector parses a comma-separated "key=value,key2=value2" string into
// a label map suitable for a Service selector.
func parseSelector(s string) (map[string]string, error) {
	result := make(map[string]string)
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
			return nil, fmt.Errorf("invalid selector term %q (expected key=value with non-empty key and value)", part)
		}
		result[kv[0]] = kv[1]
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("selector must contain at least one key=value pair")
	}
	return result, nil
}

// stringSliceFlag implements flag.Value for repeatable string flags.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(val string) error {
	*s = append(*s, val)
	return nil
}
