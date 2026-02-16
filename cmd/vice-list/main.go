package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const listVICEAppsQuery = `
	SELECT a.id,
	       a.name,
	       av.version,
	       id_data.integrator_name,
	       a.description
	  FROM apps a
	  JOIN app_versions av ON av.app_id = a.id
	  JOIN integration_data id_data ON av.integration_data_id = id_data.id
	  JOIN app_steps s ON s.app_version_id = av.id
	  JOIN tasks t ON s.task_id = t.id
	  JOIN tools tl ON t.tool_id = tl.id
	 WHERE tl.interactive = true
	   AND av.deleted = false
	 ORDER BY a.name ASC, av.version_order DESC
`

type viceApp struct {
	ID             string `db:"id"`
	Name           string `db:"name"`
	Version        string `db:"version"`
	IntegratorName string `db:"integrator_name"`
	Description    string `db:"description"`
}

func main() {
	dbURI := flag.String("db-uri", "", "PostgreSQL connection string. Required.")
	flag.Parse()

	if *dbURI == "" {
		log.Fatal("--db-uri must be set.")
	}

	db, err := sqlx.Connect("postgres", *dbURI)
	if err != nil {
		log.Fatalf("connecting to database: %v", err)
	}
	defer db.Close()

	var apps []viceApp
	if err := db.SelectContext(context.Background(), &apps, listVICEAppsQuery); err != nil {
		log.Fatalf("querying VICE apps: %v", err)
	}

	if len(apps) == 0 {
		fmt.Println("No VICE apps found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tVERSION\tINTEGRATOR\tDESCRIPTION")
	for _, a := range apps {
		desc := strings.Join(strings.Fields(a.Description), " ")
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.ID, a.Name, a.Version, a.IntegratorName, desc)
	}
	w.Flush()
}
