app-exposer
===========

This is a service that runs inside of a Kubernetes cluster namespace and implements CRUD operations for exposing interactive apps as a Service and Endpoint.

# Development

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
