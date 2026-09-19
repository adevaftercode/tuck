# Weft

Weft is a small, deterministic CLI for operating a product board stored in
`WEFT.md` and Markdown task files. Task records are the source of truth;
`WEFT.md` is a generated view.

## Build and install

Weft requires Go 1.25 or newer.

```sh
go build -o weft ./cmd/weft
go install ./cmd/weft
```

The binary has no server or runtime setup. The same source builds on Linux,
Windows, and macOS.

## Start a board

```sh
weft init
weft add "Product image management" --state todo
weft list
weft start 001
weft show 001
weft done 001
weft recent
weft reopen 001
weft sync
weft check
```

Inspection and mutation commands accept `--json` for structured output,
including `init`, `sync`, and `check`.

`add` defaults to `backlog`. Task handles remain stable when a task moves or
changes priority. Reorder active tasks with:

```sh
weft reorder 004 --first
weft reorder 004 --before 002
weft reorder 004 --after 002
```

Use `weft --help` or `weft <command> --help` for the complete command surface.

## Task files

Tasks live in `tasks/backlog/`, `tasks/todo/`, `tasks/doing/`, and
`tasks/done/`. A task is Markdown with a short Weft-owned front matter section:

```md
---
id: wft_01K5Z4KJ6M2V7D9X3R8T5Q1P0A
number: 1
order: 1
created_at: "2026-09-19T12:30:00Z"
priority: 2
labels: ["mobile", "images"]
---
# Product image management

Task context stays ordinary Markdown and can be edited directly.
```

`id`, `number`, `order`, `created_at`, and `completed_at` are reserved for
Weft. User metadata uses any other top-level key and supports strings, numbers,
booleans, `null`, and flat arrays of those values. Quote date-like strings.
Weft preserves user metadata when moving, renaming, and reordering tasks.

Create, inspect, and remove metadata fields with typed JSON values:

```sh
weft meta set 001 priority 3
weft meta set 001 labels '["mobile", "images"]'
weft meta get 001
weft meta unset 001 priority
weft find images --meta priority=3
```

`show` accepts a number or full task ID. It prints the canonical Markdown;
`--json` returns structured task records with metadata.

## Board projection and validation

`WEFT.md` lists all Doing tasks, the first 10 Todo and Backlog tasks, and the
10 most recently completed tasks. `sync` regenerates the projection only when
all task records are valid. It warns and leaves `WEFT.md` untouched otherwise.
`check` validates records and reports a stale projection without editing it.

The task directory determines each task's state. The first level-one Markdown
heading determines its title. Use `weft rename` when changing a title so the
filename stays readable.

## Git worktrees and concurrent use

When invoked from a linked Git worktree, Weft resolves the primary worktree so
workers share one board. `--root PATH` selects an explicit board. Weft serializes
commands with an OS file lock. Multi-file updates are journaled; after an
interrupted update, the next command finishes recovery before reading the board.

## Development

```sh
go test ./...
go test ./acceptance
go vet ./...
go build ./cmd/weft
```

The CLI acceptance suite builds the real `weft` executable once and runs each
scenario in an isolated temporary directory. To run one scenario while
debugging, select its testscript subtest by filename, for example:

```sh
go test ./acceptance -run 'TestAcceptance/lifecycle'
```

The scenarios cover top-level and command help, root selection, init/sync/check,
state transitions, listing/search/recency, ordering, metadata, JSON records,
invalid boards, usage errors, and exit statuses. A harness command parses JSON
stdout, while another asserts the exact status for representative usage and
runtime failures. Each `exec weft` call starts the built executable as a child
process. The suite uses testscript's portable process and filesystem commands
and supports Linux, macOS, and Windows with Go installed.
The linked-worktree scenario skips when Git is not available on `PATH`.

GitHub Actions runs tests and builds on Linux, Windows, and macOS.
