package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sakayori-Iroha-168/Software_Teamwork/services/qa/internal/service/agent"
)

func TestGenerateResultSummaryPreservesSanitizedToolFailure(t *testing.T) {
	summary := GenerateResultSummary(ToolSearchKnowledge, `{"error":{"code":"retrieval_failed","message":"knowledge retrieval service failed"}}`)

	if summary["error"] != "retrieval_failed" || summary["message"] != "knowledge retrieval service failed" || summary["sanitized"] != true {
		t.Fatalf("summary=%#v", summary)
	}
	if strings.Contains(strings.TrimSpace(summary["message"].(string)), "internal") {
		t.Fatalf("summary leaked raw marker: %#v", summary)
	}
}

func TestGenerateResultSummaryRedactsExternalToolFailureMessage(t *testing.T) {
	summary := GenerateResultSummary("external.lookup", `{"error":{"code":"upstream_failed","message":"dial tcp internal.service.local with token secret"}}`)

	if summary["error"] != "upstream_failed" || summary["message"] != "tool execution failed" || summary["sanitized"] != true {
		t.Fatalf("summary=%#v", summary)
	}
	if strings.Contains(strings.TrimSpace(summary["message"].(string)), "internal") || strings.Contains(strings.TrimSpace(summary["message"].(string)), "secret") {
		t.Fatalf("summary leaked raw external failure: %#v", summary)
	}
}

func TestGenerateResultSummaryBuildsPendingReportArtifact(t *testing.T) {
	summary := GenerateResultSummary("document__generate_report_text", `{"status":"accepted","job":{"id":"job-1","reportId":"rpt-1","jobType":"content_generation","status":"running","progress":{"percent":42,"internalUrl":"http://internal/doc"}}}`)

	artifact := summary["reportArtifact"].(map[string]any)
	if artifact["artifactType"] != "report_generation" || artifact["jobId"] != "job-1" || artifact["jobStatus"] != "running" {
		t.Fatalf("artifact=%#v", artifact)
	}
	if _, ok := artifact["downloadPath"]; ok {
		t.Fatalf("pending artifact must not expose downloadPath: %#v", artifact)
	}
	preview := artifact["preview"].(map[string]any)
	if preview["progressPercent"] != 42 {
		t.Fatalf("preview=%#v", preview)
	}
	if strings.Contains(strings.ToLower(fmtSprint(summary)), "internalurl") || strings.Contains(strings.ToLower(fmtSprint(summary)), "http://internal") {
		t.Fatalf("summary leaked internal progress fields: %#v", summary)
	}
}

func TestGenerateResultSummaryBuildsContentReportArtifactWithoutLeakingContent(t *testing.T) {
	summary := GenerateResultSummary("document__generate_report_from_content", `{"status":"accepted","report":{"id":"rpt-1","name":"Attachment","reportType":"summer_peak_inspection","status":"draft"},"job":{"id":"job-1","reportId":"rpt-1","jobType":"outline_generation","status":"accepted"},"warnings":["content_truncated"],"content":"raw token secret http://internal/document"}`)

	artifact := summary["reportArtifact"].(map[string]any)
	if artifact["reportId"] != "rpt-1" || artifact["jobId"] != "job-1" || artifact["jobStatus"] != "accepted" {
		t.Fatalf("artifact=%#v", artifact)
	}
	if strings.Contains(strings.ToLower(fmtSprint(summary)), "raw token") || strings.Contains(strings.ToLower(fmtSprint(summary)), "http://internal") || strings.Contains(strings.ToLower(fmtSprint(summary)), "content_truncated") {
		t.Fatalf("summary leaked raw content or internal warning: %#v", summary)
	}
}

func TestGenerateResultSummaryBuildsSucceededExportArtifact(t *testing.T) {
	summary := GenerateResultSummary("document__export_report_docx", `{"status":"accepted","reportFile":{"id":"rf-1","reportId":"rpt-1","jobId":"job-2","filename":"report.docx","format":"docx","fileSize":4096,"status":"succeeded"}}`)

	artifact := summary["reportArtifact"].(map[string]any)
	if artifact["reportFileId"] != "rf-1" || artifact["fileStatus"] != "succeeded" || artifact["downloadPath"] != "/api/v1/report-files/rf-1/content" {
		t.Fatalf("artifact=%#v", artifact)
	}
	if artifact["detailPath"] != "/api/v1/reports/rpt-1" {
		t.Fatalf("detailPath=%#v", artifact["detailPath"])
	}
}

func TestGenerateResultSummaryBuildsSanitizedDocumentFailure(t *testing.T) {
	summary := GenerateResultSummary("document__get_report_result", `{"status":"failed","error":{"code":"forbidden","message":"token secret at http://internal/document"}}`)

	if summary["error"] != "policy_denied" || summary["message"] != "report access is not allowed" {
		t.Fatalf("summary=%#v", summary)
	}
	artifact := summary["reportArtifact"].(map[string]any)
	if artifact["jobStatus"] != "failed" {
		t.Fatalf("artifact=%#v", artifact)
	}
	if strings.Contains(strings.ToLower(fmtSprint(summary)), "token secret") || strings.Contains(strings.ToLower(fmtSprint(summary)), "http://internal") {
		t.Fatalf("summary leaked raw document failure: %#v", summary)
	}
}

func TestGenerateResultSummaryLeavesDocumentQueryToolsAsGenericSteps(t *testing.T) {
	summary := GenerateResultSummary("document__list_reports", `{"status":"succeeded","reports":[{"id":"rpt-1","name":"Inspection"}],"totalCount":1}`)

	if _, ok := summary["reportArtifact"]; ok {
		t.Fatalf("query tool summary should not create reportArtifact: %#v", summary)
	}
	if summary["tool"] != "document__list_reports" || summary["sanitized"] != true {
		t.Fatalf("summary=%#v", summary)
	}
}

func TestDefaultDocumentReportToolsArePolicyVisibleSubset(t *testing.T) {
	allDocumentTools := []string{
		ToolGenerateReportOutline,
		ToolGenerateReportFromContent,
		ToolRegenerateReportOutline,
		ToolGenerateReportText,
		ToolRegenerateReportText,
		ToolRegenerateReportSection,
		ToolGetGenerationStatus,
		ToolGetTemplateSchema,
		ToolExportReportDOCX,
		ToolGetReportResult,
		ToolListReports,
		ToolGetReport,
		ToolListMaterials,
		ToolGetMaterial,
		ToolListReportFiles,
		ToolReadReportFile,
	}
	definitions := make([]agent.ToolDefinition, 0, len(allDocumentTools))
	for _, name := range allDocumentTools {
		definitions = append(definitions, agent.ToolDefinition{
			Type: "function",
			Function: agent.FunctionTool{
				Name:       defaultDocumentToolAlias + "__" + name,
				Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
			},
		})
	}
	policy, err := NewPolicy(PolicyConfig{EnabledToolNames: DefaultDocumentReportToolNames})
	if err != nil {
		t.Fatal(err)
	}

	filtered := policy.FilterTools(definitions)
	if len(filtered) != len(DefaultDocumentReportToolNames) {
		t.Fatalf("filtered tool count=%d, want %d: %+v", len(filtered), len(DefaultDocumentReportToolNames), filtered)
	}
	visible := make(map[string]struct{}, len(filtered))
	for _, tool := range filtered {
		visible[tool.Function.Name] = struct{}{}
	}
	for _, name := range DefaultDocumentReportToolNames {
		if _, ok := visible[name]; !ok {
			t.Fatalf("default document tool %q was filtered out; visible=%v", name, visible)
		}
	}
	if _, ok := visible[defaultDocumentToolAlias+"__"+ToolGetTemplateSchema]; ok {
		t.Fatalf("template schema tool should not be visible in the default whitelist")
	}
	if _, ok := visible[defaultDocumentToolAlias+"__"+ToolListMaterials]; ok {
		t.Fatalf("material list tool should not be visible in the default whitelist")
	}
	if _, ok := visible[defaultDocumentToolAlias+"__"+ToolGetMaterial]; ok {
		t.Fatalf("material detail tool should not be visible in the default whitelist")
	}
}

func fmtSprint(value any) string {
	return fmt.Sprintf("%#v", value)
}
