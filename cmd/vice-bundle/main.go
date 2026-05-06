// Package main provides a CLI tool that generates AnalysisBundle JSON by
// calling app-exposer's dry-run endpoint. Useful for debugging, testing,
// and manual operator interaction without going through a full launch flow.
//
// Two subcommands are exposed:
//
//	vice-bundle from-job      Bundle a raw model.Job JSON file.
//	vice-bundle from-export   Bundle a vice-export JSON file plus runtime launch params.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/cyverse-de/app-exposer/cmd/vicetools"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/model/v10"
)

const usage = `Usage: vice-bundle <command> [flags]

Commands:
  from-job      Bundle a raw model.Job JSON file
  from-export   Bundle a vice-export JSON file plus runtime launch params

Use "vice-bundle <command> -h" for help on a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	subcmd := os.Args[1]
	args := os.Args[2:]

	switch subcmd {
	case "from-job":
		runFromJob(args)
	case "from-export":
		runFromExport(args)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage) //nolint:errcheck // failure to print help is not actionable

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

// runFromJob bundles a pre-built model.Job JSON file.
func runFromJob(args []string) {
	fs := flag.NewFlagSet("from-job", flag.ExitOnError)
	appExposerURL := fs.String("app-exposer-url", "", "Base URL of app-exposer. Required.")
	jobFile := fs.String("job", "", "Path to JSON file containing a model.Job. Required.")
	validate := fs.Bool("validate", false, "Ask app-exposer to validate the job before building the bundle.")
	outFile := fs.String("out", "", "Output file path. Defaults to stdout.")
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args) //nolint:errcheck // see comment above

	baseURL := requireBaseURL(*appExposerURL)
	if *jobFile == "" {
		log.Fatal("--job must be set.")
	}

	f, err := os.Open(*jobFile)
	if err != nil {
		log.Fatalf("opening job file: %v", err)
	}
	defer common.LogClose("job file", f)

	job := &model.Job{}
	if err := json.NewDecoder(f).Decode(job); err != nil {
		log.Fatalf("decoding job JSON: %v", err)
	}

	postBundle(baseURL, *validate, *outFile, job)
}

// runFromExport bundles a vice-export JSON file combined with runtime launch params.
func runFromExport(args []string) {
	fs := flag.NewFlagSet("from-export", flag.ExitOnError)
	appExposerURL := fs.String("app-exposer-url", "", "Base URL of app-exposer. Required.")
	exportFile := fs.String("export-file", "", "Path to vice-export JSON file. Required.")
	user := fs.String("user", "", "Username for the launch. Required.")
	userID := fs.String(constants.UserIDLabel, "", "User UUID. Required.")
	outputDir := fs.String("output-dir", "", "iRODS output directory. Required.")
	email := fs.String("email", "", "User email. Optional.")
	validate := fs.Bool("validate", false, "Ask app-exposer to validate the job before building the bundle.")
	outFile := fs.String("out", "", "Output file path. Defaults to stdout.")
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args) //nolint:errcheck // see comment above

	baseURL := requireBaseURL(*appExposerURL)
	if *exportFile == "" {
		log.Fatal("--export-file must be set.")
	}
	if *user == "" {
		log.Fatal("--user must be set.")
	}
	if *userID == "" {
		log.Fatalf("--%s must be set.", constants.UserIDLabel)
	}
	if *outputDir == "" {
		log.Fatal("--output-dir must be set.")
	}

	f, err := os.Open(*exportFile)
	if err != nil {
		log.Fatalf("opening export file: %v", err)
	}
	defer common.LogClose("export file", f)

	var export vicetools.VICEAppExport
	if err := json.NewDecoder(f).Decode(&export); err != nil {
		log.Fatalf("decoding export JSON: %v", err)
	}

	params := vicetools.LaunchParams{
		User:      *user,
		UserID:    *userID,
		OutputDir: *outputDir,
		Email:     *email,
	}

	job, err := vicetools.ConvertToJob(&export, params)
	if err != nil {
		log.Fatalf("converting export to job: %v", err)
	}

	postBundle(baseURL, *validate, *outFile, job)
}

// requireBaseURL parses and validates the --app-exposer-url flag, fataling on
// missing or malformed input. The result is reused as the base for path-aware
// URL composition (per CLAUDE.md, parse once and use url.URL.JoinPath).
func requireBaseURL(raw string) *url.URL {
	if raw == "" {
		log.Fatal("--app-exposer-url must be set.")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Fatalf("invalid --app-exposer-url %q", raw)
	}
	return u
}

// postBundle marshals the job, POSTs it to app-exposer's dry-run endpoint, and
// pretty-prints the response to outFile (or stdout when outFile is empty).
func postBundle(baseURL *url.URL, validate bool, outFile string, job *model.Job) {
	body, err := json.Marshal(job)
	if err != nil {
		log.Fatalf("marshaling job: %v", err)
	}

	endpoint := baseURL.JoinPath("vice", "dry-run")
	if validate {
		q := endpoint.Query()
		q.Set("validate", "true")
		endpoint.RawQuery = q.Encode()
	}

	resp, err := http.Post(endpoint.String(), "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("posting to %s: %v", endpoint, err)
	}
	defer common.CloseBody(resp)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("reading response body: %v", err)
	}

	if resp.StatusCode >= 400 {
		if len(respBody) > 0 {
			log.Fatalf("HTTP %d %s: %s", resp.StatusCode, resp.Status, bytes.TrimRight(respBody, "\n"))
		}
		log.Fatalf("HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Decode and re-encode to pretty-print. Matches vice-export's
	// encoder.SetIndent pattern so the two tools format JSON the same way.
	var decoded any
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		log.Fatalf("decoding response JSON: %v", err)
	}

	var out io.Writer = os.Stdout
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			log.Fatalf("creating output file: %v", err)
		}
		defer common.LogClose("output file", f)
		out = f
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(decoded); err != nil {
		log.Fatalf("encoding JSON: %v", err)
	}

	if outFile != "" {
		fmt.Fprintf(os.Stderr, "Bundle written to %s\n", outFile)
	}
}
