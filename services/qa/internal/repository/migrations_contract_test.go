package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationsEnableDocumentReportToolsForUntouchedSystemDefault(t *testing.T) {
	matches, err := filepath.Glob("../../migrations/*document*tool*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("missing migration for enabling document report tools in untouched system default QA config")
	}
	var content string
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content += "\n" + string(data)
	}
	required := []string{
		"-- +goose Up",
		"UPDATE qa_config_versions",
		"created_by_user_id = 'system'",
		"version_no = 1",
		`enabled_tool_names = '["search_knowledge", "search_session_attachments"]'::jsonb`,
		"document__generate_report_from_content",
		"document__generate_report_outline",
		"document__generate_report_text",
		"document__get_generation_status",
		"document__export_report_docx",
		"document__get_report_result",
		"document__list_reports",
		"document__get_report",
		"document__list_report_files",
		"document__read_report_file",
	}
	for _, token := range required {
		if !strings.Contains(content, token) {
			t.Fatalf("document report tool migration missing %q\n%s", token, content)
		}
	}
	if strings.Contains(content, "created_by_user_id <>") || strings.Contains(content, "created_by_user_id !=") {
		t.Fatalf("document report tool migration must not update user-created configs:\n%s", content)
	}
	forbidden := []string{
		`enabled_tool_names || '["document__list_reports", "document__get_report", "document__list_materials"`,
		`enabled_tool_names || '["document__list_materials"`,
		`- 'document__list_materials'`,
		`- 'document__get_material'`,
	}
	for _, token := range forbidden {
		if strings.Contains(content, token) {
			t.Fatalf("material metadata tools must stay out of default migration; found %q\n%s", token, content)
		}
	}
}
