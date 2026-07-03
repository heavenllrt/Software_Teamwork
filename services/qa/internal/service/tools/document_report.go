package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const (
	ToolGenerateReportOutline     = "generate_report_outline"
	ToolGenerateReportFromContent = "generate_report_from_content"
	ToolGenerateReportText        = "generate_report_text"
	ToolGetGenerationStatus       = "get_generation_status"
	ToolExportReportDOCX          = "export_report_docx"
	ToolGetReportResult           = "get_report_result"
	ToolRegenerateReportOutline   = "regenerate_report_outline"
	ToolRegenerateReportText      = "regenerate_report_text"
	ToolRegenerateReportSection   = "regenerate_report_section"
	ToolGetTemplateSchema         = "get_template_schema"
	ToolListReports               = "list_reports"
	ToolGetReport                 = "get_report"
	ToolListMaterials             = "list_materials"
	ToolGetMaterial               = "get_material"
	ToolListReportFiles           = "list_report_files"
	ToolReadReportFile            = "read_report_file"

	defaultDocumentToolAlias = "document"
)

var documentReportToolNames = map[string]struct{}{
	ToolGenerateReportOutline:     {},
	ToolGenerateReportFromContent: {},
	ToolGenerateReportText:        {},
	ToolGetGenerationStatus:       {},
	ToolExportReportDOCX:          {},
	ToolGetReportResult:           {},
	ToolRegenerateReportOutline:   {},
	ToolRegenerateReportText:      {},
	ToolRegenerateReportSection:   {},
}

var DefaultDocumentReportToolNames = []string{
	defaultDocumentToolAlias + "__" + ToolGenerateReportOutline,
	defaultDocumentToolAlias + "__" + ToolGenerateReportFromContent,
	defaultDocumentToolAlias + "__" + ToolGenerateReportText,
	defaultDocumentToolAlias + "__" + ToolGetGenerationStatus,
	defaultDocumentToolAlias + "__" + ToolExportReportDOCX,
	defaultDocumentToolAlias + "__" + ToolGetReportResult,
	defaultDocumentToolAlias + "__" + ToolListReports,
	defaultDocumentToolAlias + "__" + ToolGetReport,
	defaultDocumentToolAlias + "__" + ToolListReportFiles,
	defaultDocumentToolAlias + "__" + ToolReadReportFile,
}

func OriginalToolName(toolName string) string {
	name := strings.TrimSpace(toolName)
	if _, suffix, ok := strings.Cut(name, "__"); ok {
		return suffix
	}
	if _, suffix, ok := strings.Cut(name, "."); ok {
		return suffix
	}
	return name
}

func IsDocumentReportTool(toolName string) bool {
	_, ok := documentReportToolNames[OriginalToolName(toolName)]
	return ok
}

type documentToolResult struct {
	Status     string              `json:"status"`
	Job        *documentJob        `json:"job"`
	Report     *documentReport     `json:"report"`
	ReportFile *documentReportFile `json:"reportFile"`
	Error      *documentToolError  `json:"error"`
	Warnings   []string            `json:"warnings"`
}

type documentJob struct {
	ID        string         `json:"id"`
	ReportID  string         `json:"reportId"`
	JobType   string         `json:"jobType"`
	Status    string         `json:"status"`
	Progress  map[string]any `json:"progress"`
	ErrorCode string         `json:"errorCode"`
}

type documentReport struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ReportType         string `json:"reportType"`
	Status             string `json:"status"`
	LatestJobID        string `json:"latestJobId"`
	LatestReportFileID string `json:"latestReportFileId"`
}

type documentReportFile struct {
	ID       string `json:"id"`
	ReportID string `json:"reportId"`
	JobID    string `json:"jobId"`
	Filename string `json:"filename"`
	Format   string `json:"format"`
	FileSize int64  `json:"fileSize"`
	Status   string `json:"status"`
}

type documentToolError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

func generateDocumentReportResultSummary(toolName, content string) (map[string]any, bool) {
	if !IsDocumentReportTool(toolName) {
		return nil, false
	}
	var decoded documentToolResult
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		return map[string]any{
			"tool":           toolName,
			"error":          "invalid_document_tool_result",
			"message":        "document tool returned an invalid result",
			"sanitized":      true,
			"reportArtifact": failedReportArtifact("dependency_error", "document tool result is unavailable"),
		}, true
	}
	artifact := buildReportArtifact(decoded)
	summary := map[string]any{
		"tool":           toolName,
		"sanitized":      true,
		"reportArtifact": artifact,
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Code) != "" {
		summary["error"] = sanitizeDocumentErrorCode(decoded.Error.Code)
		summary["message"] = documentErrorMessage(decoded.Error.Code)
	}
	return summary, true
}

func buildReportArtifact(result documentToolResult) map[string]any {
	artifact := map[string]any{"artifactType": "report_generation"}
	if result.Job != nil {
		putString(artifact, "jobId", result.Job.ID)
		putString(artifact, "reportId", result.Job.ReportID)
		putString(artifact, "jobType", result.Job.JobType)
		putString(artifact, "jobStatus", normalizeArtifactStatus(firstNonBlank(result.Job.Status, result.Status)))
	}
	if result.Report != nil {
		putString(artifact, "reportId", result.Report.ID)
		putString(artifact, "reportName", result.Report.Name)
		putString(artifact, "reportType", result.Report.ReportType)
		putString(artifact, "reportStatus", result.Report.Status)
		if artifact["jobId"] == nil {
			putString(artifact, "jobId", result.Report.LatestJobID)
		}
	}
	if result.ReportFile != nil {
		putString(artifact, "reportId", result.ReportFile.ReportID)
		putString(artifact, "reportFileId", result.ReportFile.ID)
		putString(artifact, "filename", result.ReportFile.Filename)
		if strings.EqualFold(result.ReportFile.Format, "docx") {
			artifact["format"] = "docx"
		}
		if result.ReportFile.FileSize >= 0 {
			artifact["fileSize"] = result.ReportFile.FileSize
		}
		fileStatus := normalizeArtifactStatus(result.ReportFile.Status)
		putString(artifact, "fileStatus", fileStatus)
		if artifact["jobId"] == nil {
			putString(artifact, "jobId", result.ReportFile.JobID)
		}
		if fileStatus == "succeeded" && safePathSegment(result.ReportFile.ID) {
			artifact["downloadPath"] = "/api/v1/report-files/" + result.ReportFile.ID + "/content"
		}
	}
	if reportID, _ := artifact["reportId"].(string); safePathSegment(reportID) {
		artifact["detailPath"] = "/api/v1/reports/" + reportID
	}
	artifact["preview"] = reportArtifactPreview(result, artifact)
	if result.Error != nil && result.Job == nil && result.Report == nil && result.ReportFile == nil {
		putString(artifact, "jobStatus", "failed")
	}
	return artifact
}

func reportArtifactPreview(result documentToolResult, artifact map[string]any) map[string]any {
	status, _ := artifact["jobStatus"].(string)
	if status == "" {
		status, _ = artifact["fileStatus"].(string)
	}
	if status == "" {
		status = normalizeArtifactStatus(result.Status)
	}
	preview := map[string]any{
		"title":      reportPreviewTitle(status, result),
		"statusText": reportStatusText(status, result),
	}
	if percent, ok := progressPercent(result.Job); ok {
		preview["progressPercent"] = percent
	}
	if result.Error != nil {
		preview["summary"] = documentErrorMessage(result.Error.Code)
	}
	return preview
}

func progressPercent(job *documentJob) (int, bool) {
	if job == nil || len(job.Progress) == 0 {
		return 0, false
	}
	for _, key := range []string{"percent", "progressPercent", "percentage"} {
		if value, ok := numberFromAny(job.Progress[key]); ok {
			return clampPercent(value), true
		}
	}
	return 0, false
}

func numberFromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func clampPercent(value float64) int {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return int(math.Round(value))
}

func normalizeArtifactStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accepted":
		return "accepted"
	case "pending", "queued":
		return "pending"
	case "running", "processing", "in_progress":
		return "running"
	case "succeeded", "success", "completed", "done":
		return "succeeded"
	case "failed", "error":
		return "failed"
	case "canceled", "cancelled":
		return "canceled"
	default:
		if strings.TrimSpace(value) == "" {
			return ""
		}
		return "running"
	}
}

func reportPreviewTitle(status string, result documentToolResult) string {
	if result.ReportFile != nil {
		switch status {
		case "succeeded":
			return "DOCX report export ready"
		case "failed":
			return "DOCX report export failed"
		default:
			return "DOCX report export is running"
		}
	}
	switch status {
	case "accepted", "pending", "running":
		return "Report generation is running"
	case "succeeded":
		return "Report generation completed"
	case "failed":
		return "Report generation failed"
	default:
		return "Report artifact updated"
	}
}

func reportStatusText(status string, result documentToolResult) string {
	if result.Error != nil {
		return documentErrorMessage(result.Error.Code)
	}
	if result.Job != nil && result.Job.JobType != "" && status != "" {
		return fmt.Sprintf("%s %s", result.Job.JobType, status)
	}
	if status != "" {
		return status
	}
	return "updated"
}

func failedReportArtifact(code, message string) map[string]any {
	return map[string]any{
		"artifactType": "report_generation",
		"jobStatus":    "failed",
		"preview": map[string]any{
			"title":      "Report generation failed",
			"summary":    message,
			"statusText": documentErrorMessage(code),
		},
	}
}

func sanitizeDocumentErrorCode(code string) string {
	code = strings.TrimSpace(code)
	switch code {
	case "forbidden", "policy_denied":
		return "policy_denied"
	case "dependency_error", "validation_error", "not_found", "conflict", "internal_error":
		return code
	case "":
		return "dependency_error"
	default:
		return "dependency_error"
	}
}

func documentErrorMessage(code string) string {
	switch sanitizeDocumentErrorCode(code) {
	case "policy_denied":
		return "report access is not allowed"
	case "validation_error":
		return "report tool request is invalid"
	case "not_found":
		return "report artifact was not found"
	case "conflict":
		return "report artifact is not ready"
	default:
		return "document report service is unavailable"
	}
}

func putString(target map[string]any, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		target[key] = value
	}
}

func safePathSegment(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
