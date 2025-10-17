app-exposer
===========

`app-exposer` is a service for the CyVerse Discovery Environment that provides a CRUD API for managing VICE analyses.

# Development

## Prerequisites

* `just` - A command runner, similar to but different from Make. See [just](https://github.com/casey/just) for more info.
* `go` - The Go programming language. See [Go](https://go.dev) for more info.
* `swag` - A Swagger 2.0 documentation generator for Go. See [swag](https://github.com/swaggo/swag) for more info.

## Build

You will need [just](https://github.com/casey/just) installed, along with the Go programming language and the bash shell.

To build `app-exposer` alone, run:
```bash
just app-exposer
```

To build `workflow-builder` alone, run:
```bash
just workflow-builder
```

To build both, run:
```bash
just
```

To clean your local repo, run:
```bash
just clean
```

Uses Go modules, so make sure you have a generally up-to-date version of Go installed locally.

The API documentation is written using the OpenAPI 3 specification and is located in `api.yml`. You can use redoc to view the documentation locally:

Install:
```npm install -g redoc-cli```

Run:
```redoc-cli serve -w api.yml```

For configuration, use `example-config.yml` as a reference. You'll need to either port-forward to or run `job-status-listener` locally and reference the correct port in the config.

## Command-Line Flags

### Authentication Control

`--disable-vice-proxy-auth` (default: `false`)

Disables authentication in the vice-proxy sidecar containers for VICE applications. When set to `true`, the `--disable-auth` flag is passed to vice-proxy, allowing unauthenticated access to VICE applications. This is intended for development, testing, or scenarios where authentication is handled elsewhere. In production environments, this should remain `false` (the default) to enforce authentication via Keycloak.

## Initializing the Swagger docs with `swag`

The command used to initialize the `./docs` directory with the `swag` tool was

```bash
swag init -g app.go -d cmd/app-exposer/,httphandlers/
```

The General Info file for swag is `cmd/app-exposer/main.go`.
