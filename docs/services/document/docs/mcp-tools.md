# Document MCP 工具契约与 Agent 工作流

本文按当前 `develop` 源码记录 Document MCP 的运行契约。工具定义的权威实现是
`services/document/internal/service/mcp_tools.go`，远程 server 实现在
`services/document/internal/platform/mcpserver/server.go`，QA 侧发现与前缀转换实现在
`services/qa/internal/app/manager.go` 和 `internal/platform/mcpclient/`。

## 1. 服务边界与当前状态

Document MCP 负责“报告资源上的生成、重生成、状态查询、模板结构查询与 DOCX 导出”，
不负责 Knowledge RAG、会话附件解析或创建 QA 会话：

| 能力 | 当前归属 | Agent 可见工具 |
| --- | --- | --- |
| 知识库 RAG | QA 调用 Knowledge retrieval client | `search_knowledge`，不是 Document MCP 工具 |
| 会话附件检索 | QA session attachment tool | `search_session_attachments`，不是 Document MCP 工具 |
| 报告生成与导出 | Document Streamable HTTP MCP | 注册 alias 后为 `document__<tool>` |

当前 `develop` 已提供 `/mcp`、服务 token 校验、`tools/list`、`tools/call` 和下述 9 个
工具。Issue #510 的 `generate_report_from_content` 尚未进入当前 `develop`，因此本次只做
接入与发现，不复制其业务实现。

当前 server 已返回 `structuredContent`，但工具定义尚未声明 MCP `outputSchema` 和
`readOnlyHint`/`destructiveHint`/`idempotentHint` 等 annotations；这些属于后续协议质量
增强，不能让客户端据此承担权限判断。查询工具不返回集合，因此当前没有分页参数。

## 2. 传输、认证与注册

Document 与 QA 都在宿主机运行；根级 Docker Compose 只启动基础设施。

| 项目 | 本地值 | 说明 |
| --- | --- | --- |
| transport | `streamable_http` | stateless、JSON response |
| endpoint | `http://localhost:8085/mcp` | Document 进程上的 `DOCUMENT_MCP_PATH` |
| server name | `document-mcp` | MCP implementation name |
| QA alias | `document` | Agent 工具名前缀为 `document__` |
| token header | `Authorization` | QA 自动发送 `Bearer <token>` |
| token | `MCP_SERVER_TOKEN` | 必须与 Document 的 `DOCUMENT_MCP_SERVICE_TOKEN` 或其 `INTERNAL_SERVICE_TOKEN` fallback 一致 |

`deploy/seeds/003-qa-document-mcp.sql` 只向 QA 的 `mcp_servers` 写入非敏感元数据，
不把 token 写进 PostgreSQL。QA 加载运行时配置时按 alias 合并：

1. 数据库同名记录被禁用时保持禁用，不由环境变量重新启用。
2. 数据库同名记录有加密 token 时使用数据库 token。
3. 数据库同名记录没有 token 时使用 `MCP_SERVER_TOKEN`。
4. 数据库没有同名 alias 时追加环境 bootstrap server。

每次 MCP 请求还会从 QA Agent 上下文透传 `X-Caller-Service: qa`、`X-Request-Id`、
`X-User-Id`、`X-User-Roles`、`X-User-Permissions`。Document 用这些字段做权限、审计和
request correlation；客户端不能用服务 token 冒充任意用户权限。

## 3. 当前 9 个工具与参数

所有 input schema 都是 JSON object，`additionalProperties=false`。字段名以 camelCase
为公开契约；实现为兼容旧调用可识别的 snake_case alias 不应被新 Agent 生成。

### 3.1 生成类工具

以下 4 个工具共用同一个 schema：

| 工具 | 创建的 jobType | 用途 |
| --- | --- | --- |
| `generate_report_outline` | `outline_generation` | 生成报告大纲 |
| `regenerate_report_outline` | `outline_regeneration` | 重新生成报告大纲 |
| `generate_report_text` | `content_generation` | 按大纲生成正文 |
| `regenerate_report_text` | `content_regeneration` | 重新生成正文 |

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `reportId` | string | 是 | 已存在且当前用户可访问的报告业务 ID |
| `requirements` | string | 否 | 本轮生成要求；日志只记录长度摘要，不记录全文 |
| `materialIds` | string[] | 否 | Document 报告素材业务 ID |
| `options` | object | 否 | 生成选项，允许扩展字段；由 Document job/worker 解释 |
| `retrieval` | object | 否 | 检索上下文或检索提示，允许扩展字段；Document 仍通过 Knowledge 服务检索，不直连 Knowledge runtime/doc engine 或 Qdrant |

典型调用：

```json
{
  "reportId": "22222222-2222-4222-8222-222222222301",
  "requirements": "生成面向管理层的精简检查报告",
  "materialIds": ["22222222-2222-4222-8222-222222222201"],
  "options": {"knowledgeBaseIds": ["kb_local_demo"]},
  "retrieval": {"query": "迎峰度夏风险与整改情况"}
}
```

`regenerate_report_section` 使用相同字段，另有必填 `sectionId: string`，创建
`section_regeneration` job。

### 3.2 查询与导出工具

| 工具 | 参数 | 必填 | 行为 |
| --- | --- | --- | --- |
| `get_generation_status` | `jobId: string` | 是 | 查询异步 job 状态、进度与安全错误码 |
| `get_template_schema` | `templateId: string` | 是 | 返回模板 `outlineSchema` 和 `styleConfig` |
| `get_report_result` | `reportId: string` | 是 | 返回报告及最新 job/file 的安全摘要 |

`export_report_docx` 参数：

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `reportId` | string | 是 | 要导出的报告业务 ID |
| `templateId` | string | 否 | 可选模板业务 ID |
| `format` | string | 否 | 只能是 `docx`，默认 `docx` |
| `exportOptions` | object | 否 | 样式/导出扩展选项 |

该工具创建异步 report-file job。当前走内置 `SimpleDOCXGenerator`，不表示
Pandoc/LibreOffice 富 DOCX 已上线。

## 4. 返回结构

所有工具同时返回 text JSON 与 MCP `structuredContent`。共同结构为：

```json
{
  "requestId": "req_...",
  "toolName": "generate_report_outline",
  "status": "accepted | succeeded | failed",
  "job": {},
  "report": {},
  "reportFile": {},
  "templateSchema": {},
  "error": {"code": "...", "message": "...", "fields": {}},
  "warnings": []
}
```

可选摘要字段：

| 对象 | 关键字段 |
| --- | --- |
| `job` | `id`、`reportId`、`jobType`、`targetType`、`targetId`、`status`、`progress`、`errorCode`、`createdAt` |
| `report` | `id`、`name`、`reportType`、`templateId`、`status`、`latestJobId`、`latestReportFileId`、时间戳 |
| `reportFile` | `id`、`reportId`、`jobId`、`filename`、`format`、`fileSize`、`status`、`contentPath`、`createdAt` |
| `templateSchema` | `templateId`、`outlineSchema`、`styleConfig` |

生成/导出提交成功通常返回 `status=accepted`；查询成功返回 `status=succeeded`；业务
校验、权限或依赖失败返回 `status=failed` 且 MCP result 的 `isError=true`。Document
不会在结果中返回 prompt、provider 原始错误、token、MinIO object key 或内部服务 URL。

## 5. Agent 调用工作流

```text
dev-up seed + deploy/.env
        |
        v
QA ConfigService 按 alias 合并数据库元数据与环境 token
        |
        v
QA Manager 连接 /mcp -> tools/list -> 加 document__ 前缀
        |
        v
QA tool policy 按 active AgentConfig 白名单筛选
        |
        v
AI Gateway 模型选择工具并生成 JSON 参数
        |
        v
QA 校验 schema + 单次 timeout，去掉前缀后 tools/call
        |
        v
Document 校验 token/用户权限 -> 复用 service layer -> 写审计/创建异步 job
        |
        v
QA 将安全摘要作为 role=tool 消息交回模型并写入脱敏 tool-call/SSE 结果
```

报告生成的典型多轮流程：

1. Agent 已有 `reportId` 时，调用 `document__generate_report_outline` 或
   `document__generate_report_text`，把用户要求放入 `requirements`，材料放入
   `materialIds`，RAG 上下文放入 `retrieval`/`options`。
2. Document 创建 PostgreSQL `ReportJob` 并投递 Redis/asynq，立即返回 `accepted`，
   不在 MCP HTTP 请求里等待模型生成完成。
3. Agent 需要确认进度时调用 `document__get_generation_status`。轮询次数由
   `AGENT_MAX_ITERATIONS` 限制，单次调用由 MCP/tool timeout 限制。
4. 正文完成后调用 `document__export_report_docx`；文件成功前不能向用户暴露下载路径。
5. 调用 `document__get_report_result` 确认最新报告文件；QA 只把成功文件映射为 Gateway
   `/api/v1/report-files/{reportFileId}/content` 下载路径。

当前默认 Agent 白名单只包含 5 个常用工具：outline、text、status、export、result。
其余 4 个工具虽然能被 `tools/list` 发现，但必须在 active QA config version 的
`enabledToolNames` 中显式加入对应 `document__...` 名称，模型才可见。

## 6. Issue #510 依赖：从内容直接生成

Issue #511 期望调用的 `generate_report_from_content` 由 Issue #510 实现。合并 #510 后，
预期工具参数为：

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `content` | string | 是 | 生成报告的原始内容 |
| `document_name` | string | 否 | 报告名称提示 |
| `instructions` | string | 否 | 生成要求 |

在 #510 合并前，QA 不得把 `document__generate_report_from_content` 宣称为已发现或可调用；
本次接入以现有 9 个工具和 env-gated Document smoke 为验收基线。

## 7. 本地验证

```bash
cp deploy/.env.example deploy/.env
./scripts/local/dev-up.sh
./scripts/local/run-backend.sh

cd services/qa
QA_DOCUMENT_MCP_SMOKE=1 \
go test ./internal/platform/mcpclient -run '^TestDocumentMCPReportToolsSmoke$' -count=1 -v
```

`dev-up.sh` 会插入 `mcp_servers.alias=document`；QA 启动后完成连接与 `tools/list`，并更新
该行的 `tool_count`、`last_connected_at` 或安全错误摘要。失败时用同一
`X-Request-Id` 对照 `.local/logs/qa.log` 与 `.local/logs/document.log`，不要把 token 或
完整工具参数写入 issue/日志。

## C-025 Current Document MCP Query And Read Tools

This section records the current contract added by C-025. The implementation
authority is `services/document/internal/service/mcp_tools.go`; QA exposes these
tools through the `document__` alias prefix.

### Implemented P1 Tools

| Model-facing name | Service tool name | Purpose |
| --- | --- | --- |
| `document__list_reports` | `list_reports` | Query reports with optional `reportType` and `status` filters. |
| `document__get_report` | `get_report` | Read detailed safe report metadata by `reportId`. |
| `document__list_materials` | `list_materials` | Query report materials with optional `category` filter. |
| `document__get_material` | `get_material` | Read safe material metadata by `materialId`. |
| `document__list_report_files` | `list_report_files` | List generated report files for a `reportId`. |
| `document__read_report_file` | `read_report_file` | Read a generated report file as bounded `text` or `markdown`. |

`list_reports` input:

```json
{
  "reportType": "summer_peak_inspection",
  "status": "generated",
  "page": 1,
  "pageSize": 20
}
```

Response fields include `reports`, `totalCount`, `page`, and `pageSize`.
Each report summary exposes business metadata only: `id`, `name`,
`reportType`, `templateId`, `topic`, `status`, latest job/file IDs, and safe
timestamps.

`get_report` input:

```json
{ "reportId": "report-id" }
```

`list_materials` input:

```json
{ "category": "inspection", "page": 1, "pageSize": 20 }
```

`get_material` input:

```json
{ "materialId": "material-id" }
```

Material results include `id`, `materialName`, `materialType`, `category`,
`filename`, `fileSize`, `description`, `tags`, `enabled`, and safe timestamps.
They must not expose `fileRef`, object keys, storage URLs, buckets, or internal
File Service IDs.

`list_report_files` input:

```json
{ "reportId": "report-id" }
```

Results include `reportFiles` with `id`, `reportId`, `jobId`, `filename`,
`format`, `fileSize`, `status`, `contentPath`, and `createdAt`.

`read_report_file` input:

```json
{ "reportFileId": "report-file-id", "format": "text" }
```

`format` may be `text` or `markdown`; default is `text`. The tool returns
`reportFileContent` with `reportFileId`, `filename`, `content`, `format`,
`fileSize`, and `truncated`. The content is capped at 1 MiB. DOCX bytes are
never returned directly; the service extracts text from `word/document.xml`.
Unsupported binary content returns a stable tool error instead of binary data.

### Safety And QA Behavior

- Query/read tools reuse the Document service layer and its permission checks.
- Tool results are sanitized and do not expose service tokens, provider errors,
  prompts, `fileRef`, object keys, storage URLs, buckets, or internal URLs.
- QA enables the report and report-file query tools in the default tool
  whitelist. Material metadata tools (`document__list_materials` and
  `document__get_material`) remain opt-in until their cross-user permission
  model is confirmed.
- Query/read tools are not report artifact tools. QA should summarize them as
  generic tool steps and must not create `reportArtifact` from
  `document__list_*`, `document__get_material`, or `document__read_report_file`
  results.

### P2 Candidates, Documented Only

These tools are intentionally not implemented by C-025:

| Candidate tool | Purpose |
| --- | --- |
| `document__list_templates` | List report templates with optional type filter. |
| `document__retry_job` | Retry a failed report generation job. |
| `document__list_job_events` | List events for a report generation job. |
| `document__retrieve_knowledge` | Retrieve report-oriented Knowledge snippets through Document retrieval settings. |
