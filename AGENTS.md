# NivWest Backend — Agent Development Guidelines

This document serves as the root index for AI coding agents (and human contributors) working on the **NivWest Backend** repository.

To optimize context limits and keep instructions modular, detailed guidelines have been split into the `doc/` directory.

## Required Reading for Agents

Before making any modifications to the codebase, **you must read the relevant documentation** based on your task:

- **[doc/architecture.md](doc/architecture.md)**
  Read this if you are writing or reviewing code. It defines our Clean Architecture, dependency inversion, strict handler/service/repository rules, database concurrency requirements (GORM transactions), and API security.

- **[doc/workflow.md](doc/workflow.md)**
  Read this if you are implementing a new feature, writing tests, or submitting a Pull Request. It provides a step-by-step recipe for extending this codebase, our testing requirements (use in-memory fakes from `internal/testutil`), and our Git branch/commit conventions.

## Project Summary

NivWest Backend is a Go API service powering the NivWest stock trading simulator. 
- **Tech Stack**: Go 1.25, Gin, GORM (PostgreSQL 16)
- **Features**: Market data proxying (Avanza), virtual portfolios with market frictions, Google OAuth 2.0 (PKCE).
- **Golden Rule**: Handlers must never access the database directly. All business logic lives in Services, and all dependencies are injected.
