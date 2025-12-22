# Envoy Ratelimit Project Context

This file is automatically loaded by Claude Code at every session start.

## Project Overview

@../docs/claude-context.md

## Additional Instructions

- When making changes to Redis-related code, always run benchmarks: `cd test/redis && go test -bench=.`
- Before committing, run: `make tests`
- For hot key feature changes, update HOTKEY.md documentation
- All new environment variables must be added to both `src/settings/settings.go` and `README.md`

## Common Tasks Quick Reference

**Build & Test**:
```bash
make compile        # Build
make tests          # Run tests
make docker_image   # Build Docker
```

**Debugging**:
```bash
LOG_LEVEL=debug ./bin/ratelimit
./bin/config_check -config_dir examples/ratelimit/config
```

**Benchmarking**:
```bash
cd test/redis && go test -bench=BenchmarkParallelDoLimit -benchtime=10s
```