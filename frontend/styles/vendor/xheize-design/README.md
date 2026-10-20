# Shared CSS snapshot

`tokens.css` is an unmodified copy of `references/xheize-design/styles/generated/tokens.css`.
`components.css` is an unmodified copy of `references/xheize-design/styles/components.css`.
The parent repository's submodule gitlink is the version authority. These copies
allow the existing frontend-only Docker/CI build context to remain self-contained.

Do not edit these files to customize the service. Use `frontend/styles/theme.css`
and the product rules in `DESIGN.md`. A separately requested shared-version update
must review the reference changes, copy both CSS files again, then run service
validation. Updating the submodule alone does not update these snapshots.
