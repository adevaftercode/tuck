package cli

const rootHelp = `tuck — deterministic product-board operations

Usage:
  tuck [--root PATH] <command> [arguments]

Inspect:
  list [state]            List the board view or one complete state
  show <task>              Show canonical Markdown for a task
  find <query>             Search titles, task content, and metadata
  recent                   List recently completed tasks

Change:
  add <title>              Create a task (default state: backlog)
  start <task>             Move a task to doing
  move <task> <state>      Move a task to any state
  reopen <task>            Move a completed task to doing
  done <task>              Mark a task done
  rename <task> <title>    Change a task title
  reorder <task>           Reorder an active task with --first/--before/--after
  meta <operation>         Read or change user-defined metadata

Maintain:
  init                     Initialize a board [--json]
  sync                     Regenerate board.md [--json]
  check                    Validate board structure [--json]

Use "tuck <command> --help" for command options. Plain output is compact;
inspection and mutation commands support --json.
`

var commandHelp = map[string]string{
	"init": `Initialize an empty Tuck board.

Usage: tuck init [--root PATH] [--json]
Creates board.md and tasks/{backlog,todo,doing,done} without overwriting existing records.
`,
	"list": `List tasks in board order.

Usage: tuck list [state] [--limit N] [--json]
Without a state, show the compact current board projection. With a state, list
every task in that state unless --limit is supplied.
`,
	"show": `Show a task's canonical Markdown or a JSON record.

Usage: tuck show <number-or-id> [--json]
`,
	"find": `Find task candidates by literal, case-insensitive text.

Usage: tuck find <query> [--state STATE] [--recent] [--meta KEY=JSON] [--limit N] [--json]
Searches title, body, and user metadata. --meta filters by exact typed value;
repeat it to require several values. --recent limits candidates to done tasks.
`,
	"recent": `List completed tasks, newest first.

Usage: tuck recent [--limit N] [--json]
The default limit is 10.
`,
	"add": `Create a task with a stable number and ID.

Usage: tuck add <title> [--state backlog|todo|doing|done] [--json]
The default state is backlog. Add user metadata with "tuck meta set".
`,
	"start": `Move a task to doing.

Usage: tuck start <number-or-id> [--json]
`,
	"move": `Move a task to any state.

Usage: tuck move <number-or-id> <backlog|todo|doing|done> [--json]
`,
	"done": `Mark a task done.

Usage: tuck done <number-or-id> [--json]
`,
	"reopen": `Reopen a completed task in doing.

Usage: tuck reopen <number-or-id> [--json]
`,
	"rename": `Rename a task and its human-readable filename.

Usage: tuck rename <number-or-id> <new title> [--json]
`,
	"reorder": `Change order within backlog, todo, or doing.

Usage: tuck reorder <number-or-id> --first|--before <other>|--after <other> [--json]
Done tasks remain in completion order.
`,
	"meta": `Read or change flat user-defined task metadata.

Usage: tuck meta get <task> [key] [--json]
       tuck meta set <task> <key> <JSON-value> [--json]
       tuck meta unset <task> <key> [--json]
Values are strings, numbers, booleans, null, or flat arrays of those values.
Tuck's id, number, order, created_at, and completed_at fields are reserved.
`,
	"sync": `Regenerate the board.md projection from valid task records.

Usage: tuck sync [--json]
Malformed records produce warnings and leave board.md unchanged.
`,
	"check": `Validate task records and the current board.md projection.

Usage: tuck check [--json]
Does not modify board files.
`,
}
