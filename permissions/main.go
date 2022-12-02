package permissions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var httpClient = http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

// Permissions performs operations related to checking permissions.
type Permissions struct {
	BaseURL string
}

// Resource is an item that can have permissions attached to it in the
// permissions service.
type Resource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"resource_type"`
}

// Subject is an item that accesses resources contained in the permissions
// service.
type Subject struct {
	ID        string `json:"id"`
	SubjectID string `json:"subject_id"`
	SourceID  string `json:"subject_source_id"`
	Type      string `json:"subject_type"`
}

// Permission is an entry from the permissions service that tells what access
// a subject has to a resource.
type Permission struct {
	ID       string   `json:"id"`
	Level    string   `json:"permission_level"`
	Resource Resource `json:"resource"`
	Subject  Subject  `json:"subject"`
}

// PermissionList contains a list of permission returned by the permissions
// service.
type PermissionList struct {
	Permissions []Permission `json:"permissions"`
}

// Lookup contains the information needed to look up
// access permissions.
type Lookup struct {
	Subject      string
	SubjectType  string
	Resource     string
	ResourceType string
}

// GetPermissions returns subjects information about a subject.
func (p *Permissions) GetPermissions(ctx context.Context, lookup *Lookup) (*PermissionList, error) {
	requrl, err := url.Parse(p.BaseURL)
	if err != nil {
		return nil, err
	}

	requrl.Path = filepath.Join(requrl.Path, "permissions/subjects", lookup.SubjectType, lookup.Subject, lookup.ResourceType, lookup.Resource)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requrl.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	retval := &PermissionList{}
	if err = json.Unmarshal(b, retval); err != nil {
		return nil, err
	}

	return retval, nil
}

// IsAllowed will return true if the user is allowed to access the running app
// and false if they're not. An error might be returned as well. Access should
// be denied if an error is returned, even if the boolean return value is true.
func (p *Permissions) IsAllowed(ctx context.Context, user, resource string) (bool, error) {
	lookup := &Lookup{
		Subject:      user,
		SubjectType:  "user",
		Resource:     resource,
		ResourceType: "analysis",
	}

	l, err := p.GetPermissions(ctx, lookup)
	if err != nil {
		return false, err
	}

	if len(l.Permissions) > 0 {
		if l.Permissions[0].Level != "" {
			return true, nil
		}
	}

	return false, nil
}
