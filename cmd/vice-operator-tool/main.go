package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/cyverse-de/app-exposer/common"
	"golang.org/x/term"
)

const usage = `Usage: vice-operator-tool [--app-exposer-url URL] <command> [flags]

Commands:
  add      Add a new operator
  list     List configured operators
  delete   Delete an operator by name
  keygen   Generate an AES-256 encryption key

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
	case "delete":
		runDelete(*appExposerURL, subcmdArgs)
	case "keygen":
		runKeygen()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
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

// promptSecret reads a secret from the terminal without echoing.
func promptSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("cannot prompt for %s: stdin is not a terminal (use the corresponding flag instead)", prompt)
	}
	fmt.Fprintf(os.Stderr, "%s: ", prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr) // newline after hidden input
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", prompt, err)
	}
	return string(b), nil
}

func runAdd(baseURLStr string, args []string) {
	u := requireBaseURL(baseURLStr)

	fs := flag.NewFlagSet("add", flag.ExitOnError)
	name := fs.String("name", "", "Operator name (required)")
	opURL := fs.String("url", "", "Operator URL (required)")
	username := fs.String("username", "", "Auth username (required)")
	password := fs.String("password", "", "Auth password (prompted if omitted)")
	encKey := fs.String("encryption-key", "", "Base64-encoded AES-256 encryption key (prompted if omitted)")
	tlsSkip := fs.Bool("tls-skip-verify", false, "Skip TLS certificate verification")
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args)

	if *name == "" || *opURL == "" || *username == "" {
		fmt.Fprintln(os.Stderr, "error: --name, --url, and --username are required")
		fs.Usage()
		os.Exit(1)
	}

	// Prompt for secrets if not provided via flags.
	if *password == "" {
		p, err := promptSecret("Password")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		*password = p
	}
	if *encKey == "" {
		k, err := promptSecret("Encryption key")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		*encKey = k
	}

	if *password == "" {
		fmt.Fprintln(os.Stderr, "error: password must not be empty")
		os.Exit(1)
	}
	if *encKey == "" {
		fmt.Fprintln(os.Stderr, "error: encryption key must not be empty")
		os.Exit(1)
	}

	encrypted, err := common.Encrypt(*password, *encKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error encrypting password: %v\n", err)
		os.Exit(1)
	}

	client := NewOperatorClient(u, http.DefaultClient)
	summary, err := client.AddOperator(context.Background(), &AddOperatorRequest{
		Name:                  *name,
		URL:                   *opURL,
		TLSSkipVerify:         *tlsSkip,
		AuthUser:              *username,
		AuthPasswordEncrypted: encrypted,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Operator %q added successfully (url=%s, tls_skip_verify=%v)\n",
		summary.Name, summary.URL, summary.TLSSkipVerify)
}

func runList(baseURLStr string, args []string) {
	u := requireBaseURL(baseURLStr)

	fs := flag.NewFlagSet("list", flag.ExitOnError)
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args)

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
	fmt.Fprintln(w, "NAME\tURL\tTLS_SKIP_VERIFY") //nolint:errcheck
	for _, op := range ops {
		fmt.Fprintf(w, "%s\t%s\t%v\n", op.Name, op.URL, op.TLSSkipVerify) //nolint:errcheck
	}
	_ = w.Flush()
}

func runDelete(baseURLStr string, args []string) {
	u := requireBaseURL(baseURLStr)

	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	// ExitOnError means Parse calls os.Exit on failure; it never returns a non-nil error.
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: vice-operator-tool delete <operator-name>")
		os.Exit(1)
	}
	name := fs.Arg(0)

	client := NewOperatorClient(u, http.DefaultClient)
	if err := client.DeleteOperator(context.Background(), name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Operator %q deleted.\n", name)
}

func runKeygen() {
	key, err := GenerateKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(key)
	fmt.Fprintln(os.Stderr) // blank line for visual separation before the hint text
	fmt.Fprintln(os.Stderr, "Set this value as the 'encryption.key' setting in the app-exposer configuration.")
	fmt.Fprintln(os.Stderr, "After updating the key and restarting app-exposer, all existing operators must")
	fmt.Fprintln(os.Stderr, "be deleted and re-added so their passwords are encrypted with the new key.")
}
