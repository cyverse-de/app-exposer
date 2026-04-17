// Package main provides a CLI tool that generates AnalysisBundle JSON by
// calling app-exposer's dry-run endpoint. Useful for debugging, testing,
// and manual operator interaction without going through a full launch flow.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/cyverse-de/app-exposer/cmd/vicetools"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/model/v10"
)

func main() {
	jobFile := flag.String("job", "", "Path to JSON file containing a model.Job (mode 1).")
	exportFile := flag.String("export-file", "", "Path to vice-export JSON file (mode 2).")
	appExposerURL := flag.String("app-exposer-url", "", "Base URL of app-exposer. Required.")
	user := flag.String("user", "", "Username for the launch (mode 2).")
	userID := flag.String(constants.UserIDLabel, "", "User UUID (mode 2).")
	outputDir := flag.String("output-dir", "", "iRODS output directory (mode 2).")
	email := flag.String("email", "", "User email (mode 2, optional).")
	outFile := flag.String("out", "", "Output file path. Defaults to stdout.")
	validate := flag.Bool("validate", false, "Ask app-exposer to validate the job before building the bundle.")

	flag.Parse()

	if *appExposerURL == "" {
		log.Fatal("--app-exposer-url must be set.")
	}

	if *jobFile == "" && *exportFile == "" {
		log.Fatal("Either --job or --export-file must be set.")
	}
	if *jobFile != "" && *exportFile != "" {
		log.Fatal("Only one of --job or --export-file may be set, not both.")
	}

	var job *model.Job

	if *jobFile != "" {
		// Mode 1: Raw job JSON.
		f, err := os.Open(*jobFile)
		if err != nil {
			log.Fatalf("opening job file: %v", err)
		}
		defer func() { _ = f.Close() }()

		job = &model.Job{}
		if err := json.NewDecoder(f).Decode(job); err != nil {
			log.Fatalf("decoding job JSON: %v", err)
		}
	} else {
		// Mode 2: Export file + runtime flags.
		if *user == "" {
			log.Fatal("--user must be set in export mode.")
		}
		if *userID == "" {
			log.Fatal("--user-id must be set in export mode.")
		}
		if *outputDir == "" {
			log.Fatal("--output-dir must be set in export mode.")
		}

		f, err := os.Open(*exportFile)
		if err != nil {
			log.Fatalf("opening export file: %v", err)
		}
		defer func() { _ = f.Close() }()

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

		job, err = vicetools.ConvertToJob(&export, params)
		if err != nil {
			log.Fatalf("converting export to job: %v", err)
		}
	}

	// POST the job to app-exposer's dry-run endpoint.
	body, err := json.Marshal(job)
	if err != nil {
		log.Fatalf("marshaling job: %v", err)
	}

	url := fmt.Sprintf("%s/vice/dry-run", *appExposerURL)
	if *validate {
		url += "?validate=true"
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("posting to %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("reading response body: %v", err)
	}

	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "HTTP %d %s\n", resp.StatusCode, resp.Status)
		if len(respBody) > 0 {
			fmt.Fprintln(os.Stderr, string(respBody))
		}
		os.Exit(1)
	}

	// Pretty-print the JSON response.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, respBody, "", "  "); err != nil {
		// Fall back to raw output if indentation fails.
		pretty.Reset()
		pretty.Write(respBody)
	}
	pretty.WriteByte('\n')

	// Write to file or stdout.
	if *outFile != "" {
		if err := os.WriteFile(*outFile, pretty.Bytes(), 0644); err != nil {
			log.Fatalf("writing output file: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Bundle written to %s\n", *outFile)
	} else {
		fmt.Print(pretty.String())
	}
}
