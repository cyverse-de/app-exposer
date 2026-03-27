// Package main implements a CLI tool that looks up a user's UUID from the
// DE database given their username. Output is pipe-friendly (bare UUID on stdout).
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const userByUsername = `
	SELECT u.id
	  FROM users u
	 WHERE u.username = $1
`

func main() {
	dbURI := flag.String("db-uri", "", "PostgreSQL connection string. Required.")
	username := flag.String("username", "", "Username to look up. Required.")
	userSuffix := flag.String("user-suffix", "@iplantcollaborative.org", "Domain suffix appended to the username if not already present.")
	flag.Parse()

	if *dbURI == "" {
		log.Fatal("--db-uri must be set.")
	}
	if *username == "" {
		log.Fatal("--username must be set.")
	}

	// Ensure the username has the expected domain suffix.
	name := *username
	if !strings.HasSuffix(name, *userSuffix) {
		name += *userSuffix
	}

	db, err := sqlx.Connect("postgres", *dbURI)
	if err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	defer func() { _ = db.Close() }()

	var id string
	if err := db.QueryRowContext(context.Background(), userByUsername, name).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = fmt.Fprintf(os.Stderr, "user not found: %s\n", name)
			os.Exit(1)
		}
		log.Fatalf("querying user ID: %v", err)
	}

	fmt.Println(id)
}
