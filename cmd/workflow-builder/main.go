package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/cyverse-de/app-exposer/batch"
	"github.com/cyverse-de/app-exposer/imageinfo"
	"github.com/cyverse-de/model/v6"
	"gopkg.in/yaml.v3"
)

func main() {
	var (
		err      error
		inputJob model.Job

		job                = flag.String("job", "", "The file containing the job definition. Required.")
		transferImage      = flag.String("transfer-image", "harbor.cyverse.org/de/gocmd:latest", "(optional) Image used to transfer files to/from the data store")
		transferWorkingDir = flag.String("transfer-working-dir", "/de-app-work", "The working directory within the file transfer image.")
		transferLogLevel   = flag.String("transfer-log-level", "debug", "The log level of the output of the file transfer tool.")
		statusSenderImage  = flag.String("status-sender-image", "harbor.cyverse.org/de/url-import:latest", "The image used to send status updates. Must container curl.")
		analysisID         = flag.String("analysis-id", "", "The unique identifier for the analysis.")
		quiet              = flag.Bool("quiet", false, "Whether to turn off printing out the workflow.")
		doSubmit           = flag.Bool("submit", false, "Whether to submit the workflow to the cluster.")
		out                = flag.String("out", "", "The file the workflow will be written to.")
		harborURL          = flag.String("harbor-url", "https://harbor.cyverse.org/api/v2.0/", "The base URL for the harbor instance")
		harborProject      = flag.String("harbor-project", "de", "The project to look up images in.")
		harborUser         = flag.String("harbor-user", "", "The user for harbor lookups.")
		harborPass         = flag.String("harbor-pass", "", "The password for the harbor user.")
	)

	flag.Parse()

	if *job == "" {
		log.Fatal("--job must be set.")
	}

	if *analysisID == "" {
		log.Fatal("--analysis-id must be set.")
	}

	if *harborUser == "" {
		log.Fatal("--harbor-user must be set.")
	}

	if *harborPass == "" {
		log.Fatal("--harbor-pass must be set")
	}

	infoGetter, err := imageinfo.NewHarborInfoGetter(*harborURL, *harborUser, *harborPass)
	if err != nil {
		log.Fatal(err)
	}

	infile, err := os.Open(*job)
	if err != nil {
		log.Fatal(err)
	}
	defer infile.Close()

	if err = json.NewDecoder(infile).Decode(&inputJob); err != nil {
		log.Fatal(err)
	}

	opts := batch.BatchSubmissionOpts{
		FileTransferImage:      *transferImage,
		FileTransferWorkingDir: *transferWorkingDir,
		FileTransferLogLevel:   *transferLogLevel,
		StatusSenderImage:      *statusSenderImage,
		AnalysisID:             *analysisID,
	}

	maker := batch.NewWorkflowMaker(*harborProject, infoGetter, &inputJob)
	workflow := maker.NewWorkflow(&opts)

	if !*quiet {
		var outfile *os.File

		if *out == "" {
			outfile = os.Stdout
		} else {
			outfile, err := os.Create(*out)
			if err != nil {
				log.Fatal(err)
			}
			defer outfile.Close()
		}

		if err = yaml.NewEncoder(outfile).Encode(&workflow); err != nil {
			log.Fatal(err)
		}
	}

	if *doSubmit {
		ctx, cl, err := batch.NewWorkflowServiceClient(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		if _, err = batch.SubmitWorkflow(ctx, cl, workflow); err != nil {
			log.Fatal(err)
		}
	}
}
