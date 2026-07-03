package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	DocumentMCPToolGenerateReportOutline     = "generate_report_outline"
	DocumentMCPToolGenerateReportFromContent = "generate_report_from_content"
	DocumentMCPToolRegenerateReportOutline   = "regenerate_report_outline"
	DocumentMCPToolGenerateReportText        = "generate_report_text"
	DocumentMCPToolRegenerateReportText      = "regenerate_report_text"
	DocumentMCPToolRegenerateReportSection   = "regenerate_report_section"
	DocumentMCPToolGetGenerationStatus       = "get_generation_status"
	DocumentMCPToolGetTemplateSchema         = "get_template_schema"
	DocumentMCPToolExportReportDOCX          = "export_report_docx"
	DocumentMCPToolGetReportResult           = "get_report_result"
	DocumentMCPToolListReports               = "list_reports"
	DocumentMCPToolGetReport                 = "get_report"
	DocumentMCPToolListReportFiles           = "list_report_files"
	DocumentMCPToolReadReportFile            = "read_report_file"
	OperationDocumentMCPToolCall             = "document_mcp_tool_call"
	documentMCPRequestSource                 = "mcp"
	documentMCPToolResultSucceeded           = "succeeded"
	documentMCPToolResultFailed              = "failed"
	documentMCPToolResultAccepted            = "accepted"
	documentMCPDefaultExportFormat           = ReportFileFormatDOCX
	documentMCPErrorUnsupported              = "unsupported"
	documentMCPDefaultContentReportType      = "summer_peak_inspection"
	documentMCPMaxSourceContentBytes         = 20_000
	documentMCPMaxReportFileContentBytes     = 1 << 20
	documentMCPMaxReportFileRawBytes         = 32 << 20
)

// MCPDocumentService is the subset of the Document metadata service used by
// Document MCP tools. Keeping it narrow prevents the tool layer from reaching
// through to repositories or storage internals.
type MCPDocumentService interface {
	GetReportTemplateStructure(context.Context, RequestContext, string) (ReportTemplateStructure, error)
}

type MCPJobService interface {
	CreateJob(context.Context, RequestContext, CreateJobInput) (ReportJob, error)
	GetJob(context.Context, RequestContext, string) (ReportJob, error)
}

type MCPReportService interface {
	CreateReport(context.Context, RequestContext, CreateReportInput) (Report, error)
	GetReport(context.Context, RequestContext, string) (Report, error)
	ListReports(context.Context, RequestContext, ReportListFilter) (ReportListResult, error)
}

type MCPReportSettingsService interface {
	GetReportSettings(ctx context.Context) (ReportSettings, error)
}

type MCPReportFileService interface {
	CreateReportFile(context.Context, RequestContext, CreateReportFileInput) (ReportFile, error)
	GetReportFile(context.Context, RequestContext, string) (ReportFile, error)
	ListReportFiles(context.Context, RequestContext, ReportFileListFilter) (ReportFileListResult, error)
	ReadReportFileContent(context.Context, RequestContext, string) (FileContent, error)
}

type MCPToolService struct {
	documents      MCPDocumentService
	jobs           MCPJobService
	reports        MCPReportService
	reportSettings MCPReportSettingsService
	reportFiles    MCPReportFileService
	recorder       OperationLogRecorder
	now            func() time.Time
}

type MCPToolServiceConfig struct {
	DocumentService       MCPDocumentService
	JobService            MCPJobService
	ReportService         MCPReportService
	ReportSettingsService MCPReportSettingsService
	ReportFileSvc         MCPReportFileService
	Recorder              OperationLogRecorder
}

func NewMCPToolService(cfg MCPToolServiceConfig) *MCPToolService {
	return &MCPToolService{
		documents:      cfg.DocumentService,
		jobs:           cfg.JobService,
		reports:        cfg.ReportService,
		reportSettings: cfg.ReportSettingsService,
		reportFiles:    cfg.ReportFileSvc,
		recorder:       cfg.Recorder,
		now:            func() time.Time { return time.Now().UTC() },
	}
}

type MCPToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type MCPToolCallResult struct {
	RequestID         string                       `json:"requestId"`
	ToolName          string                       `json:"toolName"`
	Status            string                       `json:"status"`
	Job               *MCPReportJobSummary         `json:"job,omitempty"`
	Report            *MCPReportSummary            `json:"report,omitempty"`
	Reports           []MCPReportSummary           `json:"reports,omitempty"`
	ReportFile        *MCPReportFileSummary        `json:"reportFile,omitempty"`
	ReportFiles       []MCPReportFileSummary       `json:"reportFiles,omitempty"`
	ReportFileContent *MCPReportFileContentSummary `json:"reportFileContent,omitempty"`
	TemplateSchema    *MCPTemplateSchemaSummary    `json:"templateSchema,omitempty"`
	TotalCount        int                          `json:"totalCount,omitempty"`
	Page              int                          `json:"page,omitempty"`
	PageSize          int                          `json:"pageSize,omitempty"`
	Error             *MCPToolError                `json:"error,omitempty"`
	Warnings          []string                     `json:"warnings,omitempty"`
}

type MCPToolError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type MCPReportJobSummary struct {
	ID         string         `json:"id"`
	ReportID   string         `json:"reportId"`
	JobType    string         `json:"jobType"`
	TargetType string         `json:"targetType,omitempty"`
	TargetID   string         `json:"targetId,omitempty"`
	Status     string         `json:"status"`
	Progress   map[string]any `json:"progress,omitempty"`
	ErrorCode  string         `json:"errorCode,omitempty"`
	CreatedAt  string         `json:"createdAt,omitempty"`
}

type MCPReportSummary struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	ReportType         string `json:"reportType,omitempty"`
	TemplateID         string `json:"templateId,omitempty"`
	Topic              string `json:"topic,omitempty"`
	Specialty          string `json:"specialty,omitempty"`
	BusinessObject     string `json:"businessObject,omitempty"`
	Year               int    `json:"year,omitempty"`
	Status             string `json:"status"`
	LatestJobID        string `json:"latestJobId,omitempty"`
	LatestReportFileID string `json:"latestReportFileId,omitempty"`
	CreatedAt          string `json:"createdAt,omitempty"`
	GeneratedAt        string `json:"generatedAt,omitempty"`
	ExportedAt         string `json:"exportedAt,omitempty"`
	UpdatedAt          string `json:"updatedAt,omitempty"`
}

type MCPReportFileSummary struct {
	ID          string `json:"id"`
	ReportID    string `json:"reportId"`
	JobID       string `json:"jobId,omitempty"`
	Filename    string `json:"filename,omitempty"`
	Format      string `json:"format"`
	FileSize    int64  `json:"fileSize,omitempty"`
	Status      string `json:"status"`
	ContentPath string `json:"contentPath,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

type MCPReportFileContentSummary struct {
	ReportFileID string `json:"reportFileId"`
	Filename     string `json:"filename,omitempty"`
	Content      string `json:"content"`
	Format       string `json:"format"`
	FileSize     int64  `json:"fileSize,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
}

type MCPTemplateSchemaSummary struct {
	TemplateID    string          `json:"templateId"`
	OutlineSchema json.RawMessage `json:"outlineSchema"`
	StyleConfig   json.RawMessage `json:"styleConfig"`
}

func (s *MCPToolService) ListTools(context.Context) []MCPToolDefinition {
	return []MCPToolDefinition{
		toolDefinition(DocumentMCPToolGenerateReportOutline, "Create an outline generation report job.", jobToolSchema(false)),
		toolDefinition(DocumentMCPToolGenerateReportFromContent, "Create a report and outline generation job from bounded source content.", reportFromContentSchema()),
		toolDefinition(DocumentMCPToolRegenerateReportOutline, "Create an outline regeneration report job.", jobToolSchema(false)),
		toolDefinition(DocumentMCPToolGenerateReportText, "Create a report content generation job.", jobToolSchema(false)),
		toolDefinition(DocumentMCPToolRegenerateReportText, "Create a report content regeneration job.", jobToolSchema(false)),
		toolDefinition(DocumentMCPToolRegenerateReportSection, "Create a section regeneration job.", jobToolSchema(true)),
		toolDefinition(DocumentMCPToolGetGenerationStatus, "Read a report generation job status.", requiredStringSchema("jobId", "Report job ID.")),
		toolDefinition(DocumentMCPToolGetTemplateSchema, "Read a report template structure schema.", requiredStringSchema("templateId", "Report template ID.")),
		toolDefinition(DocumentMCPToolExportReportDOCX, "Create a basic DOCX report export job.", exportDOCXSchema()),
		toolDefinition(DocumentMCPToolGetReportResult, "Read a safe report result summary.", requiredStringSchema("reportId", "Report ID.")),
		toolDefinition(DocumentMCPToolListReports, "Query reports with optional type and status filters.", listReportsSchema()),
		toolDefinition(DocumentMCPToolGetReport, "Read detailed metadata for a report.", requiredStringSchema("reportId", "Report ID.")),
		toolDefinition(DocumentMCPToolListReportFiles, "List generated report files for a report.", requiredStringSchema("reportId", "Report ID.")),
		toolDefinition(DocumentMCPToolReadReportFile, "Read generated report file content as bounded text or markdown.", readReportFileSchema()),
	}
}

func (s *MCPToolService) CallTool(ctx context.Context, reqCtx RequestContext, name string, arguments json.RawMessage) MCPToolCallResult {
	reqCtx = normalizeMCPRequestContext(reqCtx)
	result := MCPToolCallResult{
		RequestID: reqCtx.RequestID,
		ToolName:  strings.TrimSpace(name),
	}
	args, err := decodeMCPArguments(arguments)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"argumentShape": "invalid"})
		return result
	}

	switch result.ToolName {
	case DocumentMCPToolGenerateReportOutline:
		result = s.createGenerationJob(ctx, reqCtx, result.ToolName, JobTypeOutlineGeneration, args, false)
	case DocumentMCPToolGenerateReportFromContent:
		result = s.generateReportFromContent(ctx, reqCtx, args)
	case DocumentMCPToolRegenerateReportOutline:
		result = s.createGenerationJob(ctx, reqCtx, result.ToolName, JobTypeOutlineRegeneration, args, false)
	case DocumentMCPToolGenerateReportText:
		result = s.createGenerationJob(ctx, reqCtx, result.ToolName, JobTypeContentGeneration, args, false)
	case DocumentMCPToolRegenerateReportText:
		result = s.createGenerationJob(ctx, reqCtx, result.ToolName, JobTypeContentRegeneration, args, false)
	case DocumentMCPToolRegenerateReportSection:
		result = s.createGenerationJob(ctx, reqCtx, result.ToolName, JobTypeSectionRegeneration, args, true)
	case DocumentMCPToolGetGenerationStatus:
		result = s.getGenerationStatus(ctx, reqCtx, args)
	case DocumentMCPToolGetTemplateSchema:
		result = s.getTemplateSchema(ctx, reqCtx, args)
	case DocumentMCPToolExportReportDOCX:
		result = s.exportReportDOCX(ctx, reqCtx, args)
	case DocumentMCPToolGetReportResult:
		result = s.getReportResult(ctx, reqCtx, args)
	case DocumentMCPToolListReports:
		result = s.listReports(ctx, reqCtx, args)
	case DocumentMCPToolGetReport:
		result = s.getReport(ctx, reqCtx, args)
	case DocumentMCPToolListReportFiles:
		result = s.listReportFiles(ctx, reqCtx, args)
	case DocumentMCPToolReadReportFile:
		result = s.readReportFile(ctx, reqCtx, args)
	default:
		result.Status = documentMCPToolResultFailed
		result.Error = &MCPToolError{Code: documentMCPErrorUnsupported, Message: "document MCP tool is not supported"}
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"toolSupported": false})
	}
	if result.RequestID == "" {
		result.RequestID = reqCtx.RequestID
	}
	if result.ToolName == "" {
		result.ToolName = name
	}
	return result
}

func (s *MCPToolService) generateReportFromContent(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolGenerateReportFromContent}
	summary := reportFromContentParameterSummary(args, 0, false)
	if s.reportSettings == nil || s.reports == nil || s.jobs == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "document report services are not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, summary)
		return result
	}
	content := strings.TrimSpace(stringArgument(args, "content"))
	if content == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"content": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, summary)
		return result
	}
	source := buildBoundedSourceContent(content, stringArgument(args, "document_name", "documentName"))
	summary = reportFromContentParameterSummary(args, len(source.Excerpt), source.Truncated)
	settings, err := s.reportSettings.GetReportSettings(ctx)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(dependencyError("get report settings", err))
		s.recordToolCall(ctx, reqCtx, result, summary)
		return result
	}
	settings = normalizeReportSettings(settings)
	templateID := strings.TrimSpace(settings.DefaultTemplates[documentMCPDefaultContentReportType])
	if templateID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"defaultTemplates." + documentMCPDefaultContentReportType: "default template is required"}))
		s.recordToolCall(ctx, reqCtx, result, summary)
		return result
	}
	reportName := source.DocumentName
	if reportName == "" {
		reportName = "MCP Content Report"
	}
	report, err := s.reports.CreateReport(ctx, reqCtx, CreateReportInput{
		Name:       reportName,
		ReportType: documentMCPDefaultContentReportType,
		TemplateID: templateID,
		Topic:      reportName,
		Year:       s.now().Year(),
		Source:     documentMCPRequestSource,
	})
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, summary)
		return result
	}
	result.Report = reportSummary(report)
	options := map[string]any{
		"sourceContent": source.asPayload(),
	}
	job, err := s.jobs.CreateJob(ctx, reqCtx, CreateJobInput{
		RequestID:    reqCtx.RequestID,
		UserID:       reqCtx.UserID,
		ReportID:     report.ID,
		JobType:      JobTypeOutlineGeneration,
		Requirements: stringArgument(args, "instructions"),
		Options:      options,
	})
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, summary)
		return result
	}
	result.Status = documentMCPToolResultAccepted
	result.Job = reportJobSummary(job)
	if result.Job != nil {
		result.Job.Status = documentMCPToolResultAccepted
	}
	if source.Truncated {
		result.Warnings = append(result.Warnings, "content_truncated")
	}
	s.recordToolCall(ctx, reqCtx, result, summary)
	return result
}

func (s *MCPToolService) createGenerationJob(ctx context.Context, reqCtx RequestContext, toolName string, jobType JobType, args map[string]any, requiresSection bool) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: toolName}
	if s.jobs == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report job service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, requiresSection))
		return result
	}
	reportID := stringArgument(args, "reportId", "report_id")
	if reportID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"reportId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, requiresSection))
		return result
	}
	sectionID := ""
	targetScope := ""
	if requiresSection {
		sectionID = stringArgument(args, "sectionId", "section_id")
		if sectionID == "" {
			result.Status = documentMCPToolResultFailed
			result.Error = toolErrorFromError(ValidationError(map[string]string{"sectionId": "is required"}))
			s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, true))
			return result
		}
		targetScope = "section"
	}
	materialIDs, err := stringSliceArgumentStrict(args, "materialIds", "material_ids", "materialRefs", "material_refs")
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"materialIds": "must be an array of strings"}))
		s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, requiresSection))
		return result
	}
	options, err := mapArgumentStrict(args, "options")
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"options": "must be a JSON object"}))
		s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, requiresSection))
		return result
	}
	retrieval, err := mapArgumentStrict(args, "retrieval")
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"retrieval": "must be a JSON object"}))
		s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, requiresSection))
		return result
	}
	job, err := s.jobs.CreateJob(ctx, reqCtx, CreateJobInput{
		RequestID:    reqCtx.RequestID,
		UserID:       reqCtx.UserID,
		ReportID:     reportID,
		JobType:      jobType,
		TargetScope:  targetScope,
		SectionID:    sectionID,
		Requirements: stringArgument(args, "requirements"),
		MaterialIDs:  materialIDs,
		Options:      options,
		Retrieval:    retrieval,
	})
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, requiresSection))
		return result
	}
	result.Status = documentMCPToolResultAccepted
	result.Job = reportJobSummary(job)
	s.recordToolCall(ctx, reqCtx, result, generationParameterSummary(args, jobType, requiresSection))
	return result
}

func (s *MCPToolService) getGenerationStatus(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolGetGenerationStatus}
	if s.jobs == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report job service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"jobIdProvided": stringArgument(args, "jobId", "job_id") != ""})
		return result
	}
	jobID := stringArgument(args, "jobId", "job_id")
	if jobID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"jobId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"jobIdProvided": false})
		return result
	}
	job, err := s.jobs.GetJob(ctx, reqCtx, jobID)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"jobId": jobID})
		return result
	}
	result.Status = documentMCPToolResultSucceeded
	result.Job = reportJobSummary(job)
	s.recordToolCall(ctx, reqCtx, result, map[string]any{"jobId": job.ID})
	return result
}

func (s *MCPToolService) getTemplateSchema(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolGetTemplateSchema}
	if s.documents == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "document service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"templateIdProvided": stringArgument(args, "templateId", "template_id") != ""})
		return result
	}
	templateID := stringArgument(args, "templateId", "template_id", "reportTemplateId", "report_template_id")
	if templateID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"templateId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"templateIdProvided": false})
		return result
	}
	structure, err := s.documents.GetReportTemplateStructure(ctx, reqCtx, templateID)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"templateId": templateID})
		return result
	}
	result.Status = documentMCPToolResultSucceeded
	result.TemplateSchema = &MCPTemplateSchemaSummary{
		TemplateID:    templateID,
		OutlineSchema: emptyJSONIfNil(structure.OutlineSchema),
		StyleConfig:   emptyJSONIfNil(structure.StyleConfig),
	}
	s.recordToolCall(ctx, reqCtx, result, map[string]any{"templateId": templateID})
	return result
}

func (s *MCPToolService) exportReportDOCX(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolExportReportDOCX}
	if s.reportFiles == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report file service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, exportParameterSummary(args))
		return result
	}
	reportID := stringArgument(args, "reportId", "report_id")
	if reportID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"reportId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, exportParameterSummary(args))
		return result
	}
	format := strings.ToLower(stringArgument(args, "format"))
	if format == "" {
		format = documentMCPDefaultExportFormat
	}
	if format != ReportFileFormatDOCX {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"format": "must be docx"}))
		s.recordToolCall(ctx, reqCtx, result, exportParameterSummary(args))
		return result
	}
	styleOptions, err := rawJSONArgument(args, "exportOptions", "export_options", "styleOptions", "style_options")
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"exportOptions": "must be a JSON object"}))
		s.recordToolCall(ctx, reqCtx, result, exportParameterSummary(args))
		return result
	}
	reportFile, err := s.reportFiles.CreateReportFile(ctx, reqCtx, CreateReportFileInput{
		ReportID:     reportID,
		Format:       ReportFileFormatDOCX,
		TemplateID:   stringArgument(args, "templateId", "template_id"),
		StyleOptions: styleOptions,
	})
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, exportParameterSummary(args))
		return result
	}
	result.Status = documentMCPToolResultAccepted
	result.ReportFile = reportFileSummary(reportFile)
	if result.ReportFile != nil {
		result.Job = &MCPReportJobSummary{ID: result.ReportFile.JobID, ReportID: result.ReportFile.ReportID, JobType: string(JobTypeReportFileCreation), Status: string(reportFile.Status)}
	}
	s.recordToolCall(ctx, reqCtx, result, exportParameterSummary(args))
	return result
}

func (s *MCPToolService) getReportResult(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolGetReportResult}
	if s.reports == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportIdProvided": stringArgument(args, "reportId", "report_id") != ""})
		return result
	}
	reportID := stringArgument(args, "reportId", "report_id")
	if reportID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"reportId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportIdProvided": false})
		return result
	}
	report, err := s.reports.GetReport(ctx, reqCtx, reportID)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportId": reportID})
		return result
	}
	result.Status = documentMCPToolResultSucceeded
	result.Report = reportSummary(report)
	if report.LatestReportFileID != "" && s.reportFiles != nil {
		reportFile, err := s.reportFiles.GetReportFile(ctx, reqCtx, report.LatestReportFileID)
		if err != nil {
			result.Status = documentMCPToolResultFailed
			result.Error = toolErrorFromError(err)
			s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportId": reportID, "latestReportFileId": report.LatestReportFileID})
			return result
		}
		result.ReportFile = reportFileSummary(reportFile)
	}
	s.recordToolCall(ctx, reqCtx, result, map[string]any{
		"reportId":              report.ID,
		"latestReportFileIdSet": report.LatestReportFileID != "",
	})
	return result
}

func (s *MCPToolService) listReports(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolListReports}
	if s.reports == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, listReportsParameterSummary(args))
		return result
	}
	page, pageSize, err := paginationArguments(args)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, listReportsParameterSummary(args))
		return result
	}
	list, err := s.reports.ListReports(ctx, reqCtx, ReportListFilter{
		Page:       page,
		PageSize:   pageSize,
		ReportType: stringArgument(args, "reportType", "report_type"),
		Status:     stringArgument(args, "status"),
	})
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, listReportsParameterSummary(args))
		return result
	}
	result.Status = documentMCPToolResultSucceeded
	result.Reports = reportSummaries(list.Items)
	result.TotalCount = list.Page.Total
	result.Page = list.Page.Page
	result.PageSize = list.Page.PageSize
	s.recordToolCall(ctx, reqCtx, result, map[string]any{
		"reportType":  stringArgument(args, "reportType", "report_type"),
		"status":      stringArgument(args, "status"),
		"page":        result.Page,
		"pageSize":    result.PageSize,
		"resultCount": len(result.Reports),
	})
	return result
}

func (s *MCPToolService) getReport(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolGetReport}
	if s.reports == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportIdProvided": stringArgument(args, "reportId", "report_id") != ""})
		return result
	}
	reportID := stringArgument(args, "reportId", "report_id")
	if reportID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"reportId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportIdProvided": false})
		return result
	}
	report, err := s.reports.GetReport(ctx, reqCtx, reportID)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportId": reportID})
		return result
	}
	result.Status = documentMCPToolResultSucceeded
	result.Report = reportSummary(report)
	s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportId": report.ID})
	return result
}

func (s *MCPToolService) listReportFiles(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolListReportFiles}
	if s.reportFiles == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report file service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportIdProvided": stringArgument(args, "reportId", "report_id") != ""})
		return result
	}
	reportID := stringArgument(args, "reportId", "report_id")
	if reportID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"reportId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportIdProvided": false})
		return result
	}
	list, err := s.reportFiles.ListReportFiles(ctx, reqCtx, ReportFileListFilter{
		Page:     DefaultPage,
		PageSize: MaxPageSize,
		ReportID: reportID,
	})
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, map[string]any{"reportId": reportID})
		return result
	}
	result.Status = documentMCPToolResultSucceeded
	result.ReportFiles = reportFileSummaries(list.Items)
	result.TotalCount = list.Page.Total
	result.Page = list.Page.Page
	result.PageSize = list.Page.PageSize
	s.recordToolCall(ctx, reqCtx, result, map[string]any{
		"reportId":    reportID,
		"resultCount": len(result.ReportFiles),
	})
	return result
}

func (s *MCPToolService) readReportFile(ctx context.Context, reqCtx RequestContext, args map[string]any) MCPToolCallResult {
	result := MCPToolCallResult{RequestID: reqCtx.RequestID, ToolName: DocumentMCPToolReadReportFile}
	if s.reportFiles == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report file service is not configured", nil))
		s.recordToolCall(ctx, reqCtx, result, readReportFileParameterSummary(args, 0, false))
		return result
	}
	reportFileID := stringArgument(args, "reportFileId", "report_file_id")
	if reportFileID == "" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"reportFileId": "is required"}))
		s.recordToolCall(ctx, reqCtx, result, readReportFileParameterSummary(args, 0, false))
		return result
	}
	format := strings.ToLower(stringArgument(args, "format"))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "markdown" {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(ValidationError(map[string]string{"format": "must be text or markdown"}))
		s.recordToolCall(ctx, reqCtx, result, readReportFileParameterSummary(args, 0, false))
		return result
	}
	content, err := s.reportFiles.ReadReportFileContent(ctx, reqCtx, reportFileID)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, readReportFileParameterSummary(args, 0, false))
		return result
	}
	if content.Content == nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(NewError(CodeDependency, "report file content is not available", nil))
		s.recordToolCall(ctx, reqCtx, result, readReportFileParameterSummary(args, 0, false))
		return result
	}
	defer content.Content.Close()

	text, truncated, err := readSafeReportFileText(content)
	if err != nil {
		result.Status = documentMCPToolResultFailed
		result.Error = toolErrorFromError(err)
		s.recordToolCall(ctx, reqCtx, result, readReportFileParameterSummary(args, 0, false))
		return result
	}
	result.Status = documentMCPToolResultSucceeded
	result.ReportFileContent = &MCPReportFileContentSummary{
		ReportFileID: reportFileID,
		Filename:     content.Filename,
		Content:      renderReportFileContent(text, format),
		Format:       format,
		FileSize:     content.SizeBytes,
		Truncated:    truncated,
	}
	s.recordToolCall(ctx, reqCtx, result, readReportFileParameterSummary(args, len([]byte(result.ReportFileContent.Content)), truncated))
	return result
}

func (s *MCPToolService) recordToolCall(ctx context.Context, reqCtx RequestContext, result MCPToolCallResult, summary map[string]any) {
	if s.recorder == nil {
		return
	}
	operationResult := OperationResultSucceeded
	if result.Status == documentMCPToolResultFailed || result.Error != nil {
		operationResult = OperationResultFailed
	}
	targetType, targetID := mcpResultTarget(result)
	errorMessage := ""
	if result.Error != nil {
		errorMessage = result.Error.Code
	}
	recordOperationLog(ctx, s.recorder, OperationLog{
		OperatorID:       reqCtx.UserID,
		OperatorName:     reqCtx.UserID,
		OperationType:    OperationDocumentMCPToolCall,
		TargetType:       targetType,
		TargetID:         targetID,
		RequestID:        reqCtx.RequestID,
		RequestSource:    documentMCPRequestSource,
		ToolName:         result.ToolName,
		OperationResult:  operationResult,
		ErrorMessage:     errorMessage,
		ParameterSummary: safeToolSummary(summary),
		Metadata: map[string]any{
			"status": result.Status,
		},
		CreatedAt: s.now(),
	})
}

func mcpResultTarget(result MCPToolCallResult) (string, string) {
	if result.Job != nil && result.Job.ID != "" {
		return "job", result.Job.ID
	}
	if result.ReportFile != nil && result.ReportFile.ID != "" {
		return "report_file", result.ReportFile.ID
	}
	if result.ReportFileContent != nil && result.ReportFileContent.ReportFileID != "" {
		return "report_file", result.ReportFileContent.ReportFileID
	}
	if result.Report != nil && result.Report.ID != "" {
		return "report", result.Report.ID
	}
	if result.TemplateSchema != nil && result.TemplateSchema.TemplateID != "" {
		return "report_template", result.TemplateSchema.TemplateID
	}
	return "mcp_tool", result.ToolName
}

func normalizeMCPRequestContext(reqCtx RequestContext) RequestContext {
	if strings.TrimSpace(reqCtx.RequestID) == "" {
		reqCtx.RequestID = "req_" + newID()
	}
	if strings.TrimSpace(reqCtx.CallerService) == "" {
		reqCtx.CallerService = documentMCPRequestSource
	}
	return reqCtx
}

func decodeMCPArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, ValidationError(map[string]string{"arguments": "must be a JSON object"})
	}
	if args == nil {
		return map[string]any{}, nil
	}
	return args, nil
}

func stringArgument(args map[string]any, names ...string) string {
	for _, name := range names {
		value, ok := args[name]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func stringSliceArgument(args map[string]any, names ...string) []string {
	values, _ := stringSliceArgumentStrict(args, names...)
	return values
}

func stringSliceArgumentStrict(args map[string]any, names ...string) ([]string, error) {
	for _, name := range names {
		value, ok := args[name]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return compactStrings(typed), nil
		case []any:
			values := make([]string, 0, len(typed))
			for _, item := range typed {
				text, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("argument %s must be an array of strings", name)
				}
				if strings.TrimSpace(text) != "" {
					values = append(values, strings.TrimSpace(text))
				}
			}
			return values, nil
		default:
			return nil, fmt.Errorf("argument %s must be an array of strings", name)
		}
	}
	return nil, nil
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func mapArgument(args map[string]any, names ...string) map[string]any {
	value, _ := mapArgumentStrict(args, names...)
	return value
}

func mapArgumentStrict(args map[string]any, names ...string) (map[string]any, error) {
	for _, name := range names {
		value, ok := args[name]
		if !ok || value == nil {
			continue
		}
		if mapped, ok := value.(map[string]any); ok {
			return mapped, nil
		}
		return nil, fmt.Errorf("argument %s must be a JSON object", name)
	}
	return nil, nil
}

func rawJSONArgument(args map[string]any, names ...string) (json.RawMessage, error) {
	for _, name := range names {
		value, ok := args[name]
		if !ok || value == nil {
			continue
		}
		if _, ok := value.(map[string]any); !ok {
			return nil, fmt.Errorf("argument %s must be a JSON object", name)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	return nil, nil
}

func toolErrorFromError(err error) *MCPToolError {
	if err == nil {
		return nil
	}
	if appErr, ok := Classify(err); ok {
		return &MCPToolError{Code: string(appErr.Code), Message: appErr.Message, Fields: appErr.Fields}
	}
	return &MCPToolError{Code: string(CodeInternal), Message: "document MCP tool failed"}
}

func reportJobSummary(job ReportJob) *MCPReportJobSummary {
	return &MCPReportJobSummary{
		ID:         job.ID,
		ReportID:   job.ReportID,
		JobType:    string(job.JobType),
		TargetType: job.TargetType,
		TargetID:   job.TargetID,
		Status:     string(job.Status),
		Progress:   safeToolSummary(job.Progress),
		ErrorCode:  job.ErrorCode,
		CreatedAt:  formatMCPTime(job.CreatedAt),
	}
}

func reportSummaries(reports []Report) []MCPReportSummary {
	if len(reports) == 0 {
		return []MCPReportSummary{}
	}
	result := make([]MCPReportSummary, 0, len(reports))
	for _, report := range reports {
		if summary := reportSummary(report); summary != nil {
			result = append(result, *summary)
		}
	}
	return result
}

func reportSummary(report Report) *MCPReportSummary {
	return &MCPReportSummary{
		ID:                 report.ID,
		Name:               report.Name,
		ReportType:         report.ReportType,
		TemplateID:         report.TemplateID,
		Topic:              report.Topic,
		Specialty:          report.Specialty,
		BusinessObject:     report.BusinessObject,
		Year:               report.Year,
		Status:             string(report.Status),
		LatestJobID:        report.LatestJobID,
		LatestReportFileID: report.LatestReportFileID,
		CreatedAt:          formatMCPTime(report.CreatedAt),
		GeneratedAt:        formatMCPTimePtr(report.GeneratedAt),
		ExportedAt:         formatMCPTimePtr(report.ExportedAt),
		UpdatedAt:          formatMCPTime(report.UpdatedAt),
	}
}

func reportFileSummaries(reportFiles []ReportFile) []MCPReportFileSummary {
	if len(reportFiles) == 0 {
		return []MCPReportFileSummary{}
	}
	result := make([]MCPReportFileSummary, 0, len(reportFiles))
	for _, reportFile := range reportFiles {
		if summary := reportFileSummary(reportFile); summary != nil {
			result = append(result, *summary)
		}
	}
	return result
}

func reportFileSummary(reportFile ReportFile) *MCPReportFileSummary {
	summary := &MCPReportFileSummary{
		ID:        reportFile.ID,
		ReportID:  reportFile.ReportID,
		JobID:     reportFile.JobID,
		Filename:  reportFile.Filename,
		Format:    reportFile.Format,
		FileSize:  reportFile.FileSize,
		Status:    string(reportFile.Status),
		CreatedAt: formatMCPTime(reportFile.CreatedAt),
	}
	if reportFile.ID != "" {
		summary.ContentPath = "/api/v1/report-files/" + reportFile.ID + "/content"
	}
	return summary
}

func paginationArguments(args map[string]any) (int, int, error) {
	page, err := intArgument(args, DefaultPage, "page")
	if err != nil {
		return 0, 0, ValidationError(map[string]string{"page": "must be an integer between 1 and 2147483647"})
	}
	pageSize, err := intArgument(args, DefaultPageSize, "pageSize", "page_size")
	if err != nil {
		return 0, 0, ValidationError(map[string]string{"pageSize": "must be an integer between 1 and 100"})
	}
	if page < 1 {
		return 0, 0, ValidationError(map[string]string{"page": "must be greater than or equal to 1"})
	}
	if pageSize < 1 || pageSize > MaxPageSize {
		return 0, 0, ValidationError(map[string]string{"pageSize": "must be between 1 and 100"})
	}
	return page, pageSize, nil
}

func intArgument(args map[string]any, fallback int, names ...string) (int, error) {
	for _, name := range names {
		value, ok := args[name]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int:
			if typed < math.MinInt32 || typed > math.MaxInt32 {
				return 0, fmt.Errorf("argument %s is out of range", name)
			}
			return typed, nil
		case int64:
			if typed < math.MinInt32 || typed > math.MaxInt32 {
				return 0, fmt.Errorf("argument %s is out of range", name)
			}
			return int(typed), nil
		case float64:
			if typed < math.MinInt32 || typed > math.MaxInt32 || typed != math.Trunc(typed) {
				return 0, fmt.Errorf("argument %s must be an integer", name)
			}
			return int(typed), nil
		case json.Number:
			parsed, err := typed.Int64()
			if err != nil {
				return 0, err
			}
			if parsed < math.MinInt32 || parsed > math.MaxInt32 {
				return 0, fmt.Errorf("argument %s is out of range", name)
			}
			return int(parsed), nil
		default:
			return 0, fmt.Errorf("argument %s must be an integer", name)
		}
	}
	return fallback, nil
}

func readSafeReportFileText(content FileContent) (string, bool, error) {
	if content.Content == nil {
		return "", false, NewError(CodeDependency, "report file content is not available", nil)
	}
	contentType := strings.ToLower(strings.TrimSpace(content.ContentType))
	if isTextReportFileContent(contentType) {
		raw, truncated, err := readBoundedReportFileBytes(content.Content, documentMCPMaxReportFileContentBytes)
		if err != nil {
			return "", false, dependencyError("read report file content", err)
		}
		return sanitizeReportFileText(string(raw), truncated)
	}

	raw, rawTruncated, err := readBoundedReportFileBytes(content.Content, documentMCPMaxReportFileRawBytes)
	if err != nil {
		return "", false, dependencyError("read report file content", err)
	}
	if rawTruncated {
		return "", false, NewError(CodeConflict, "report file content is too large to read as text", nil)
	}
	text, err := extractDOCXText(raw)
	if err != nil {
		return "", false, NewError(CodeConflict, "report file content is not readable as text", err)
	}
	return sanitizeReportFileText(text, false)
}

func readBoundedReportFileBytes(reader io.Reader, limit int) ([]byte, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	truncated := len(raw) > limit
	if truncated {
		raw = raw[:limit]
	}
	return raw, truncated, nil
}

func sanitizeReportFileText(text string, truncated bool) (string, bool, error) {
	text = strings.TrimSpace(strings.ToValidUTF8(text, ""))
	text = redactSourceContentFragments(text)
	if bounded, didTruncate := truncateUTF8ByBytes(text, documentMCPMaxReportFileContentBytes); didTruncate {
		text = strings.TrimSpace(bounded)
		truncated = true
	}
	return text, truncated, nil
}

func isTextReportFileContent(contentType string) bool {
	if strings.Contains(contentType, "markdown") || strings.HasPrefix(contentType, "text/") {
		return true
	}
	return false
}

func extractDOCXText(raw []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", err
	}
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		part, err := file.Open()
		if err != nil {
			return "", err
		}
		defer part.Close()
		return extractWordDocumentText(part)
	}
	return "", fmt.Errorf("word/document.xml not found")
}

func extractWordDocumentText(reader io.Reader) (string, error) {
	decoder := xml.NewDecoder(reader)
	var builder strings.Builder
	needsSpace := false
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "p", "tr":
				appendReportFileTextSeparator(&builder, "\n")
				needsSpace = false
			case "tc":
				if needsSpace {
					appendReportFileTextSeparator(&builder, "\t")
				}
			case "br":
				appendReportFileTextSeparator(&builder, "\n")
				needsSpace = false
			}
		case xml.CharData:
			text := strings.TrimSpace(string(typed))
			if text == "" {
				continue
			}
			if needsSpace && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			builder.WriteString(text)
			needsSpace = true
		}
		if builder.Len() > documentMCPMaxReportFileContentBytes {
			return builder.String(), nil
		}
	}
	return compactReportFileText(builder.String()), nil
}

func appendReportFileTextSeparator(builder *strings.Builder, sep string) {
	if builder.Len() == 0 {
		return
	}
	current := builder.String()
	if strings.HasSuffix(current, sep) || strings.HasSuffix(current, "\n\n") {
		return
	}
	builder.WriteString(sep)
}

func compactReportFileText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func renderReportFileContent(text, format string) string {
	if format != "markdown" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

func formatMCPTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func formatMCPTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatMCPTime(*value)
}

func emptyJSONIfNil(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func generationParameterSummary(args map[string]any, jobType JobType, section bool) map[string]any {
	requirements := stringArgument(args, "requirements")
	return safeToolSummary(map[string]any{
		"reportId":           stringArgument(args, "reportId", "report_id"),
		"sectionId":          stringArgument(args, "sectionId", "section_id"),
		"jobType":            string(jobType),
		"sectionTarget":      section,
		"requirementsLength": len(strings.TrimSpace(requirements)),
		"materialCount":      len(stringSliceArgument(args, "materialIds", "material_ids", "materialRefs", "material_refs")),
		"optionsProvided":    mapArgument(args, "options") != nil,
		"retrievalProvided":  mapArgument(args, "retrieval") != nil,
	})
}

func exportParameterSummary(args map[string]any) map[string]any {
	return safeToolSummary(map[string]any{
		"reportId":          stringArgument(args, "reportId", "report_id"),
		"templateId":        stringArgument(args, "templateId", "template_id"),
		"format":            firstNonEmpty(stringArgument(args, "format"), ReportFileFormatDOCX),
		"exportOptionsSet":  mapArgument(args, "exportOptions", "export_options", "styleOptions", "style_options") != nil,
		"richDocxRequested": false,
		"basicDocxExporter": true,
	})
}

func reportFromContentParameterSummary(args map[string]any, excerptLength int, truncated bool) map[string]any {
	content := strings.TrimSpace(stringArgument(args, "content"))
	instructions := strings.TrimSpace(stringArgument(args, "instructions"))
	return safeToolSummary(map[string]any{
		"contentLength":        len([]byte(content)),
		"excerptLength":        excerptLength,
		"truncated":            truncated,
		"documentNameProvided": stringArgument(args, "document_name", "documentName") != "",
		"instructionsLength":   len([]byte(instructions)),
	})
}

func listReportsParameterSummary(args map[string]any) map[string]any {
	return safeToolSummary(map[string]any{
		"reportType": stringArgument(args, "reportType", "report_type"),
		"status":     stringArgument(args, "status"),
		"page":       args["page"],
		"pageSize":   firstPresent(args, "pageSize", "page_size"),
	})
}

func listMaterialsParameterSummary(args map[string]any) map[string]any {
	return safeToolSummary(map[string]any{
		"category": stringArgument(args, "category"),
		"page":     args["page"],
		"pageSize": firstPresent(args, "pageSize", "page_size"),
	})
}

func readReportFileParameterSummary(args map[string]any, contentLength int, truncated bool) map[string]any {
	return safeToolSummary(map[string]any{
		"reportFileId":  stringArgument(args, "reportFileId", "report_file_id"),
		"format":        firstNonEmpty(stringArgument(args, "format"), "text"),
		"contentLength": contentLength,
		"truncated":     truncated,
	})
}

func firstPresent(args map[string]any, names ...string) any {
	for _, name := range names {
		if value, ok := args[name]; ok {
			return value
		}
	}
	return nil
}

type boundedSourceContent struct {
	DocumentName   string
	Excerpt        string
	OriginalLength int
	ExcerptLength  int
	Truncated      bool
}

func (s boundedSourceContent) asPayload() map[string]any {
	return map[string]any{
		"documentName":   s.DocumentName,
		"excerpt":        s.Excerpt,
		"originalLength": s.OriginalLength,
		"excerptLength":  s.ExcerptLength,
		"truncated":      s.Truncated,
	}
}

func buildBoundedSourceContent(content, documentName string) boundedSourceContent {
	trimmed := strings.TrimSpace(content)
	excerpt, truncated := truncateUTF8ByBytes(trimmed, documentMCPMaxSourceContentBytes)
	excerpt = redactSourceContentFragments(excerpt)
	documentName = redactSourceContentFragments(compactTextForPrompt(documentName, 160))
	return boundedSourceContent{
		DocumentName:   strings.TrimSpace(documentName),
		Excerpt:        strings.TrimSpace(excerpt),
		OriginalLength: len([]byte(trimmed)),
		ExcerptLength:  len([]byte(excerpt)),
		Truncated:      truncated,
	}
}

func truncateUTF8ByBytes(text string, limit int) (string, bool) {
	if limit <= 0 {
		return "", len(text) > 0
	}
	if len([]byte(text)) <= limit {
		return text, false
	}
	var builder strings.Builder
	builder.Grow(limit)
	used := 0
	for _, r := range text {
		size := len(string(r))
		if used+size > limit {
			break
		}
		builder.WriteRune(r)
		used += size
	}
	return builder.String(), true
}

func redactSourceContentFragments(text string) string {
	text = strings.TrimSpace(text)
	for _, pattern := range sourceContentRedactionPatterns {
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	return text
}

func safeToolSummary(input map[string]any) map[string]any {
	return sanitizeMap(input)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func toolDefinition(name, description string, inputSchema map[string]any) MCPToolDefinition {
	return MCPToolDefinition{Name: name, Description: description, InputSchema: inputSchema}
}

func jobToolSchema(requireSection bool) map[string]any {
	required := []any{"reportId"}
	properties := map[string]any{
		"reportId":     map[string]any{"type": "string", "description": "Report business ID."},
		"requirements": map[string]any{"type": "string", "description": "Optional generation requirements. Stored only as a length summary in logs."},
		"materialIds": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Optional material business IDs.",
		},
		"options":   map[string]any{"type": "object", "additionalProperties": true},
		"retrieval": map[string]any{"type": "object", "additionalProperties": true},
	}
	if requireSection {
		required = append(required, "sectionId")
		properties["sectionId"] = map[string]any{"type": "string", "description": "Report section business ID."}
	}
	return objectSchema(required, properties)
}

func requiredStringSchema(name, description string) map[string]any {
	return objectSchema([]any{name}, map[string]any{
		name: map[string]any{"type": "string", "description": description},
	})
}

func exportDOCXSchema() map[string]any {
	return objectSchema([]any{"reportId"}, map[string]any{
		"reportId":      map[string]any{"type": "string", "description": "Report business ID."},
		"templateId":    map[string]any{"type": "string", "description": "Optional report template business ID."},
		"format":        map[string]any{"type": "string", "enum": []any{ReportFileFormatDOCX}, "default": ReportFileFormatDOCX},
		"exportOptions": map[string]any{"type": "object", "additionalProperties": true},
	})
}

func reportFromContentSchema() map[string]any {
	return objectSchema([]any{"content"}, map[string]any{
		"content":       map[string]any{"type": "string", "description": "Source content used to create a bounded report-generation context. Only a UTF-8 safe excerpt is retained for asynchronous generation.", "x-max-source-content-bytes": documentMCPMaxSourceContentBytes},
		"document_name": map[string]any{"type": "string", "description": "Optional display name for the generated report."},
		"instructions":  map[string]any{"type": "string", "description": "Optional generation instructions stored only as length summary in logs."},
	})
}

func listReportsSchema() map[string]any {
	return objectSchema(nil, map[string]any{
		"reportType": map[string]any{"type": "string", "description": "Optional report type code filter."},
		"status":     map[string]any{"type": "string", "enum": []any{string(ReportStatusDraft), string(ReportStatusOutlineGenerating), string(ReportStatusOutlineGenerated), string(ReportStatusContentGenerating), string(ReportStatusGenerated), string(ReportStatusExporting), string(ReportStatusExported), string(ReportStatusFailed)}},
		"page":       map[string]any{"type": "integer", "minimum": 1, "default": DefaultPage},
		"pageSize":   map[string]any{"type": "integer", "minimum": 1, "maximum": MaxPageSize, "default": DefaultPageSize},
	})
}

func readReportFileSchema() map[string]any {
	return objectSchema([]any{"reportFileId"}, map[string]any{
		"reportFileId": map[string]any{"type": "string", "description": "Report file business ID."},
		"format":       map[string]any{"type": "string", "enum": []any{"text", "markdown"}, "default": "text"},
	})
}

func objectSchema(required []any, properties map[string]any) map[string]any {
	if required == nil {
		required = []any{}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

var sourceContentRedactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(token|api[_-]?key|secret|password|authorization|prompt)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)\bbearer\s+\S+`),
	regexp.MustCompile(`(?i)\b(?:https?|s3)://\S+`),
}
