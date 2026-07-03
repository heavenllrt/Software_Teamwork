# Document Service

`document` owns report types, templates, materials, reports, outlines, sections,
report jobs, job attempts, events, generated file metadata, statistics, and
operation logs.

The current implementation provides the service/data baseline, implemented
report type/template/material/report/outline/section APIs, the report
job/attempt/event state machine, report file creation, report settings, report
statistics, and operation logs. DOCX export currently uses the in-process Go
`SimpleDOCXGenerator`. Basic AI outline and section-content orchestration is
implemented for the fixed `summer_peak_inspection` and `coal_inventory_audit`
report types through AI Gateway chat calls, with optional Knowledge retrieval
context when configured and requested. A stateless Streamable HTTP Document MCP
server is implemented at `/mcp` for safe report generation, status, template
schema, result, and basic DOCX export tool calls. QA discovery and an env-gated
cross-service smoke are implemented; the Pandoc/LibreOffice rich DOCX
conversion toolchain remains future work.

## Local Configuration

Required environment variables:

| Variable | Example | Purpose |
| --- | --- | --- |
| `DOCUMENT_DATABASE_URL` | `postgres://document_app:document_app_dev@localhost:5432/document_system?sslmode=disable` | PostgreSQL connection string. |
| `DOCUMENT_REDIS_ADDR` | `localhost:6379` | Redis/asynq queue endpoint. Redis is not the durable job state authority. |
| `DOCUMENT_FILE_SERVICE_URL` | `http://localhost:8082` | Internal file service base URL for later template/material/report-file bytes. |
| `DOCUMENT_FILE_SERVICE_TOKEN` | empty | Service token sent to File Service when request context has no `X-Service-Token`. Falls back to `INTERNAL_SERVICE_TOKEN` when empty. |
| `DOCUMENT_AI_GATEWAY_URL` | `http://localhost:8086` | Internal AI Gateway base URL for report settings validation and report generation chat calls. Must target `localhost`, loopback, or `ai-gateway` on port `8086`. |
| `DOCUMENT_AI_GATEWAY_PROFILE_ID` | `default-chat` | AI Gateway chat profile reference used by report settings/default generation. |

Optional variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `DOCUMENT_HTTP_ADDR` | `:8085` | HTTP listen address. |
| `DOCUMENT_AI_GATEWAY_SERVICE_TOKEN` | empty | Service token sent to AI Gateway profile validation APIs. Falls back to `INTERNAL_SERVICE_TOKEN` when empty. |
| `DOCUMENT_KNOWLEDGE_SERVICE_URL` | empty | Optional internal Knowledge service base URL. When empty, report generation skips Knowledge retrieval and uses only report/template/request context. |
| `DOCUMENT_KNOWLEDGE_SERVICE_TOKEN` | empty | Optional service token sent to Knowledge. Falls back to `INTERNAL_SERVICE_TOKEN` when empty. Required when `DOCUMENT_KNOWLEDGE_SERVICE_URL` is set. |
| `INTERNAL_SERVICE_TOKEN` | empty | Shared internal service token fallback for local/dev deployments. |
| `DOCUMENT_PANDOC_PATH` | `pandoc` | Reserved path for a future Pandoc-backed rich DOCX worker. The current host-run baseline does not require this CLI. |
| `DOCUMENT_LIBREOFFICE_PATH` | `soffice` | Reserved path for a future LibreOffice-backed conversion worker. The current host-run baseline does not require this CLI. |
| `DOCUMENT_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout. |
| `DOCUMENT_MCP_PATH` | `/mcp` | Streamable HTTP MCP endpoint path. |
| `DOCUMENT_MCP_SERVICE_TOKEN` | `INTERNAL_SERVICE_TOKEN` fallback | Required MCP service credential. |
| `DOCUMENT_MCP_TOKEN_HEADER` | `Authorization` | Credential header; Authorization accepts Bearer form. |

## Run

For normal local development, start repository infra, migrations, seed, and all
host-run backend services from the repository root:

```bash
cp deploy/.env.example deploy/.env
./scripts/local/dev-up.sh
./scripts/local/run-backend.sh
```

For Document-only code changes, run service checks from `services/document`.
The root `deploy/.env` values are the default local configuration source.

Operational routes:

```text
GET /healthz
GET /readyz
```

Both JSON responses use the project envelope: `{ "data": ..., "requestId": "..." }`.
The service-local operational contract is documented in [`api/openapi.yaml`](api/openapi.yaml).

## Active Report Route Coverage

Gateway exposes these document-owned report routes under `/api/v1`. The service
local paths below omit that prefix. Implemented routes call the document service
layer. Job routes persist state and drive the worker state machine; file export
jobs currently produce basic DOCX packages through the in-process Go generator.
Generation jobs for `summer_peak_inspection` and `coal_inventory_audit` call AI
Gateway for outline and section content and persist the generated outline,
sections, section versions, progress, and events. The richer Pandoc/LibreOffice
toolchain remains a future host-run worker dependency and is not required by
the current service.

| Method | Local path | Operation ID | Status |
| --- | --- | --- | --- |
| `GET` | `/report-types` | `listReportTypes` | Implemented |
| `GET` | `/report-templates` | `listReportTemplates` | Implemented |
| `POST` | `/report-templates` | `createReportTemplate` | Implemented |
| `GET` | `/report-templates/{reportTemplateId}` | `getReportTemplate` | Implemented |
| `PATCH` | `/report-templates/{reportTemplateId}` | `updateReportTemplate` | Implemented |
| `DELETE` | `/report-templates/{reportTemplateId}` | `deleteReportTemplate` | Implemented |
| `GET` | `/report-templates/{reportTemplateId}/structure` | `getReportTemplateStructure` | Implemented |
| `PATCH` | `/report-templates/{reportTemplateId}/structure` | `updateReportTemplateStructure` | Implemented |
| `GET` | `/report-materials` | `listReportMaterials` | Implemented |
| `POST` | `/report-materials` | `createReportMaterial` | Implemented |
| `GET` | `/report-materials/{materialId}` | `getReportMaterial` | Implemented |
| `DELETE` | `/report-materials/{materialId}` | `deleteReportMaterial` | Implemented |
| `GET` | `/reports` | `listReports` | Implemented |
| `POST` | `/reports` | `createReport` | Implemented |
| `GET` | `/reports/{reportId}` | `getReport` | Implemented |
| `PATCH` | `/reports/{reportId}` | `updateReport` | Implemented |
| `DELETE` | `/reports/{reportId}` | `deleteReport` | Implemented |
| `GET` | `/reports/{reportId}/outlines` | `listReportOutlines` | Implemented |
| `POST` | `/reports/{reportId}/outlines` | `createReportOutline` | Implemented |
| `GET` | `/reports/{reportId}/outlines/{outlineId}` | `getReportOutline` | Implemented |
| `PATCH` | `/reports/{reportId}/outlines/{outlineId}` | `updateReportOutline` | Implemented |
| `DELETE` | `/reports/{reportId}/outlines/{outlineId}/sections/{sectionId}` | `deleteReportOutlineSection` | Implemented |
| `GET` | `/reports/{reportId}/sections` | `listReportSections` | Implemented |
| `POST` | `/reports/{reportId}/sections` | `createReportSection` | Implemented; single create or batch save |
| `GET` | `/reports/{reportId}/sections/{sectionId}` | `getReportSection` | Implemented |
| `PATCH` | `/reports/{reportId}/sections/{sectionId}` | `updateReportSection` | Implemented |
| `GET` | `/reports/{reportId}/sections/{sectionId}/versions` | `listReportSectionVersions` | Implemented |
| `POST` | `/reports/{reportId}/sections/{sectionId}/versions` | `createReportSectionVersion` | Implemented |
| `GET` | `/reports/{reportId}/jobs` | `listReportJobs` | Implemented |
| `POST` | `/reports/{reportId}/jobs` | `createReportJob` | Implemented; enqueues AI outline/content or file worker task |
| `GET` | `/report-jobs/{jobId}` | `getReportJob` | Implemented |
| `GET` | `/report-jobs/{jobId}/attempts` | `listReportJobAttempts` | Implemented |
| `POST` | `/report-jobs/{jobId}/attempts` | `createReportJobAttempt` | Implemented; retry claim/enqueue |
| `GET` | `/reports/{reportId}/events` | `listReportEvents` | Implemented |
| `GET` | `/report-files` | `listReportFiles` | Implemented |
| `POST` | `/report-files` | `createReportFile` | Implemented; enqueues basic DOCX export |
| `GET` | `/report-files/{reportFileId}` | `getReportFile` | Implemented |
| `GET` | `/report-files/{reportFileId}/content` | `getReportFileContent` | Implemented; requires succeeded file content |
| `GET` | `/report-statistics/overview` | `getReportStatisticsOverview` | Implemented |
| `GET` | `/report-statistics/daily` | `listDailyReportStatistics` | Implemented |
| `GET` | `/report-operation-logs` | `listReportOperationLogs` | Implemented |
| `GET` | `/report-settings` | `getReportSettings` | Implemented |
| `PATCH` | `/report-settings` | `updateReportSettings` | Implemented; admin/super_admin only |

## MCP Tool Adapter

`internal/service/mcp_tools.go` exposes a Document-owned service-layer tool
adapter with these stable tool names:

- `generate_report_outline`
- `regenerate_report_outline`
- `generate_report_text`
- `regenerate_report_text`
- `regenerate_report_section`
- `get_generation_status`
- `get_template_schema`
- `export_report_docx`
- `get_report_result`
- `list_reports`
- `get_report`
- `list_report_files`
- `read_report_file`

The adapter accepts a trusted `RequestContext`, validates JSON-object
arguments, calls existing Document services, returns only safe summaries and
business IDs, and records operation logs with `requestSource=mcp` and
`toolName=<tool>`. It does not directly access repositories, File object keys,
MinIO, Qdrant, or model providers.

`export_report_docx` uses the current basic DOCX report-file path. It must not
be treated as Pandoc/LibreOffice rich DOCX support. Exact schemas, runtime
registration, result fields and the QA Agent workflow are documented in
[`../../docs/services/document/docs/mcp-tools.md`](../../docs/services/document/docs/mcp-tools.md).

The C-025 query/read tools (`list_reports`, `get_report`,
`list_report_files`, and `read_report_file`) are read-only metadata/content
tools. `read_report_file` returns text or markdown only, extracts DOCX text
from `word/document.xml`, caps returned content at 1 MiB, and never returns raw
DOCX bytes, File Service internal references, object keys, storage URLs, or
service tokens.

Material metadata query tools are not registered by Document MCP until their
cross-user permission model is confirmed.

## Migrations

Migration files live in `migrations/` and are applied with the project-pinned `goose@v3.27.1` command.

```powershell
go run github.com/pressly/goose/v3/cmd/goose@v3.27.1 -dir migrations postgres "$env:DOCUMENT_DATABASE_URL" up
```

The first migration creates the report generation tables and seeds the initial
report types:

- `summer_peak_inspection`
- `coal_inventory_audit`

`report_jobs`, `report_job_attempts`, and `report_events` are PostgreSQL
business-state tables. Redis/asynq should only carry queue payloads and task
execution coordination.

The second migration creates the singleton report settings row and adds indexes
used by active report, statistics, job status, and operation-log queries.

The third migration seeds the first-slice report defaults. It inserts the two
report types if they are missing, adds enabled placeholder template metadata for
local development, and fills missing `report_settings` keys for:

- `defaultTemplates.summer_peak_inspection`
- `defaultTemplates.coal_inventory_audit`
- `file.defaultFormat=docx`
- `file.defaultNumberingMode=global`
- `file.defaultStyleProfileId=first-slice-default-docx`

The placeholder templates intentionally have no stored file reference. Their
description and structure/style JSON mark the formal DOCX template dependency as
`needs_decision` and point back to
`services/document/migrations/0003_seed_initial_report_defaults.sql` as the
runnable import path. Re-running the seed keeps existing report type rows,
template rows, and user-provided settings values. The default settings do not
store provider API keys, provider URLs, object storage details, or internal
file references.

## SQLC

SQL queries live under `internal/repository/queries/`, and generated code lives
under `internal/repository/sqlc/`.

```bash
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
```

## Tests

```powershell
go test ./...
go build ./cmd/server
```

Repository integration tests are skipped unless `DOCUMENT_TEST_DATABASE_URL` is
set:

```powershell
$env:DOCUMENT_TEST_DATABASE_URL = "postgres://document:document@localhost:5432/document_test?sslmode=disable"
go test ./internal/repository
```
