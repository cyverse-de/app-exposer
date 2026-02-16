package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/cyverse-de/app-exposer/cmd/vicetools"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

func main() {
	appID := flag.String("app-id", "", "UUID of the app to export. Required.")
	dbURI := flag.String("db-uri", "", "PostgreSQL connection string. Required.")
	out := flag.String("out", "", "Output file path. Defaults to stdout.")

	flag.Parse()

	if *appID == "" {
		log.Fatal("--app-id must be set.")
	}
	if *dbURI == "" {
		log.Fatal("--db-uri must be set.")
	}

	db, err := sqlx.Connect("postgres", *dbURI)
	if err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	defer db.Close()

	export, err := vicetools.ExportApp(context.Background(), db, *appID)
	if err != nil {
		log.Fatalf("exporting app: %v", err)
	}

	var outFile *os.File
	if *out == "" {
		outFile = os.Stdout
	} else {
		outFile, err = os.Create(*out)
		if err != nil {
			log.Fatalf("creating output file: %v", err)
		}
		defer outFile.Close()
	}

	enc := json.NewEncoder(outFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(export); err != nil {
		log.Fatalf("encoding JSON: %v", err)
	}
}
