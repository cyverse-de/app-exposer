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
	"github.com/cyverse-de/model/v9"
)

func main() {
	jobFile := flag.String("job", "", "Path to JSON file containing a model.Job (mode 1).")
	exportFile := flag.String("export-file", "", "Path to vice-export JSON file (mode 2).")
	appExposerURL := flag.String("app-exposer-url", "", "Base URL of app-exposer. Required.")
	user := flag.String("user", "", "Username for the launch (mode 2).")
	userID := flag.String("user-id", "", "User UUID (mode 2).")
	outputDir := flag.String("output-dir", "", "iRODS output directory (mode 2).")
	email := flag.String("email", "", "User email (mode 2, optional).")

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
		// Mode 1: Raw job JSON
		f, err := os.Open(*jobFile)
		if err != nil {
			log.Fatalf("opening job file: %v", err)
		}
		defer f.Close()

		job = &model.Job{}
		if err := json.NewDecoder(f).Decode(job); err != nil {
			log.Fatalf("decoding job JSON: %v", err)
		}
	} else {
		// Mode 2: Export file + runtime flags
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
		defer f.Close()

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

	// POST the job to app-exposer
	body, err := json.Marshal(job)
	if err != nil {
		log.Fatalf("marshaling job: %v", err)
	}

	url := fmt.Sprintf("%s/vice/launch", *appExposerURL)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("posting to %s: %v", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("HTTP %d %s\n", resp.StatusCode, resp.Status)
	if len(respBody) > 0 {
		fmt.Println(string(respBody))
	}

	if resp.StatusCode >= 400 {
		os.Exit(1)
	}
}
