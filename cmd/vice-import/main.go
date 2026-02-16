package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/cyverse-de/app-exposer/cmd/vicetools"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

func main() {
	file := flag.String("file", "", "Path to JSON export file. Required.")
	dbURI := flag.String("db-uri", "", "PostgreSQL connection string. Required.")

	flag.Parse()

	if *file == "" {
		log.Fatal("--file must be set.")
	}
	if *dbURI == "" {
		log.Fatal("--db-uri must be set.")
	}

	f, err := os.Open(*file)
	if err != nil {
		log.Fatalf("opening file: %v", err)
	}
	defer f.Close()

	var export vicetools.VICEAppExport
	if err := json.NewDecoder(f).Decode(&export); err != nil {
		log.Fatalf("decoding JSON: %v", err)
	}

	db, err := sqlx.Connect("postgres", *dbURI)
	if err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	defer db.Close()

	result, err := vicetools.ImportApp(context.Background(), db, &export)
	if err != nil {
		log.Fatalf("importing app: %v", err)
	}

	fmt.Printf("Import successful:\n")
	fmt.Printf("  App ID:     %s\n", result.AppID)
	fmt.Printf("  Version ID: %s\n", result.VersionID)
	fmt.Printf("  Tool ID:    %s\n", result.ToolID)
}
