package main

const defaultBoard = `# Columns run their steps top to bottom when a task enters them.
# Step types: shell, agent, human, goto, setup. See docs/phases for details.

setup:
  generate: |
    claude -p --allowedTools=Read,Glob,Grep "Look at this repository and write a bash script that installs its dependencies. The script is stored outside the repository and runs with a checkout of it as the current directory, so it must not change directory. Only install dependencies, do not run tests or builds. Print only the script, starting with #!/usr/bin/env bash." > "$KANBAN_SETUP"

suggest:
  to: todo
  command: |
    claude -p --allowedTools=Read,Glob,Grep "$(cat "$KANBAN_FAMILY_FILE")

    Suggest up to three next tasks that follow from the tasks above. Print only YAML: a list of items, each with a content field holding the task description in markdown." > "$KANBAN_SUGGESTIONS"

columns:
  - name: backlog
    steps: []

  - name: todo
    steps:
      - goto: next

  - name: planning
    harness: claude
    steps:
      - shell: test -d "$KANBAN_WORKTREE" || git -C "$KANBAN_REPO" worktree add -B "kanban/$KANBAN_TASK" "$KANBAN_WORKTREE"
      - setup: true
      - agent: Read the task in $KANBAN_TASK_FILE. Write a short implementation plan to $KANBAN_TASK_DIR/PLAN.md. Do not change files in the repository.
      - goto: next

  - name: plan-review
    steps:
      - human: Review PLAN.md, then approve to start implementation
      - goto: next

  - name: implementation
    harness: claude
    steps:
      - agent: Implement the plan in $KANBAN_TASK_DIR/PLAN.md. Run the tests. Commit the work on the current branch.
        resume: true
      - shell: git push -u origin HEAD && (gh pr view >/dev/null 2>&1 || gh pr create --fill)
      - goto: next

  - name: review
    harness: claude
    steps:
      - agent: Review the changes on this branch against the base branch without mercy. Write the findings to $KANBAN_TASK_DIR/REVIEW.md. Do not change code.
      - goto: next

  - name: merge
    steps:
      - human: Read REVIEW.md, decide, merge the pull request, then approve
      - shell: git -C "$KANBAN_REPO" worktree remove --force "$KANBAN_WORKTREE"
      - goto: next

  - name: done
    steps: []
`
