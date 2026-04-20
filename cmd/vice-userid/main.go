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

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
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
	username := flag.String(constants.UsernameLabel, "", "Username to look up. Required.")
	userSuffix := flag.String("user-suffix", "@iplantcollaborative.org", "Domain suffix appended to the username if not already present.")
	flag.Parse()

	if *dbURI == "" {
		log.Fatal("--db-uri must be set.")
	}
	if *username == "" {
		log.Fatal("--username must be set.")
	}

	// Ensure the username has the expected domain suffix. If the configured
	// suffix doesn't start with @, insert one so callers can pass
	// `--user-suffix iplantcollaborative.org` (without the @) and still get
	// `user@iplantcollaborative.org`.
	name := *username
	if !strings.HasSuffix(name, *userSuffix) {
		if !strings.HasPrefix(*userSuffix, "@") {
			name += "@"
		}
		name += *userSuffix
	}

	db, err := sqlx.Connect("postgres", *dbURI)
	if err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	defer common.LogClose("database", db)

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
