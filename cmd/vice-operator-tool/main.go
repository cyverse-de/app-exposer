package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/google/uuid"
)

const usage = `Usage: vice-operator-tool [--app-exposer-url URL] <command> [flags]

Commands:
  add      Add a new operator
  list     List configured operators
  update   Update an existing operator (partial update by name)
  delete   Delete an operator by name

Use "vice-operator-tool <command> -h" for help on a command.
`

func main() {
	// Parse the global --app-exposer-url flag. The standard flag package stops
	// at the first non-flag argument, so remaining args start with the subcommand.
	globalFlags := flag.NewFlagSet("global", flag.ContinueOnError)
	appExposerURL := globalFlags.String("app-exposer-url", "", "Base URL of the app-exposer instance (required for add/list/delete)")

	if err := globalFlags.Parse(os.Args[1:]); err != nil {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	remaining := globalFlags.Args()
	if len(remaining) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	subcmd := remaining[0]
	subcmdArgs := remaining[1:]

	switch subcmd {
	case "add":
		runAdd(*appExposerURL, subcmdArgs)
	case "list":
		runList(*appExposerURL, subcmdArgs)
	case "update":
		runUpdate(*appExposerURL, subcmdArgs)
	case "delete":
		runDelete(*appExposerURL, subcmdArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

// derefOr returns the pointee of p, or fallback when p is nil. Used to render
// the nullable base_url field for human-readable output.
func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// requireBaseURL validates and parses the --app-exposer-url flag.
func requireBaseURL(raw string) *url.URL {
	if raw == "" {
		fmt.Fprintln(os.Stderr, "error: --app-exposer-url is required")
		os.Exit(1)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		fmt.Fprintf(os.Stderr, "error: invalid --app-exposer-url %q\n", raw)
		os.Exit(1)
	}
	return u
}

func runAdd(baseURLStr string, args []string) {
	u := requireBaseURL(baseURLStr)

	fs := flag.NewFlagSet("add", flag.ExitOnError)
	name := fs.String("name", "", "Operator name (required)")
	opURL := fs.String("url", "", "Operator URL (required)")
	baseURL := fs.String("base-url", "", "VICE landing-domain base URL for analyses on this operator (required)")
	tlsSkip := fs.Bool("tls-skip-verify", false, "Skip TLS certificate verification")
	priority := fs.Int("priority", 0, "Scheduling priority (lower = tried first, default 0)")
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args) //nolint:errcheck // see comment above

	if *name == "" || *opURL == "" || *baseURL == "" {
		fmt.Fprintln(os.Stderr, "error: --name, --url, and --base-url are required")
		fs.Usage()
		os.Exit(1)
	}

	client := NewOperatorClient(u, http.DefaultClient)
	summary, err := client.AddOperator(context.Background(), &operatorclient.OperatorConfig{
		Name:          *name,
		URL:           *opURL,
		TLSSkipVerify: *tlsSkip,
		Priority:      *priority,
		BaseURL:       baseURL,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Operator %q added successfully (id=%s, url=%s, base_url=%s, priority=%d, tls_skip_verify=%v)\n",
		summary.Name, summary.ID, summary.URL, derefOr(summary.BaseURL, "-"), summary.Priority, summary.TLSSkipVerify)
}

// resolveOperatorID looks up an operator by name and returns its UUID.
// Used by the update and delete subcommands so admins identify operators
// by their human-readable name while the API endpoints take stable ids.
// Returns a clear "no operator named X" message if no match is found.
func resolveOperatorID(client *OperatorClient, name string) (uuid.UUID, error) {
	ops, err := client.ListOperators(context.Background())
	if err != nil {
		return uuid.Nil, fmt.Errorf("listing operators: %w", err)
	}
	for i := range ops {
		if ops[i].Name == name {
			return ops[i].ID, nil
		}
	}
	return uuid.Nil, fmt.Errorf("no operator found with name %q", name)
}

func runList(baseURLStr string, args []string) {
	u := requireBaseURL(baseURLStr)

	fs := flag.NewFlagSet("list", flag.ExitOnError)
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args) //nolint:errcheck // see comment above

	client := NewOperatorClient(u, http.DefaultClient)
	ops, err := client.ListOperators(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(ops) == 0 {
		fmt.Println("No operators configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tURL\tBASE_URL\tPRIORITY\tTLS_SKIP_VERIFY") //nolint:errcheck
	for _, op := range ops {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%v\n", op.ID, op.Name, op.URL, derefOr(op.BaseURL, "-"), op.Priority, op.TLSSkipVerify) //nolint:errcheck
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "error: flushing tabwriter: %v\n", err)
	}
}

func runUpdate(baseURLStr string, args []string) {
	u := requireBaseURL(baseURLStr)

	fs := flag.NewFlagSet("update", flag.ExitOnError)
	name := fs.String("name", "", "Current operator name (required, used to look up the row)")
	newName := fs.String("new-name", "", "New operator name (rename)")
	opURL := fs.String("url", "", "New operator URL")
	baseURL := fs.String("base-url", "", "New VICE landing-domain base URL")
	tlsSkip := fs.Bool("tls-skip-verify", false, "Set TLS skip-verify flag")
	priority := fs.Int("priority", 0, "Set scheduling priority")
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args) //nolint:errcheck // see comment above

	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required")
		fs.Usage()
		os.Exit(1)
	}

	// Build the partial-update body from flags that were explicitly set.
	// flag.Visit reports only flags the user passed on the command line;
	// untouched flags map to nil pointers so the server's COALESCE leaves
	// those columns unchanged.
	req := &operatorclient.UpdateOperatorRequest{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "new-name":
			req.Name = newName
		case "url":
			req.URL = opURL
		case "base-url":
			req.BaseURL = baseURL
		case "tls-skip-verify":
			req.TLSSkipVerify = tlsSkip
		case "priority":
			req.Priority = priority
		}
	})

	if req.Name == nil && req.URL == nil && req.BaseURL == nil && req.TLSSkipVerify == nil && req.Priority == nil {
		fmt.Fprintln(os.Stderr, "error: at least one of --new-name, --url, --base-url, --tls-skip-verify, --priority must be set")
		os.Exit(1)
	}

	client := NewOperatorClient(u, http.DefaultClient)

	// Resolve --name to an id. The PATCH endpoint identifies the row by
	// UUID so that a concurrent rename can't redirect the update to a
	// different row.
	id, err := resolveOperatorID(client, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	summary, err := client.UpdateOperator(context.Background(), id, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// summary.Name reads the *new* name through the embedded OperatorConfig,
	// so when this is a rename the printed line shows the post-rename
	// state. *name (from the flag) is the lookup key; we print both as
	// "old → new" only when they differ to keep the steady-state path
	// terse.
	if *name != summary.Name {
		fmt.Printf("Operator %q → %q updated successfully (id=%s, url=%s, base_url=%s, priority=%d, tls_skip_verify=%v)\n",
			*name, summary.Name, summary.ID, summary.URL, derefOr(summary.BaseURL, "-"), summary.Priority, summary.TLSSkipVerify)
	} else {
		fmt.Printf("Operator %q updated successfully (id=%s, url=%s, base_url=%s, priority=%d, tls_skip_verify=%v)\n",
			summary.Name, summary.ID, summary.URL, derefOr(summary.BaseURL, "-"), summary.Priority, summary.TLSSkipVerify)
	}
}

func runDelete(baseURLStr string, args []string) {
	u := requireBaseURL(baseURLStr)

	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args) //nolint:errcheck // see comment above

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: vice-operator-tool delete <operator-name>")
		os.Exit(1)
	}
	name := fs.Arg(0)

	client := NewOperatorClient(u, http.DefaultClient)

	// The DELETE endpoint takes a UUID; resolve the human-readable name
	// to an id via the listing. This is one extra round-trip versus a
	// name-keyed delete, but it keeps all admin endpoints id-keyed which
	// is safer in the presence of concurrent renames.
	id, err := resolveOperatorID(client, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := client.DeleteOperator(context.Background(), id); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Operator %q (id=%s) deleted.\n", name, id)
}
