---
name: workspace-summary
description: Summarize a user-selected workspace text file with evidence and explicit uncertainty.
---

# Workspace summary

Use this skill only when the user asks to summarize or inspect a text file in the persistent workspace.

1. Ask for a workspace-relative path only if the user did not provide one.
2. Call `workspace_read_file` for that exact path. Do not guess alternate files.
3. Separate facts present in the file from your interpretation.
4. Mention when the file was truncated or when important context appears missing.
5. Do not claim access to files, shell commands, networks, or services that the harness did not expose.
