---
name: go-idiomatic
description: Go idiomatic patterns and best practices. Use when writing, reviewing or refactoring Go code: error handling, interfaces, concurrency, project layout, testing.
---

# Go Idiomatic — Best Practices

## When to Apply
- Writing new Go services, handlers, or packages
- Reviewing error handling, interfaces, concurrency
- Refactoring to idiomatic Go (Effective Go)

## Core Rules

1. **Errors**: `if err != nil { return fmt.Errorf("context: %w", err) }` — always wrap, never ignore. No `panic` in libraries.
2. **Interfaces**: small, consumer-defined, mockable. `type Checker interface { IsOnWhatsApp(ctx context.Context, phone string) (bool, error) }`
3. **Context**: first arg `ctx context.Context`, propagate, respect cancellation for WA/TG calls.
4. **Project layout**: `cmd/app/main.go` (thin), `internal/*` (private), `pkg/*` only if reusable.
5. **Concurrency**: goroutines via `errgroup`, channels for signals, never share memory without `sync`.
6. **Naming**: `MixedCaps`, `err`, `ctx`, `svc`, no stutter (`cascade.Service` not `cascade.CascadeService`).
7. **Testing**: table-driven, `t.Helper()`, interfaces mocked, `go test -race`.

## Verification
```bash
go vet ./...
go test ./... -race -count=1
golangci-lint run
gofmt -l .
```
