package imageinfo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type ImageInfo struct {
	Digest       string
	Architecture string
	Author       string
	Entrypoint   string
	Env          []string
	Labels       map[string]string
	WorkingDir   string
	Created      string
	OS           string
}

type HarborInfoGetter struct {
	url    *url.URL
	auth   string
	client *http.Client
}

type InfoGetter interface {
	GetInfo(project, image, tag string) (*ImageInfo, error)
	IsInRepo(repo string) bool
	RepoParts(repo string) (project, image, tag string, err error)
}

func NewHarborInfoGetter(server, user, password string) (*HarborInfoGetter, error) {
	harborURL, err := url.Parse(server)
	if err != nil {
		return nil, err
	}
	authString := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", user, password)))
	return &HarborInfoGetter{
		url:    harborURL,
		auth:   authString,
		client: &http.Client{},
	}, nil
}

var replyData struct {
	Digest     string `json:"digest"`
	ExtraAttrs struct {
		Architecture string `json:"architecture"`
		Author       string `json:"author"`
		Config       struct {
			Entrypoint []string          `json:"Entrypoint"`
			WorkingDir string            `json:"WorkingDir"`
			Env        []string          `json:"Env"`
			Labels     map[string]string `json:"Labels"`
		} `json:"config"`
		OS      string `json:"os"`
		Created string `json:"created"`
	} `json:"extra_attrs"`
}

// IsInRepo returns whether the provided repo says it is present in the configured
// repository instance. Does not hit the repo API.
func (h *HarborInfoGetter) IsInRepo(repo string) bool {
	return strings.HasPrefix(repo, h.url.Hostname())
}

func (h *HarborInfoGetter) RepoParts(repo string) (project, image, tag string, err error) {
	parts := strings.SplitN(repo, "/", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("%s does not have enough fields", repo)
	}

	project = parts[1]
	tag = "latest"
	image = parts[2]

	// Extract the tag if it's specified, otherwise default to 'latest'.
	imageParts := strings.SplitN(parts[2], ":", 2)
	if len(imageParts) > 1 {
		image = imageParts[0]
		tag = imageParts[1]
	}

	return project, image, tag, nil
}

// GetInfo accepts a string containing a container image repo
// (e.g. "harbor.cyverse.org/de/url-import:latest") and retrieves information about it
// from the harbor instance accessible at the HarborInfoGetter's url
// field. The return value contains information parsed out of the harbor
// instance's response body.
func (h *HarborInfoGetter) GetInfo(project, image, tag string) (*ImageInfo, error) {
	endpoint := h.url.JoinPath(
		fmt.Sprintf("/projects/%s/repositories/%s/artifacts/%s", project, image, tag),
	)

	req, err := http.NewRequest("GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %s", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", h.auth))
	req.Header.Set("accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %s", err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&replyData); err != nil {
		return nil, fmt.Errorf("error decoding response: %s", err)
	}

	entrypoint := ""
	if len(replyData.ExtraAttrs.Config.Entrypoint) > 0 {
		entrypoint = strings.Join(replyData.ExtraAttrs.Config.Entrypoint, " ")
	}

	return &ImageInfo{
		Digest:       replyData.Digest,
		Architecture: replyData.ExtraAttrs.Architecture,
		Author:       replyData.ExtraAttrs.Author,
		Entrypoint:   entrypoint,
		WorkingDir:   replyData.ExtraAttrs.Config.WorkingDir,
		Env:          replyData.ExtraAttrs.Config.Env,
		Labels:       replyData.ExtraAttrs.Config.Labels,
		Created:      replyData.ExtraAttrs.Created,
		OS:           replyData.ExtraAttrs.OS,
	}, nil
}
