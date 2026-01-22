# Contributing to rustydocs

Thank you for your interest in contributing to rustydocs! This document provides guidelines and instructions for contributing.

## Code of Conduct

Be respectful and constructive in all interactions. We welcome contributors of all experience levels.

## Getting Started

### Prerequisites

- Go 1.21 or later
- Git

### Development Setup

1. Fork the repository on GitHub
2. Clone your fork:
   ```bash
   git clone https://github.com/YOUR_USERNAME/rustydocs.git
   cd rustydocs
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/nrynss/rustydocs.git
   ```
4. Build the project:
   ```bash
   go build -o rustydocs ./cmd/rustydocs
   ```

## Making Changes

### Branch Workflow

1. Sync your fork with upstream:
   ```bash
   git fetch upstream
   git checkout main
   git merge upstream/main
   ```
2. Create a feature branch:
   ```bash
   git checkout -b feature/your-feature-name
   ```
3. Make your changes
4. Commit with clear, descriptive messages

### Code Style

This project follows standard Go conventions:

- Run `gofmt -w .` before committing (CI will fail otherwise)
- Run `go vet ./...` to catch common issues
- Keep functions focused and reasonably sized
- Add comments for non-obvious logic

### Testing

Run tests before submitting:

```bash
go test -v ./...
```

### Building

```bash
# Standard build
go build -o rustydocs ./cmd/rustydocs

# Build with version info
make build
```

## Submitting Changes

### Pull Request Process

1. Push your branch to your fork:
   ```bash
   git push origin feature/your-feature-name
   ```
2. Open a Pull Request against `main`
3. Fill out the PR template with:
   - Summary of changes
   - Test plan describing how you verified the changes
4. Wait for CI checks to pass
5. Address any review feedback

### PR Requirements

- All CI checks must pass (build, test, lint)
- At least one approving review is required
- Keep PRs focused on a single change

## Reporting Issues

### Bug Reports

Include:
- Go version (`go version`)
- Operating system
- Steps to reproduce
- Expected vs actual behavior
- Relevant error messages or logs

### Feature Requests

Include:
- Use case description
- Proposed solution (if any)
- Alternatives considered

## Project Structure

```
cmd/rustydocs/          CLI entry point
internal/
  analyzer/             Core orchestration, parallel file analysis
  config/               Configuration loading and validation
  git/                  Git blame/log integration
  parser/               Markdown parsing, section extraction
  report/               Report generators (Markdown, HTML, JSON)
templates/              HTML report template (embedded)
```

## Labels

When opening issues, maintainers will add relevant labels:

| Label | Description |
|-------|-------------|
| `bug` | Something isn't working |
| `enhancement` | New feature or improvement |
| `good first issue` | Good for newcomers |
| `help wanted` | Extra attention needed |
| `git-integration` | Related to git blame/log |
| `parser` | Markdown parsing |
| `report` | Report generation |
| `config` | Configuration/CLI options |
| `performance` | Performance improvements |
| `windows` | Windows-specific issues |

## Questions?

- Open a [Discussion](https://github.com/nrynss/rustydocs/discussions) for general questions
- Check existing issues before opening a new one

## License

By contributing, you agree that your contributions will be licensed under the Apache 2.0 License.
