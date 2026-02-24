# General Code Guidelines
* Keep code succinct.
* Add validation both in the backend and frontend.
* Don't repeat yourself needlessly.
* Don't use multiple inheritance.
* Prefer composition over inheritance for new first-party types.
* Use table-driven tests rather than lots of small, similar tests.
* Add doc comments to publicly available methods and functions.
* Document code succinctly but thoroughly.
* Use type hinting in Python.
* Generally treat warnings as errors unless fixing the warning would cause difficult to fix breakages.
* Prefer using the standard library over adding new dependencies unless adding a new dependency is truly the more effective option or is the de-facto standard.
* Do a prettification/clean up pass on all generated code.
* Add good comments to code that may be confusing or doesn't behave in a standard way.
* Add comments to code changed as part of a pull request.
* Keep comments succinct, but thorough.


# Guidelines for Python programming language projects
* Use 'uv' for building, running, and managing Python projects. See https://docs.astral.sh/uv/ for documentation.
* Use 'ruff' for linting and formatting Python code. See https://docs.astral.sh/ruff/ for documentation.


# Guidelines for Go programming language projects
* Follow the standards outlined in Effective Go, found at https://go.dev/doc/effective_go.
* Use the `goimport` tool to format import statements.
* Use the `gofmt` tool to format code.
* Use `golangci-lint` to lint code. See https://golangci-lint.run/docs/ for documentation.
* Do not ignore returned errors.


# Docker guidelines
* Use '--network host' with local Docker containers by default.
* Use '--network host' with Docker if you encounter DNS issues in containers.


# Kubernetes guidelines
* The kubeconfig file for the QA environment is located at `~/.kube/qa.conf`.
* The kubeconfig file for the production environment is located at `~/.kube/prod.conf`.
* The kubeconfig file for the local cluster is located at `~/.kube/local-admin.conf`.
* Use QA kubeconfig file unless told explictly otherwise.
* Don't use the production environment kubeconfig file unless explicitly told to and ask permission first anyway.