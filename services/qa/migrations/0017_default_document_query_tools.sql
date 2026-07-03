-- +goose Up
-- Add C-025 Document MCP query/read tools to the untouched system default.
-- User-created or explicitly customized QA configs remain authoritative.
UPDATE qa_config_versions
SET enabled_tool_names = enabled_tool_names || '["document__list_reports", "document__get_report", "document__list_materials", "document__get_material", "document__list_report_files", "document__read_report_file"]'::jsonb
WHERE version_no = 1
  AND created_by_user_id = 'system'
  AND enabled_tool_names @> '["document__generate_report_from_content", "document__generate_report_outline", "document__generate_report_text", "document__get_generation_status", "document__export_report_docx", "document__get_report_result"]'::jsonb
  AND NOT (enabled_tool_names @> '["document__list_reports"]'::jsonb);

-- +goose Down
UPDATE qa_config_versions
SET enabled_tool_names = (((((enabled_tool_names
  - 'document__list_reports')
  - 'document__get_report')
  - 'document__list_materials')
  - 'document__get_material')
  - 'document__list_report_files')
  - 'document__read_report_file'
WHERE version_no = 1
  AND created_by_user_id = 'system'
  AND enabled_tool_names @> '["document__list_reports"]'::jsonb;
