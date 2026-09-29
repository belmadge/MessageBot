# Repository Guidelines

## Project Structure & Module Organization

This repository is at its initial stage; no application source or tests exist yet. The planned project is a Go REST service for WhatsApp support, backed by PostgreSQL and Docker Compose. Keep the layout small: `cmd/api` for the entry point, `internal/` for application packages, `migrations/` for SQL changes, and `Dockerfile` plus `compose.yaml` at the root. Put Go tests beside their package. Never commit secrets or local-only configuration.

## Build, Test, and Development Commands

No build tooling is committed yet. Once the Go module and Compose configuration exist, use these commands and update this section if tooling changes:

- `go run ./cmd/api` — run the API locally.
- `go build ./...` — compile all packages.
- `go test ./...` — run the Go test suite.
- `gofmt -w .` — format Go source before committing.
- `docker compose up --build` — start the API and PostgreSQL services.

Run commands from the repository root. Compose requires the database configuration and migrations.

## Coding Style & Naming Conventions

Use `gofmt`, short lowercase package names, exported `PascalCase` identifiers, and unexported `camelCase` identifiers. Separate HTTP handlers, business rules, and database access. Use context-aware database and API calls, and return errors with useful context. Name migrations with an ordered prefix, e.g. `0001_create_conversations.sql`.

## Testing Guidelines

Use Go's standard `testing` package and `*_test.go` filenames. Write focused tests for business logic and handlers; isolate tests from live WhatsApp and AI services with fakes. Add PostgreSQL integration coverage when database setup is available. Run `go test ./...`; no coverage threshold is configured.

## Commit & Pull Request Guidelines

The Git history has no commits, so no convention is established. Use concise imperative subjects (e.g. `Add webhook verification`). Pull requests should describe the change, note configuration or migration steps, link related work, and report tests. Include screenshots if a UI is added.

## Security & Configuration

Store WhatsApp tokens, AI keys, database passwords, and webhook secrets in environment variables. Validate webhook authenticity, make processing safe to retry, and avoid logging message contents or secrets. Document required variables in an example file with placeholders only.
