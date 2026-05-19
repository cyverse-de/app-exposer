package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// mustEnsure runs fn — a function intended to ensure that a resource
// exists in Kubernetes — with a 30-second timeout, and fatally exits on
// error. This eliminates the repetitive context-create / defer-cancel /
// log.Fatalf boilerplate at each startup ensure-call site.
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

// parseAdminEntitlements splits a comma-separated entitlement list, trims
// whitespace, and drops empty entries. An empty input yields an empty slice
// (which means "no entitlement-claim values are accepted as admin").
func parseAdminEntitlements(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// stringSliceFlag implements flag.Value for repeatable string flags.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(val string) error {
	*s = append(*s, val)
	return nil
}

// nameValueMapFlag implements flag.Value for repeatable NAME:VALUE pairs
// that build up a map. Used for --gpu-model-mapping. Repeated keys
// overwrite earlier values (documented behavior; the last Set wins).
type nameValueMapFlag map[string]string

func (m *nameValueMapFlag) String() string {
	if *m == nil {
		return ""
	}
	pairs := make([]string, 0, len(*m))
	for k, v := range *m {
		pairs = append(pairs, k+":"+v)
	}
	slices.Sort(pairs)
	return strings.Join(pairs, ",")
}

func (m *nameValueMapFlag) Set(val string) error {
	name, value, ok := strings.Cut(val, ":")
	if !ok || name == "" || value == "" {
		return fmt.Errorf("invalid NAME:VALUE pair %q (expected NAME:VALUE with non-empty halves)", val)
	}
	if *m == nil {
		*m = make(map[string]string)
	}
	(*m)[name] = value
	return nil
}
