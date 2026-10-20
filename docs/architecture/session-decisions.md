# Confirmed project decisions

Reviewed against repository code and configuration on **2026-10-20**. These are
architectural decisions and supported execution paths, not evidence of a live
production deployment.

## Identity and execution environment

- The project provides a persistent Identity that uses replaceable LLM API
  providers for reasoning. The execution environment provides persistent paths,
  tools, capabilities and authority checks. Keep the domain independent of
  Docker, LXC or VM deployment choices.
- PostgreSQL stores canonical messages, memories, runs and events. The in-memory
  store is a non-persistent development fallback, and the builtin model provider
  is a preview/test implementation.
- Models may propose CLI operations and code execution through the Agent Harness.
  The runtime exposes `environment_exec` only when both capability and authority
  permit `shell`; tool registration, caller permissions, workspace boundaries,
  timeouts and output limits still apply. SSO login alone does not grant access
  to the owner's filesystem, CLI or external credentials.
- Package installation additionally depends on the OS user and container/systemd
  restrictions. A package manager being installed does not grant permission to
  use it. The default Compose configuration grants read/write-file authority,
  not shell authority; the engine image runs as a non-root user.

## Skills and MCP

- Skills provide operational knowledge and help select between CLI, MCP and
  direct APIs. They do not grant capabilities or expand authority.
- MCP is an optional external-service connection mechanism. Administrators manage
  server inventory and allowed tools; model-visible tools are restricted by the
  intersection with separately granted authority. Skills and remote tool
  descriptions cannot change those grants.
- Preserve the distinction between the reasoning provider, the Harness that
  validates tool calls, and the environment that executes permitted operations.
  See [environment.md](environment.md) for the execution and authority contracts;
  source and deployment files determine which adapters are currently present.

## Frontend and engine separation

- `frontend/` is an independent Node project. `frontend/app/` contains the UI,
  and `frontend/worker/index.ts` provides a Cloudflare Worker entry point.
  The frontend also supports the Node/Vinext Docker execution path documented
  in [README.md](../../README.md).
- `runtime/` contains the Go API, Agent Harness and execution-environment code.
  It runs separately from the frontend. Local execution, a Docker image and an
  LXC/systemd deployment adapter are present. A VM may host the process; this
  does not imply that VM provisioning is implemented in the core.
- Cloudflare hosting and LXC/VM hosting are supported architectural directions,
  not claims about the currently deployed production topology. Do not move agent
  execution into the frontend Worker or make it an agent control plane by inference.
- The frontend Docker build context is `frontend/`. The engine Docker build context
  is the repository root so that it can include `runtime/`, `config/` and `skills/`.
  The root `compose.yaml` supplies a local development setup with PostgreSQL.
- Explain whether “runtime” means the frontend Worker/Node process, the Go agent
  engine, or the agent's execution environment when that distinction matters.

## Design and validation

- UI decisions and service-specific shared-design adoption are documented in
  [DESIGN.md](../../DESIGN.md). The common design submodule remains a read-only
  reference during ordinary service work. Frontend-local CSS snapshots preserve
  the independent frontend build context.
- The current Woodpecker pipeline intentionally checks only the frontend:
  frozen dependency installation, lint, type checking, tests and application build.
  Backend CI and container-image publication require a separate scope decision.
- Keep local tests, remote CI, image publication and production verification as
  separate evidence. [TASK.MD](../TASK.MD) tracks confirmed preparation work and
  items that still need checking.
