package main

const defaultBoard = `# Columns run their steps top to bottom when a task enters them.
# Step types: shell, agent, human, goto, setup. See docs/phases for details.

# A personality is chosen per task: harness, model and a prompt put in front of each agent step.
personalities:
  - name: Claude
    harness: claude
    model: opus
  - name: Codex
    harness: codex

setup:
  generate: |
    claude -p --allowedTools=Read,Glob,Grep "Look at this repository and write a bash script that installs its dependencies. The script is stored outside the repository and runs with a checkout of it as the current directory, so it must not change directory. Only install dependencies, do not run tests or builds. Print only the script, starting with #!/usr/bin/env bash." > "$KK_SETUP"

suggest:
  to: Todo
  command: |
    claude -p --allowedTools=Read,Glob,Grep "$(cat "$KK_FAMILY_FILE")

    Suggest up to three next tasks that follow from the tasks above. Print only YAML: a list of items, each with a content field holding the task description in markdown." > "$KK_SUGGESTIONS"

columns:
  - name: Backlog
    steps: []

  - name: Todo
    steps:
      - goto: next

  - name: Planning
    harness: claude
    steps:
      - shell: test -d "$KK_WORKTREE" || git -C "$KK_REPO" worktree add -B "kk/$KK_TASK" "$KK_WORKTREE"
      - setup: true
      - agent: Read the task in $KK_TASK_FILE. Write a short implementation plan to $KK_TASK_DIR/PLAN.md. Do not change files in the repository.
      - goto: next

  - name: Plan review
    steps:
      - human: Review PLAN.md, then approve to start implementation
      - goto: next

  - name: Implementation
    harness: claude
    steps:
      - agent: Implement the plan in $KK_TASK_DIR/PLAN.md. Run the tests. Commit the work on the current branch.
        resume: true
      - shell: git push -u origin HEAD && (gh pr view >/dev/null 2>&1 || gh pr create --fill)
      - goto: next

  - name: Review
    harness: claude
    steps:
      - agent: Review the changes on this branch against the base branch without mercy. Write the findings to $KK_TASK_DIR/REVIEW.md. Do not change code.
      - goto: next

  - name: Merge
    steps:
      - human: Read REVIEW.md, decide, merge the pull request, then approve
      - shell: git -C "$KK_REPO" worktree remove --force "$KK_WORKTREE"
      - goto: next

  - name: Done
    steps: []
`
