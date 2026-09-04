---
name: git-best-practices
description: Use when creating branches, writing commits, handling merges/rebases, pull requests, .gitignore, hooks, history cleanup, or collaborating with Git in visnyk
---

# Git — Best Practices

## Overview
History is documentation. Write it for the reader in 6 months — not for the committer today. Keep history linear, commits atomic, and secrets out of it forever.

## When to Use
- Creating branches, commits, tags, or PRs
- Merging, rebasing, cherry-picking, or resolving conflicts
- Writing `.gitignore`, `.gitattributes`, `pre-commit`/`pre-push` hooks
- Cleaning history (`rebase -i`, `reset`, `reflog`, `filter-repo`)
- Reviewing `git status`, `git diff`, `git log` before push

When NOT to use: editing code without versioning, one-off local experiments you will discard.

## Branching (visnyk convention)

```
<type>/<YYYY-MM-DD>/<short-kebab-description>
Types: feature | fix | chore | refactor | docs
Example: feature/2026-09-02/whatsapp-check
         fix/2026-09-04/normalizer-plus380
         chore/2026-09-04/fake-maintainer-email
```

- One branch = one purpose. Branch from `main`, keep it short-lived (< 3 days).
- Never commit directly to `main`. Never use `rm -rf` or `git push --force` on `main`.

## Commits

**Conventional Commits for visnyk:**
```
feat: add WhatsApp IsOnWhatsApp check
fix: handle +380 normalization for 099 prefix
chore: replace maintainer email with team@visnyk.local
docs: update ARCHITECTURE for cascade queue
refactor: extract normalizer to internal/normalizer
```

Rules:
- Atomic: one logical change per commit (passes `go vet` + `go test`).
- Message: `type: short imperative (≤72 chars)` + blank line + body (why, not what) + `Refs: #123` if needed.
- No secrets: check `git diff --cached` for `.env`, `master.key`, `*.db`, `telegram-store/`, `whatsapp-store/` before commit.
- Sign if possible: `git commit -S -m "feat: ..."` (GPG/SSH).

```bash
# Before every commit (required by AGENTS.md)
git status
git diff --cached
git log --oneline -5
go vet ./... && go test ./...
```

## Workflow — Before/After

```bash
# ❌ BAD: vague, mixed concerns, secrets, force push to main
git checkout -b fix-stuff
# edit 5 files + .env + visnyk.db
git add .
git commit -m "fix"
git push --force origin main

# ✅ GOOD: atomic, conventional, reviewed, safe push
git checkout main && git pull --ff-only
git checkout -b fix/2026-09-04/normalizer-plus380
# edit only internal/normalizer/phone.go + phone_test.go
git add internal/normalizer/phone.go internal/normalizer/phone_test.go
git diff --cached   # verify no secrets
go vet ./... && go test ./... -v
git commit -m "fix: handle +380 normalization for leading zeros

Normalize 099 123-45-67 and (099)1234567 to E.164 +380991234567.
Filter invalid numbers before messenger checks."
git push -u origin fix/2026-09-04/normalizer-plus380
# open PR → review → squash or rebase-merge
```

## Quick Reference

| Area | Rule | Command |
|------|------|---------|
| **Branch** | `type/YYYY-MM-DD/kebab` | `git checkout -b feature/2026-09-04/cascade-queue` |
| **Commit** | `type: imperative ≤72` | `feat: add cascade WA > TG > VIBER` |
| **Staging** | Stage only intended files | `git add <file>` not `git add .` |
| **Diff** | Review before commit/push | `git diff`, `git diff --cached`, `git diff main...HEAD` |
| **Log** | Linear, readable | `git log --oneline --graph --all -20` |
| **Update** | Rebase private, merge public | `git pull --rebase`, `git rebase main` (private only) |
| **PR** | Small, <400 lines, template | `gh pr create --fill` |
| **Undo** | Safe: `revert`, risky: `reset` | `git revert <sha>`, `git reset --soft HEAD~1` |
| **Stash** | For WIP, not backup | `git stash push -m "wip: cascade"` |
| **Ignore** | Never commit secrets/build | `.gitignore` has `*.db`, `.env`, `/build/bin/`, `wailsjs/` |
| **Hooks** | `pre-commit` vet/test/lint | `golangci-lint run`, `gofmt -l .` |
| **Signing** | Sign commits/tags | `git commit -S`, `git tag -s v0.1.0` |
| **Remote** | Verify before push | `git remote -v`, `git status -b` |

## .gitignore for visnyk (already enforced)

Already in `.gitignore` — never override:
```
/visnyk, /build/bin/, /build/deb/, wailsjs/, frontend/node_modules/, frontend/dist/
telegram-store/, whatsapp-store/, *.db, *.sqlite, master.key, .env, .env.*
```

Check: `git check-ignore -v visnyk.db` and `git ls-files --others --ignored --exclude-standard | head`.

## History Cleanup (use with care)

```bash
# Interactive rebase (private branch only)
git rebase -i HEAD~3  # squash/fixup/reword
# Amend last commit
git commit --amend --no-edit && git push --force-with-lease
# Recover lost work
git reflog && git checkout <sha>
# Remove secrets from history (requires force push + rotation)
git filter-repo --path visnyk.db --invert-paths  # or BFG
# Then: rotate TG_API_HASH, re-create master.key, notify team
```

Never `filter-repo` on public `main` without coordination.

## Pull Requests

- Title: `feat: add WhatsApp QR auth` (same as commit).
- Body: what + why + how tested (`go vet`, `go test`, `wails build`).
- Size: <400 lines; split otherwise.
- Checks: `go vet`, `go test ./... -v`, `golangci-lint`, `gofmt -l .`, arch tests `tests/arch/` if non-trivial.
- Merge: `Squash and merge` for features, `Rebase and merge` for linear history, never `merge --no-ff` without reason.

```bash
gh pr create --title "feat: add cascade WA > TG > VIBER" --body "Closes #12"
gh pr checks --watch
```

## Common Mistakes

| Mistake | Consequence | Fix |
|---------|-------------|-----|
| `git add .` | commits secrets (`*.db`, `.env`) | `git add <intent>` + `git diff --cached` |
| `feat: fix bug` vague message | unreadable `git log` | `fix: handle E.164 for 099 prefix` |
| `git push --force` on `main` | rewrites public history | `git push --force-with-lease` on private branch only |
| Long-lived branch `dev` | merge hell | rebase daily, merge in <3 days |
| Merge `main` into feature repeatedly | noisy graph | `git rebase main` (private) or one final merge |
| Commit `wailsjs/`, `frontend/dist/` | bloat, conflicts | already ignored — don't `git add -f` |
| No `git status` before commit | commits wrong files | alias `gst = git status -sb` |
| Secrets in history | forever in `git log` | `filter-repo` + rotate secrets immediately |

## Verification
```bash
git status -sb && git diff --cached --stat
git log --oneline --graph --all -10
git check-ignore -v visnyk.db telegram-store/session.json .env
go vet ./... && go test ./... -count=1
gofmt -l . && golangci-lint run 2>&1 | head -n 20
gh pr view --json title,commits,checks 2>/dev/null | head
```
