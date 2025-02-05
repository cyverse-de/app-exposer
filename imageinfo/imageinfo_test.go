package imageinfo

import "testing"

func TestIsHarborRepo(t *testing.T) {
	tests := []struct {
		name string
		repo string
		want bool
	}{
		{
			name: "Valid harbor repo",
			repo: "harbor.cyverse.org/de/url-import",
			want: true,
		},
		{
			name: "Non-harbor repo",
			repo: "docker.io/library/ubuntu",
			want: false,
		},
		{
			name: "Empty repo",
			repo: "",
			want: false,
		},
		{
			name: "Invalid URL",
			repo: "not-a-url/foo/bar",
			want: false,
		},
	}

	h, err := NewHarborInfoGetter("https://harbor.cyverse.org", "", "")
	if err != nil {
		t.Error(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := h.IsInRepo(tt.repo); got != tt.want {
				t.Errorf("IsHarborRepo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRepoParts(t *testing.T) {
	tests := []struct {
		name          string
		repo          string
		wantProject   string
		wantImage     string
		wantTag       string
		wantErr       bool
		wantErrString string
	}{
		{
			name:          "Valid repo with tag",
			repo:          "harbor.cyverse.org/de/url-import:v1.0",
			wantProject:   "de",
			wantImage:     "url-import",
			wantTag:       "v1.0",
			wantErr:       false,
			wantErrString: "",
		},
		{
			name:          "Valid repo without tag",
			repo:          "harbor.cyverse.org/de/url-import",
			wantProject:   "de",
			wantImage:     "url-import",
			wantTag:       "latest",
			wantErr:       false,
			wantErrString: "",
		},
		{
			name:          "Invalid repo - too few parts",
			repo:          "harbor.cyverse.org/de",
			wantProject:   "",
			wantImage:     "",
			wantTag:       "",
			wantErr:       true,
			wantErrString: "harbor.cyverse.org/de does not have enough fields",
		},
	}

	h, err := NewHarborInfoGetter("https://harbor.cyverse.org", "", "")
	if err != nil {
		t.Error(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, image, tag, err := h.RepoParts(tt.repo)

			if tt.wantErr {
				if err == nil {
					t.Errorf("RepoParts() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if err.Error() != tt.wantErrString {
					t.Errorf("RepoParts() error = %v, wantErrString %v", err, tt.wantErrString)
				}
				return
			}

			if err != nil {
				t.Errorf("RepoParts() unexpected error = %v", err)
				return
			}

			if project != tt.wantProject {
				t.Errorf("RepoParts() project = %v, want %v", project, tt.wantProject)
			}
			if image != tt.wantImage {
				t.Errorf("RepoParts() image = %v, want %v", image, tt.wantImage)
			}
			if tag != tt.wantTag {
				t.Errorf("RepoParts() tag = %v, want %v", tag, tt.wantTag)
			}
		})
	}
}
