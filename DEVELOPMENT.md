# Diun Docker Development Guide

## Prerequisites

Ensure the following dependencies are installed prior to local development:
- Git
- Go 1.27+
- GNU Make
- Docker and Docker Compose (required for building the minimal `FROM scratch` image)
- golangci-lint (required for strict Go code linting)
- govulncheck (required for Go vulnerability checking)
- hadolint (required for `Dockerfile` linting)
- trivy (required for container and filesystem security scanning)
- gitleaks (required for repository secret scanning)

## Repository Initialization

1. **Clone the repository:**
   ```sh
   git clone https://github.com/underhax/diun-docker
   cd diun-docker
   ```

2. **Install Go and Security Tooling:**
   Install the required Go linter, vulnerability checker, and security scanners using any convenient method for your platform. These tools are utilized in our CI pipeline and are required for local code validation prior to submission:
   - **golangci-lint**: [https://golangci-lint.run/docs/welcome/install/](https://golangci-lint.run/docs/welcome/install/)
   - **govulncheck**: `go install golang.org/x/vuln/cmd/govulncheck@latest`
   - **hadolint**: [https://github.com/hadolint/hadolint#install](https://github.com/hadolint/hadolint#install)
   - **trivy**: [https://trivy.dev/docs/latest/getting-started/](https://trivy.dev/docs/latest/getting-started/)
   - **gitleaks**: [https://github.com/gitleaks/gitleaks#installing](https://github.com/gitleaks/gitleaks#installing)

3. **Repository Layout:**
   ```text
   ├── Dockerfile                     # Multi-stage scratch container build
   ├── docker-compose.yaml            # Production deployment manifest
   ├── docker-compose.dev.yaml        # Local build and development manifest
   ├── Makefile                       # Verification, linting, and testing targets
   ├── .golangci.yml                  # Strict Go linter configuration
   ├── go.mod                         # Go module definition
   ├── init/                          # Static Go entrypoint (secret mediation & process exec)
   │   ├── main.go
   │   └── main_test.go
   ├── custom-images.yaml             # Custom image list watched by file provider
   └── .env.example                   # Environment variable template
   ```

## Code Validation & Testing

The project enforces strict code quality and security standards across the Go helper binary (`init`), `Dockerfile`, and Compose manifests. All validation steps are centralized in the [`Makefile`](Makefile).

1. **Go Formatting, Linting & Unit Tests:**
   To validate Go code formatting (`gofmt -s`), execute `golangci-lint`, verify compilation, and run race-enabled unit tests:
   ```sh
   make check
   make test
   ```

2. **Vulnerability & Secret Scanning:**
   To execute Go vulnerability checks, filesystem security scans, and repository secret detection:
   ```sh
   make vulncheck
   make security-trivy
   make secrets-scan
   ```

3. **Docker Linting:**
   To validate `Dockerfile` best practices via `hadolint`:
   ```sh
   make docker-lint
   ```

4. **Code Coverage:**
   To execute the Go test suite with the race detector enabled and generate a function-level coverage report for `init`:
   ```sh
   make coverage
   ```
   To remove generated coverage artifacts afterward:
   ```sh
   make clean
   ```

5. **Project Verification:**
   Prior to submitting any pull request, you must execute the complete validation, testing, and security suite:
   ```sh
   make verify
   ```

## Build Workflow

The repository builds a zero-dependency static Go binary (`init` with `CGO_ENABLED=0`) and packages it alongside the upstream `diun` binary into a minimal `FROM scratch` container image.

1. **Compile the Go binary locally:**
   ```sh
   CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /dev/null ./init
   ```

2. **Cross-compile the Go binary for target container architectures (Optional):**
   You can verify cross-compilation for the supported Linux container architectures (`amd64` and `arm64`) by specifying `GOOS` and `GOARCH`:

   **Linux (AMD64):**
   ```sh
   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -trimpath -o /dev/null ./init
   ```

   **Linux (ARM64):**
   ```sh
   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -trimpath -o /dev/null ./init
   ```

3. **Build the Docker image locally:**
   Build the scratch image natively for your local architecture:
   ```sh
   docker build -t diun:dev .
   ```

   To explicitly cross-compile for another architecture on a modern Docker Engine with BuildKit:
   ```sh
   # For ARM64 architecture:
   docker build --platform linux/arm64 -t diun:dev .

   # For AMD64 architecture:
   docker build --platform linux/amd64 -t diun:dev .
   ```

4. **Build and run using Docker Compose:**
   You can use [`docker-compose.dev.yaml`](docker-compose.dev.yaml) to build and test the stack locally with all production security constraints (`read_only: true`, `cap_drop: ALL`, `no-new-privileges: true`, in-memory secret mediation) applied:
   ```sh
   # Build the image from source:
   docker compose -f docker-compose.dev.yaml build

   # Start the development stack:
   docker compose -f docker-compose.dev.yaml up -d
   ```

   To explicitly cross-compile for another architecture using Docker Compose:
   ```sh
   # For ARM64 architecture:
   DOCKER_DEFAULT_PLATFORM=linux/arm64 docker compose -f docker-compose.dev.yaml build

   # For AMD64 architecture:
   DOCKER_DEFAULT_PLATFORM=linux/amd64 docker compose -f docker-compose.dev.yaml build
   ```

## Makefile Targets Reference

| Target | Description |
| --- | --- |
| `make check` | Validates Go code formatting (`gofmt -s`), executes `golangci-lint`, and verifies compilation across all packages. |
| `make test` | Executes the Go unit test suite (`./init`) with race condition detection enabled (`-race`). |
| `make coverage` | Executes the Go unit test suite with race detection and outputs a function-level statement coverage report (`coverage.out`). |
| `make vulncheck` | Analyzes Go source code and standard library dependencies for known vulnerabilities using `govulncheck`. |
| `make docker-lint` | Lints the multi-stage `Dockerfile` via `hadolint`. |
| `make security-trivy` | Scans the repository filesystem for `CRITICAL` and `HIGH` severity vulnerabilities using `trivy`. |
| `make secrets-scan` | Scans the repository working tree for hardcoded secrets using `gitleaks`. |
| `make verify` | Sequentially executes `check`, `test`, `vulncheck`, `docker-lint`, `security-trivy`, and `secrets-scan`. **Must be executed prior to submitting a pull request.** |
| `make clean` | Removes generated test coverage artifacts (`coverage` and `coverage.out`). |
