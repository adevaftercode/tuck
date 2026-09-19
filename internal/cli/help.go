package cli

const rootHelp = `weft — deterministic product-board operations

Usage:
  weft [--root PATH] <command> [arguments]

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
  sync                     Regenerate WEFT.md [--json]
  check                    Validate board structure [--json]

Use "weft <command> --help" for command options. Plain output is compact;
inspection and mutation commands support --json.
`

var commandHelp = map[string]string{
	"init": `Initialize an empty Weft board.

Usage: weft init [--root PATH] [--json]
Creates WEFT.md and tasks/{backlog,todo,doing,done} without overwriting existing records.
`,
	"list": `List tasks in board order.

Usage: weft list [state] [--limit N] [--json]
Without a state, show the compact current board projection. With a state, list
every task in that state unless --limit is supplied.
`,
	"show": `Show a task's canonical Markdown or a JSON record.

Usage: weft show <number-or-id> [--json]
`,
	"find": `Find task candidates by literal, case-insensitive text.

Usage: weft find <query> [--state STATE] [--recent] [--meta KEY=JSON] [--limit N] [--json]
Searches title, body, and user metadata. --meta filters by exact typed value;
repeat it to require several values. --recent limits candidates to done tasks.
`,
	"recent": `List completed tasks, newest first.

Usage: weft recent [--limit N] [--json]
The default limit is 10.
`,
	"add": `Create a task with a stable number and ID.

Usage: weft add <title> [--state backlog|todo|doing|done] [--json]
The default state is backlog. Add user metadata with "weft meta set".
`,
	"start": `Move a task to doing.

Usage: weft start <number-or-id> [--json]
`,
	"move": `Move a task to any state.

Usage: weft move <number-or-id> <backlog|todo|doing|done> [--json]
`,
	"done": `Mark a task done.

Usage: weft done <number-or-id> [--json]
`,
	"reopen": `Reopen a completed task in doing.

Usage: weft reopen <number-or-id> [--json]
`,
	"rename": `Rename a task and its human-readable filename.

Usage: weft rename <number-or-id> <new title> [--json]
`,
	"reorder": `Change order within backlog, todo, or doing.

Usage: weft reorder <number-or-id> --first|--before <other>|--after <other> [--json]
Done tasks remain in completion order.
`,
	"meta": `Read or change flat user-defined task metadata.

Usage: weft meta get <task> [key] [--json]
       weft meta set <task> <key> <JSON-value> [--json]
       weft meta unset <task> <key> [--json]
Values are strings, numbers, booleans, null, or flat arrays of those values.
Weft's id, number, order, created_at, and completed_at fields are reserved.
`,
	"sync": `Regenerate the WEFT.md projection from valid task records.

Usage: weft sync [--json]
Malformed records produce warnings and leave WEFT.md unchanged.
`,
	"check": `Validate task records and the current WEFT.md projection.

Usage: weft check [--json]
Does not modify board files.
`,
}
