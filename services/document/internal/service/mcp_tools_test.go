package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMCPToolServiceListToolsDefinesStableSchemas(t *testing.T) {
	svc := NewMCPToolService(MCPToolServiceConfig{})

	tools := svc.ListTools(context.Background())
	want := []string{
		DocumentMCPToolGenerateReportOutline,
		DocumentMCPToolGenerateReportFromContent,
		DocumentMCPToolRegenerateReportOutline,
		DocumentMCPToolGenerateReportText,
		DocumentMCPToolRegenerateReportText,
		DocumentMCPToolRegenerateReportSection,
		DocumentMCPToolGetGenerationStatus,
		DocumentMCPToolGetTemplateSchema,
		DocumentMCPToolExportReportDOCX,
		DocumentMCPToolGetReportResult,
		DocumentMCPToolListReports,
		DocumentMCPToolGetReport,
		DocumentMCPToolListMaterials,
		DocumentMCPToolGetMaterial,
		DocumentMCPToolListReportFiles,
		DocumentMCPToolReadReportFile,
	}
	if len(tools) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(tools), len(want))
	}
	seen := map[string]MCPToolDefinition{}
	for _, tool := range tools {
		if tool.Name == "" || tool.Description == "" {
			t.Fatalf("tool has empty name or description: %+v", tool)
		}
		if tool.InputSchema["type"] != "object" || tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("tool %s schema = %+v, want strict object", tool.Name, tool.InputSchema)
		}
		seen[tool.Name] = tool
	}
	for _, name := range want {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing tool %q from registry", name)
		}
	}
	assertSchemaRequires(t, seen[DocumentMCPToolGenerateReportOutline].InputSchema, "reportId")
	assertSchemaRequires(t, seen[DocumentMCPToolGenerateReportFromContent].InputSchema, "content")
	contentProperties := seen[DocumentMCPToolGenerateReportFromContent].InputSchema["properties"].(map[string]any)
	contentSchema := contentProperties["content"].(map[string]any)
	if contentSchema["x-max-source-content-bytes"] != documentMCPMaxSourceContentBytes {
		t.Fatalf("content schema = %+v, want source content byte limit", contentSchema)
	}
	assertSchemaRequires(t, seen[DocumentMCPToolRegenerateReportSection].InputSchema, "reportId", "sectionId")
	assertSchemaRequires(t, seen[DocumentMCPToolGetGenerationStatus].InputSchema, "jobId")
	assertSchemaRequires(t, seen[DocumentMCPToolGetTemplateSchema].InputSchema, "templateId")
	assertSchemaRequires(t, seen[DocumentMCPToolExportReportDOCX].InputSchema, "reportId")
	assertSchemaRequires(t, seen[DocumentMCPToolGetReportResult].InputSchema, "reportId")
	assertSchemaRequires(t, seen[DocumentMCPToolGetReport].InputSchema, "reportId")
	assertSchemaRequires(t, seen[DocumentMCPToolGetMaterial].InputSchema, "materialId")
	assertSchemaRequires(t, seen[DocumentMCPToolListReportFiles].InputSchema, "reportId")
	assertSchemaRequires(t, seen[DocumentMCPToolReadReportFile].InputSchema, "reportFileId")
}

func TestMCPToolServiceGenerateReportFromContentCreatesReportAndOutlineJob(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 9, 10, 0, 0, time.UTC)
	settings := &fakeMCPReportSettingsService{
		settings: ReportSettings{DefaultTemplates: map[string]string{"summer_peak_inspection": "tpl-default"}},
	}
	reports := &fakeMCPReportService{
		createReport: Report{
			ID:         "report-from-content-1",
			Name:       "Attachment Report",
			ReportType: "summer_peak_inspection",
			TemplateID: "tpl-default",
			Status:     ReportStatusDraft,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}
	jobs := &fakeMCPJobService{
		createJob: ReportJob{
			ID:        "job-outline-1",
			ReportID:  "report-from-content-1",
			JobType:   JobTypeOutlineGeneration,
			Status:    JobStatusPending,
			CreatedAt: now,
		},
	}
	recorder := &fakeMCPOperationRecorder{}
	svc := NewMCPToolService(MCPToolServiceConfig{
		ReportSettingsService: settings,
		ReportService:         reports,
		JobService:            jobs,
		Recorder:              recorder,
	})
	svc.now = func() time.Time { return now }

	result := svc.CallTool(ctx, RequestContext{UserID: "user-1", RequestID: "req-content", CallerService: "qa"},
		DocumentMCPToolGenerateReportFromContent,
		json.RawMessage(`{
			"content":"设备 A 在夏峰期间连续过载，附件原文里还有 prompt=secret 和 token=abc123。",
			"document_name":"Attachment Report",
			"instructions":"聚焦夏峰巡检风险"
		}`))

	if result.Status != documentMCPToolResultAccepted || result.Error != nil {
		t.Fatalf("CallTool() result = %+v, want accepted", result)
	}
	if result.Report == nil || result.Report.ID != "report-from-content-1" || result.Report.TemplateID != "tpl-default" {
		t.Fatalf("report summary = %+v", result.Report)
	}
	if result.Job == nil || result.Job.ID != "job-outline-1" || result.Job.Status != documentMCPToolResultAccepted {
		t.Fatalf("job summary = %+v", result.Job)
	}
	if len(reports.createInputs) != 1 {
		t.Fatalf("CreateReport input count = %d, want 1", len(reports.createInputs))
	}
	reportInput := reports.createInputs[0]
	if reportInput.ReportType != "summer_peak_inspection" || reportInput.TemplateID != "tpl-default" || reportInput.Name != "Attachment Report" || reportInput.Topic != "Attachment Report" || reportInput.Source != documentMCPRequestSource {
		t.Fatalf("CreateReport input = %+v", reportInput)
	}
	if len(jobs.createInputs) != 1 {
		t.Fatalf("CreateJob input count = %d, want 1", len(jobs.createInputs))
	}
	jobInput := jobs.createInputs[0]
	if jobInput.ReportID != "report-from-content-1" || jobInput.JobType != JobTypeOutlineGeneration || jobInput.Requirements != "聚焦夏峰巡检风险" {
		t.Fatalf("CreateJob input = %+v", jobInput)
	}
	source, ok := jobInput.Options["sourceContent"].(map[string]any)
	if !ok {
		t.Fatalf("sourceContent option = %#v, want map", jobInput.Options["sourceContent"])
	}
	if source["excerpt"] == "" || source["originalLength"] == 0 || source["truncated"] != false || source["documentName"] != "Attachment Report" {
		t.Fatalf("sourceContent = %+v", source)
	}
	rawPayload, err := json.Marshal(jobInput.Options)
	if err != nil {
		t.Fatalf("marshal job options: %v", err)
	}
	if strings.Contains(string(rawPayload), "token=abc123") || strings.Contains(string(rawPayload), "prompt=secret") {
		t.Fatalf("job options leaked sensitive raw content: %s", rawPayload)
	}
	log := recorder.lastLog(t)
	if log.ToolName != DocumentMCPToolGenerateReportFromContent || log.TargetType != "job" || log.TargetID != "job-outline-1" {
		t.Fatalf("operation log = %+v", log)
	}
	summary := log.ParameterSummary
	if _, ok := summary["content"]; ok {
		t.Fatalf("operation log leaked content: %+v", summary)
	}
	contentLength, ok := summary["contentLength"]
	if !ok || contentLength == 0 {
		t.Fatalf("operation log summary missing contentLength: %+v", summary)
	}
	if summary["excerptLength"] == 0 || summary["documentNameProvided"] != true || summary["instructionsLength"] == 0 {
		t.Fatalf("operation log summary = %+v", summary)
	}
}

func TestMCPToolServiceGenerateReportFromContentRejectsEmptyContent(t *testing.T) {
	reports := &fakeMCPReportService{}
	jobs := &fakeMCPJobService{}
	result := NewMCPToolService(MCPToolServiceConfig{
		ReportSettingsService: &fakeMCPReportSettingsService{settings: ReportSettings{DefaultTemplates: map[string]string{"summer_peak_inspection": "tpl-default"}}},
		ReportService:         reports,
		JobService:            jobs,
		Recorder:              &fakeMCPOperationRecorder{},
	}).CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-empty"},
		DocumentMCPToolGenerateReportFromContent, json.RawMessage(`{"content":"   "}`))

	if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != string(CodeValidation) {
		t.Fatalf("CallTool() result = %+v, want validation failure", result)
	}
	if result.Error.Fields["content"] == "" {
		t.Fatalf("validation fields = %+v, want content error", result.Error.Fields)
	}
	if len(reports.createInputs) != 0 || len(jobs.createInputs) != 0 {
		t.Fatalf("empty content should not create report/job, reports=%+v jobs=%+v", reports.createInputs, jobs.createInputs)
	}
}

func TestMCPToolServiceGenerateReportFromContentRequiresDefaultTemplate(t *testing.T) {
	reports := &fakeMCPReportService{}
	jobs := &fakeMCPJobService{}
	result := NewMCPToolService(MCPToolServiceConfig{
		ReportSettingsService: &fakeMCPReportSettingsService{settings: ReportSettings{DefaultTemplates: map[string]string{}}},
		ReportService:         reports,
		JobService:            jobs,
		Recorder:              &fakeMCPOperationRecorder{},
	}).CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-template"},
		DocumentMCPToolGenerateReportFromContent, json.RawMessage(`{"content":"夏峰巡检记录"}`))

	if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != string(CodeValidation) {
		t.Fatalf("CallTool() result = %+v, want validation failure", result)
	}
	if result.Error.Fields["defaultTemplates.summer_peak_inspection"] == "" {
		t.Fatalf("validation fields = %+v, want default template error", result.Error.Fields)
	}
	if len(reports.createInputs) != 0 || len(jobs.createInputs) != 0 {
		t.Fatalf("missing default template should not create report/job, reports=%+v jobs=%+v", reports.createInputs, jobs.createInputs)
	}
}

func TestMCPToolServiceGenerateReportFromContentTruncatesLongContent(t *testing.T) {
	reports := &fakeMCPReportService{createReport: Report{ID: "report-1", Name: "Long", ReportType: "summer_peak_inspection", TemplateID: "tpl-default", Status: ReportStatusDraft}}
	jobs := &fakeMCPJobService{createJob: ReportJob{ID: "job-1", ReportID: "report-1", JobType: JobTypeOutlineGeneration, Status: JobStatusPending}}
	recorder := &fakeMCPOperationRecorder{}
	longContent := strings.Repeat("负荷异常", 8000)
	rawArgs, err := json.Marshal(map[string]any{"content": longContent, "document_name": "Long"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result := NewMCPToolService(MCPToolServiceConfig{
		ReportSettingsService: &fakeMCPReportSettingsService{settings: ReportSettings{DefaultTemplates: map[string]string{"summer_peak_inspection": "tpl-default"}}},
		ReportService:         reports,
		JobService:            jobs,
		Recorder:              recorder,
	}).CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-long"},
		DocumentMCPToolGenerateReportFromContent, rawArgs)

	if result.Status != documentMCPToolResultAccepted || len(result.Warnings) != 1 || result.Warnings[0] != "content_truncated" {
		t.Fatalf("CallTool() result = %+v, want accepted with content_truncated warning", result)
	}
	source := jobs.createInputs[0].Options["sourceContent"].(map[string]any)
	if source["truncated"] != true || source["originalLength"].(int) <= documentMCPMaxSourceContentBytes {
		t.Fatalf("sourceContent metadata = %+v", source)
	}
	if len([]byte(source["excerpt"].(string))) > documentMCPMaxSourceContentBytes {
		t.Fatalf("source excerpt length = %d, want <= %d", len([]byte(source["excerpt"].(string))), documentMCPMaxSourceContentBytes)
	}
	if recorder.lastLog(t).ParameterSummary["truncated"] != true {
		t.Fatalf("operation log summary = %+v, want truncated", recorder.lastLog(t).ParameterSummary)
	}
}

func TestMCPToolServiceGenerateReportFromContentDoesNotAcceptWhenJobCreationFails(t *testing.T) {
	reports := &fakeMCPReportService{createReport: Report{ID: "report-1", Name: "Report", ReportType: "summer_peak_inspection", TemplateID: "tpl-default", Status: ReportStatusDraft}}
	jobs := &fakeMCPJobService{createErr: NewError(CodeDependency, "queue unavailable", nil)}
	result := NewMCPToolService(MCPToolServiceConfig{
		ReportSettingsService: &fakeMCPReportSettingsService{settings: ReportSettings{DefaultTemplates: map[string]string{"summer_peak_inspection": "tpl-default"}}},
		ReportService:         reports,
		JobService:            jobs,
		Recorder:              &fakeMCPOperationRecorder{},
	}).CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-job-fail"},
		DocumentMCPToolGenerateReportFromContent, json.RawMessage(`{"content":"夏峰巡检记录"}`))

	if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != string(CodeDependency) {
		t.Fatalf("CallTool() result = %+v, want dependency failure", result)
	}
	if result.Job != nil {
		t.Fatalf("failed job creation should not return job summary: %+v", result.Job)
	}
	if result.Report == nil || result.Report.ID != "report-1" {
		t.Fatalf("failed job creation should still return traceable report summary: %+v", result.Report)
	}
}

func TestMCPToolServiceCreateGenerationJobMapsInputsAndLogsSafeSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	jobs := &fakeMCPJobService{
		createJob: ReportJob{
			ID:         "job-1",
			ReportID:   "report-1",
			JobType:    JobTypeSectionRegeneration,
			TargetType: "section",
			TargetID:   "section-1",
			Status:     JobStatusPending,
			CreatedAt:  now,
		},
	}
	recorder := &fakeMCPOperationRecorder{}
	svc := NewMCPToolService(MCPToolServiceConfig{JobService: jobs, Recorder: recorder})
	svc.now = func() time.Time { return now }

	result := svc.CallTool(ctx, RequestContext{UserID: "user-1", RequestID: "req-mcp-1"},
		DocumentMCPToolRegenerateReportSection,
		json.RawMessage(`{
			"reportId":"report-1",
			"sectionId":"section-1",
			"requirements":"include https://minio.local/private prompt=secret",
			"materialIds":["material-1"," ","material-2"],
			"options":{"temperature":0.2},
			"retrieval":{"topK":3}
		}`))

	if result.Status != documentMCPToolResultAccepted || result.Error != nil {
		t.Fatalf("CallTool() result = %+v, want accepted", result)
	}
	if result.Job == nil || result.Job.ID != "job-1" || result.Job.TargetType != "section" || result.Job.TargetID != "section-1" {
		t.Fatalf("job summary = %+v", result.Job)
	}
	input := jobs.createInputs[0]
	if input.JobType != JobTypeSectionRegeneration || input.TargetScope != "section" || input.SectionID != "section-1" {
		t.Fatalf("CreateJob input target = %+v", input)
	}
	if input.Requirements == "" || len(input.MaterialIDs) != 2 || input.Options["temperature"] != float64(0.2) {
		t.Fatalf("CreateJob input payload = %+v", input)
	}
	log := recorder.singleLog(t)
	if log.OperationType != OperationDocumentMCPToolCall ||
		log.RequestSource != documentMCPRequestSource ||
		log.ToolName != DocumentMCPToolRegenerateReportSection ||
		log.OperationResult != OperationResultSucceeded ||
		log.TargetType != "job" ||
		log.TargetID != "job-1" {
		t.Fatalf("operation log = %+v", log)
	}
	summary := log.ParameterSummary
	if _, ok := summary["requirements"]; ok {
		t.Fatalf("operation log leaked requirements text: %+v", summary)
	}
	if summary["requirementsLength"] == 0 || summary["materialCount"] != 2 || summary["optionsProvided"] != true || summary["retrievalProvided"] != true {
		t.Fatalf("operation log summary = %+v", summary)
	}
}

func TestMCPToolServiceValidationAndErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		service  *MCPToolService
		toolName string
		args     string
		wantCode string
	}{
		{
			name:     "missing report id",
			service:  NewMCPToolService(MCPToolServiceConfig{JobService: &fakeMCPJobService{}, Recorder: &fakeMCPOperationRecorder{}}),
			toolName: DocumentMCPToolGenerateReportOutline,
			args:     `{}`,
			wantCode: string(CodeValidation),
		},
		{
			name:     "invalid arguments shape",
			service:  NewMCPToolService(MCPToolServiceConfig{Recorder: &fakeMCPOperationRecorder{}}),
			toolName: DocumentMCPToolGetReportResult,
			args:     `[]`,
			wantCode: string(CodeValidation),
		},
		{
			name:     "unknown tool",
			service:  NewMCPToolService(MCPToolServiceConfig{Recorder: &fakeMCPOperationRecorder{}}),
			toolName: "delete_everything",
			args:     `{}`,
			wantCode: documentMCPErrorUnsupported,
		},
		{
			name: "forbidden job status",
			service: NewMCPToolService(MCPToolServiceConfig{
				JobService: &fakeMCPJobService{getErr: NewError(CodeForbidden, "report access denied", nil)},
				Recorder:   &fakeMCPOperationRecorder{},
			}),
			toolName: DocumentMCPToolGetGenerationStatus,
			args:     `{"jobId":"job-1"}`,
			wantCode: string(CodeForbidden),
		},
		{
			name: "dependency error",
			service: NewMCPToolService(MCPToolServiceConfig{
				JobService: &fakeMCPJobService{getErr: errors.New("postgres unavailable")},
				Recorder:   &fakeMCPOperationRecorder{},
			}),
			toolName: DocumentMCPToolGetGenerationStatus,
			args:     `{"jobId":"job-1"}`,
			wantCode: string(CodeInternal),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.service.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-error"}, tt.toolName, json.RawMessage(tt.args))
			if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != tt.wantCode {
				t.Fatalf("CallTool() result = %+v, want failed %s", result, tt.wantCode)
			}
		})
	}
}

func TestMCPToolServiceRejectsInvalidGenerationArgumentTypes(t *testing.T) {
	tests := []struct {
		name      string
		args      string
		wantField string
	}{
		{
			name:      "material ids must be string array",
			args:      `{"reportId":"report-1","materialIds":["material-1",3]}`,
			wantField: "materialIds",
		},
		{
			name:      "options must be object",
			args:      `{"reportId":"report-1","options":["not-object"]}`,
			wantField: "options",
		},
		{
			name:      "retrieval must be object",
			args:      `{"reportId":"report-1","retrieval":"not-object"}`,
			wantField: "retrieval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := &fakeMCPJobService{}
			result := NewMCPToolService(MCPToolServiceConfig{
				JobService: jobs,
				Recorder:   &fakeMCPOperationRecorder{},
			}).CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-invalid-type"},
				DocumentMCPToolGenerateReportOutline, json.RawMessage(tt.args))

			if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != string(CodeValidation) {
				t.Fatalf("CallTool() result = %+v, want validation failure", result)
			}
			if result.Error.Fields[tt.wantField] == "" {
				t.Fatalf("validation fields = %+v, missing %q", result.Error.Fields, tt.wantField)
			}
			if len(jobs.createInputs) != 0 {
				t.Fatalf("invalid arguments should not call CreateJob, inputs=%+v", jobs.createInputs)
			}
		})
	}
}

func TestMCPToolServiceGetTemplateSchemaReturnsSafeStructure(t *testing.T) {
	svc := NewMCPToolService(MCPToolServiceConfig{
		DocumentService: &fakeMCPDocumentService{
			structure: ReportTemplateStructure{
				OutlineSchema: json.RawMessage(`{"type":"object","properties":{"sections":{"type":"array"}}}`),
				StyleConfig:   json.RawMessage(`{"numbering":"global"}`),
			},
		},
		Recorder: &fakeMCPOperationRecorder{},
	})

	result := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-template"},
		DocumentMCPToolGetTemplateSchema, json.RawMessage(`{"templateId":"tpl-1"}`))

	if result.Status != documentMCPToolResultSucceeded || result.TemplateSchema == nil {
		t.Fatalf("CallTool() result = %+v, want template schema", result)
	}
	if result.TemplateSchema.TemplateID != "tpl-1" || !strings.Contains(string(result.TemplateSchema.OutlineSchema), "sections") {
		t.Fatalf("template schema = %+v", result.TemplateSchema)
	}
}

func TestMCPToolServiceGetGenerationStatusSanitizesProgress(t *testing.T) {
	svc := NewMCPToolService(MCPToolServiceConfig{
		JobService: &fakeMCPJobService{getJob: ReportJob{
			ID:       "job-1",
			ReportID: "report-1",
			JobType:  JobTypeContentGeneration,
			Status:   JobStatusRunning,
			Progress: map[string]any{
				"percent": 40,
				"prompt":  "raw prompt must not survive",
				"detail":  "https://minio.local/object",
			},
		}},
		Recorder: &fakeMCPOperationRecorder{},
	})

	result := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-status"},
		DocumentMCPToolGetGenerationStatus, json.RawMessage(`{"jobId":"job-1"}`))

	if result.Status != documentMCPToolResultSucceeded || result.Job == nil {
		t.Fatalf("CallTool() result = %+v, want job status", result)
	}
	if _, ok := result.Job.Progress["prompt"]; ok {
		t.Fatalf("job progress leaked prompt: %+v", result.Job.Progress)
	}
	if got := result.Job.Progress["detail"]; got != "[redacted]" {
		t.Fatalf("job progress detail = %v, want redacted", got)
	}
}

func TestMCPToolServiceExportReportDOCXUsesBasicExporterAndHidesFileRef(t *testing.T) {
	files := &fakeMCPReportFileService{
		createFile: ReportFile{
			ID:        "rf-1",
			ReportID:  "report-1",
			JobID:     "job-file-1",
			Filename:  "Report.docx",
			Format:    ReportFileFormatDOCX,
			FileRef:   "file_ref_internal_secret",
			FileSize:  1024,
			Status:    ReportFileStatusPending,
			CreatedAt: time.Date(2026, 7, 1, 9, 5, 0, 0, time.UTC),
		},
	}
	recorder := &fakeMCPOperationRecorder{}
	svc := NewMCPToolService(MCPToolServiceConfig{ReportFileSvc: files, Recorder: recorder})

	result := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-export"},
		DocumentMCPToolExportReportDOCX,
		json.RawMessage(`{"reportId":"report-1","templateId":"tpl-1","format":"docx","exportOptions":{"numbering":"global"}}`))

	if result.Status != documentMCPToolResultAccepted || result.ReportFile == nil {
		t.Fatalf("CallTool() result = %+v, want accepted report file", result)
	}
	if files.createInputs[0].Format != ReportFileFormatDOCX || string(files.createInputs[0].StyleOptions) == "" {
		t.Fatalf("CreateReportFile input = %+v", files.createInputs[0])
	}
	if result.ReportFile.ContentPath != "/api/v1/report-files/rf-1/content" {
		t.Fatalf("content path = %q", result.ReportFile.ContentPath)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(raw), "file_ref") || strings.Contains(string(raw), "internal_secret") {
		t.Fatalf("tool result leaked File internal reference: %s", raw)
	}
	log := recorder.singleLog(t)
	if log.ParameterSummary["basicDocxExporter"] != true || log.ParameterSummary["richDocxRequested"] != false {
		t.Fatalf("export summary = %+v", log.ParameterSummary)
	}
}

func TestMCPToolServiceExportReportDOCXRejectsUnsupportedFormat(t *testing.T) {
	files := &fakeMCPReportFileService{}
	svc := NewMCPToolService(MCPToolServiceConfig{ReportFileSvc: files, Recorder: &fakeMCPOperationRecorder{}})

	result := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-export"},
		DocumentMCPToolExportReportDOCX, json.RawMessage(`{"reportId":"report-1","format":"pdf"}`))

	if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != string(CodeValidation) {
		t.Fatalf("CallTool() result = %+v, want validation failure", result)
	}
	if len(files.createInputs) != 0 {
		t.Fatalf("unsupported export should not call service, inputs=%+v", files.createInputs)
	}
}

func TestMCPToolServiceExportAndResultErrorsUseStableCodes(t *testing.T) {
	tests := []struct {
		name     string
		service  *MCPToolService
		toolName string
		args     string
		wantCode Code
	}{
		{
			name: "export conflict",
			service: NewMCPToolService(MCPToolServiceConfig{
				ReportFileSvc: &fakeMCPReportFileService{createErr: NewError(CodeConflict, "report file is not ready", nil)},
				Recorder:      &fakeMCPOperationRecorder{},
			}),
			toolName: DocumentMCPToolExportReportDOCX,
			args:     `{"reportId":"report-1"}`,
			wantCode: CodeConflict,
		},
		{
			name: "export dependency",
			service: NewMCPToolService(MCPToolServiceConfig{
				ReportFileSvc: &fakeMCPReportFileService{createErr: NewError(CodeDependency, "redis unavailable", nil)},
				Recorder:      &fakeMCPOperationRecorder{},
			}),
			toolName: DocumentMCPToolExportReportDOCX,
			args:     `{"reportId":"report-1"}`,
			wantCode: CodeDependency,
		},
		{
			name: "result latest file dependency",
			service: NewMCPToolService(MCPToolServiceConfig{
				ReportService: &fakeMCPReportService{report: Report{
					ID:                 "report-1",
					LatestReportFileID: "rf-1",
					Status:             ReportStatusGenerated,
				}},
				ReportFileSvc: &fakeMCPReportFileService{getErr: NewError(CodeDependency, "file service unavailable", nil)},
				Recorder:      &fakeMCPOperationRecorder{},
			}),
			toolName: DocumentMCPToolGetReportResult,
			args:     `{"reportId":"report-1"}`,
			wantCode: CodeDependency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.service.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-stable-error"}, tt.toolName, json.RawMessage(tt.args))
			if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != string(tt.wantCode) {
				t.Fatalf("CallTool() result = %+v, want failed %s", result, tt.wantCode)
			}
		})
	}
}

func TestMCPToolServiceGetReportResultIncludesSafeLatestFile(t *testing.T) {
	generatedAt := time.Date(2026, 7, 1, 8, 30, 0, 0, time.UTC)
	svc := NewMCPToolService(MCPToolServiceConfig{
		ReportService: &fakeMCPReportService{report: Report{
			ID:                 "report-1",
			Name:               "Inspection",
			ReportType:         "summer_peak_inspection",
			TemplateID:         "tpl-1",
			Status:             ReportStatusGenerated,
			LatestJobID:        "job-1",
			LatestReportFileID: "rf-1",
			GeneratedAt:        &generatedAt,
			UpdatedAt:          generatedAt,
		}},
		ReportFileSvc: &fakeMCPReportFileService{getFile: ReportFile{
			ID:       "rf-1",
			ReportID: "report-1",
			JobID:    "job-file-1",
			Filename: "Inspection.docx",
			Format:   ReportFileFormatDOCX,
			FileRef:  "file_ref_hidden",
			Status:   ReportFileStatusSucceeded,
		}},
		Recorder: &fakeMCPOperationRecorder{},
	})

	result := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-result"},
		DocumentMCPToolGetReportResult, json.RawMessage(`{"reportId":"report-1"}`))

	if result.Status != documentMCPToolResultSucceeded || result.Report == nil || result.ReportFile == nil {
		t.Fatalf("CallTool() result = %+v, want report and latest file", result)
	}
	if result.Report.ID != "report-1" || result.Report.LatestReportFileID != "rf-1" {
		t.Fatalf("report summary = %+v", result.Report)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(raw), "file_ref_hidden") {
		t.Fatalf("report result leaked File internal reference: %s", raw)
	}
}

func TestMCPToolServiceListAndGetReportsReturnSafeMetadata(t *testing.T) {
	createdAt := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
	reports := &fakeMCPReportService{
		list: ReportListResult{
			Items: []Report{{
				ID: "report-1", Name: "Inspection", ReportType: "summer_peak_inspection",
				TemplateID: "tpl-1", Topic: "夏峰", Status: ReportStatusGenerated,
				CreatorID: "user-1", CreatedAt: createdAt, UpdatedAt: createdAt,
			}},
			Page: PageMeta{Page: 2, PageSize: 10, Total: 11},
		},
		report: Report{
			ID: "report-1", Name: "Inspection", ReportType: "summer_peak_inspection",
			TemplateID: "tpl-1", Topic: "夏峰", Specialty: "电气", BusinessObject: "变电站",
			Year: 2026, Status: ReportStatusGenerated, CreatorID: "user-1", CreatedAt: createdAt, UpdatedAt: createdAt,
		},
	}
	svc := NewMCPToolService(MCPToolServiceConfig{ReportService: reports, Recorder: &fakeMCPOperationRecorder{}})

	list := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-list"},
		DocumentMCPToolListReports, json.RawMessage(`{"reportType":"summer_peak_inspection","status":"generated","page":2,"pageSize":10}`))
	if list.Status != documentMCPToolResultSucceeded || len(list.Reports) != 1 || list.TotalCount != 11 || list.Page != 2 || list.PageSize != 10 {
		t.Fatalf("list result = %+v", list)
	}
	if got := reports.listFilters[0]; got.ReportType != "summer_peak_inspection" || got.Status != "generated" || got.Page != 2 || got.PageSize != 10 {
		t.Fatalf("list filter = %+v", got)
	}

	get := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-get"},
		DocumentMCPToolGetReport, json.RawMessage(`{"reportId":"report-1"}`))
	if get.Status != documentMCPToolResultSucceeded || get.Report == nil || get.Report.Topic != "夏峰" || get.Report.Year != 2026 {
		t.Fatalf("get result = %+v", get)
	}
	raw, err := json.Marshal(get)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "CreatorID") || strings.Contains(string(raw), "file_ref") {
		t.Fatalf("report metadata leaked internal fields: %s", raw)
	}
}

func TestMCPToolServiceListReportsRejectsOverflowPage(t *testing.T) {
	reports := &fakeMCPReportService{}
	svc := NewMCPToolService(MCPToolServiceConfig{ReportService: reports, Recorder: &fakeMCPOperationRecorder{}})

	result := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-page"},
		DocumentMCPToolListReports, json.RawMessage(`{"page":2147483648}`))

	if result.Status != documentMCPToolResultFailed || result.Error == nil || result.Error.Code != string(CodeValidation) {
		t.Fatalf("overflow page result = %+v, want validation failure", result)
	}
	if len(reports.listFilters) != 0 {
		t.Fatalf("ListReports was called with invalid pagination: %+v", reports.listFilters)
	}
}

func TestMCPToolServiceListAndGetMaterialsHideFileRef(t *testing.T) {
	createdAt := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
	documents := &fakeMCPDocumentService{
		materials: ReportMaterialListResult{
			Items: []ReportMaterial{{
				ID: "mat-1", MaterialName: "Load data", MaterialType: "spreadsheet",
				Category: "load", FileRef: "file_ref_hidden", Filename: "load.xlsx",
				FileSize: 512, Tags: []string{"daily"}, Enabled: true, CreatedAt: createdAt, UpdatedAt: createdAt,
			}},
			Page: PageMeta{Page: 1, PageSize: 20, Total: 1},
		},
		material: ReportMaterial{
			ID: "mat-1", MaterialName: "Load data", MaterialType: "spreadsheet",
			Category: "load", FileRef: "file_ref_hidden", Filename: "load.xlsx",
			FileSize: 512, Description: "daily load", Tags: []string{"daily"}, Enabled: true,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		},
	}
	svc := NewMCPToolService(MCPToolServiceConfig{DocumentService: documents, Recorder: &fakeMCPOperationRecorder{}})

	list := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-materials"},
		DocumentMCPToolListMaterials, json.RawMessage(`{"category":"load"}`))
	if list.Status != documentMCPToolResultSucceeded || len(list.Materials) != 1 || list.Materials[0].ID != "mat-1" {
		t.Fatalf("list materials result = %+v", list)
	}
	get := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-material"},
		DocumentMCPToolGetMaterial, json.RawMessage(`{"materialId":"mat-1"}`))
	if get.Status != documentMCPToolResultSucceeded || get.Material == nil || get.Material.MaterialName != "Load data" {
		t.Fatalf("get material result = %+v", get)
	}
	raw, err := json.Marshal([]MCPToolCallResult{list, get})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "file_ref_hidden") {
		t.Fatalf("material tool result leaked file ref: %s", raw)
	}
}

func TestMCPToolServiceListReportFilesAndReadDOCXText(t *testing.T) {
	docx, err := NewSimpleDOCXGenerator().GenerateDOCX(context.Background(), Report{Name: "Inspection", Topic: "夏峰"}, []ReportSection{{
		Title: "Summary", Content: "all clear token=secret", SortOrder: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	files := &fakeMCPReportFileService{
		list: ReportFileListResult{
			Items: []ReportFile{{
				ID: "rf-1", ReportID: "report-1", JobID: "job-1", Filename: "Inspection.docx",
				Format: ReportFileFormatDOCX, FileRef: "file_ref_hidden", FileSize: int64(len(docx)),
				Status: ReportFileStatusSucceeded, CreatedAt: time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC),
			}},
			Page: PageMeta{Page: 1, PageSize: 100, Total: 1},
		},
		readContent: FileContent{
			Filename: "Inspection.docx", ContentType: docxContentType, SizeBytes: int64(len(docx)),
			Content: io.NopCloser(strings.NewReader(string(docx))),
		},
	}
	svc := NewMCPToolService(MCPToolServiceConfig{ReportFileSvc: files, Recorder: &fakeMCPOperationRecorder{}})

	list := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-files"},
		DocumentMCPToolListReportFiles, json.RawMessage(`{"reportId":"report-1"}`))
	if list.Status != documentMCPToolResultSucceeded || len(list.ReportFiles) != 1 || list.ReportFiles[0].ContentPath == "" {
		t.Fatalf("list report files result = %+v", list)
	}
	if files.listFilters[0].ReportID != "report-1" {
		t.Fatalf("list report files filter = %+v", files.listFilters[0])
	}

	read := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-read"},
		DocumentMCPToolReadReportFile, json.RawMessage(`{"reportFileId":"rf-1","format":"text"}`))
	if read.Status != documentMCPToolResultSucceeded || read.ReportFileContent == nil {
		t.Fatalf("read report file result = %+v", read)
	}
	if !strings.Contains(read.ReportFileContent.Content, "Inspection") || !strings.Contains(read.ReportFileContent.Content, "Summary") {
		t.Fatalf("extracted content = %q", read.ReportFileContent.Content)
	}
	raw, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "file_ref_hidden") || strings.Contains(string(raw), "token=secret") || strings.Contains(string(raw), "PK\x03\x04") {
		t.Fatalf("read report file leaked internal/binary/sensitive data: %s", raw)
	}
}

func TestMCPToolServiceReadLargeDOCXExtractsTextBeforeContentCap(t *testing.T) {
	docx := largeDOCXWithText(t, "large report summary")
	if len(docx) <= documentMCPMaxReportFileContentBytes {
		t.Fatalf("test DOCX size=%d, want larger than content cap", len(docx))
	}
	files := &fakeMCPReportFileService{
		readContent: FileContent{
			Filename: "large.docx", ContentType: docxContentType, SizeBytes: int64(len(docx)),
			Content: io.NopCloser(bytes.NewReader(docx)),
		},
	}
	svc := NewMCPToolService(MCPToolServiceConfig{ReportFileSvc: files, Recorder: &fakeMCPOperationRecorder{}})

	result := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-large-docx"},
		DocumentMCPToolReadReportFile, json.RawMessage(`{"reportFileId":"rf-large","format":"text"}`))

	if result.Status != documentMCPToolResultSucceeded || result.ReportFileContent == nil {
		t.Fatalf("read large DOCX result = %+v, want success", result)
	}
	if !strings.Contains(result.ReportFileContent.Content, "large report summary") {
		t.Fatalf("extracted content = %q, want DOCX text", result.ReportFileContent.Content)
	}
	if result.ReportFileContent.Truncated {
		t.Fatalf("result marked truncated for small extracted text: %+v", result.ReportFileContent)
	}
}

func TestMCPToolServiceReadReportFileValidationAndUnsupportedContent(t *testing.T) {
	svc := NewMCPToolService(MCPToolServiceConfig{
		ReportFileSvc: &fakeMCPReportFileService{
			readContent: FileContent{
				Filename: "report.bin", ContentType: "application/octet-stream", SizeBytes: 4,
				Content: io.NopCloser(strings.NewReader("bin")),
			},
		},
		Recorder: &fakeMCPOperationRecorder{},
	})

	missing := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-missing"},
		DocumentMCPToolReadReportFile, json.RawMessage(`{}`))
	if missing.Status != documentMCPToolResultFailed || missing.Error == nil || missing.Error.Code != string(CodeValidation) {
		t.Fatalf("missing id result = %+v", missing)
	}
	invalidFormat := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-format"},
		DocumentMCPToolReadReportFile, json.RawMessage(`{"reportFileId":"rf-1","format":"docx"}`))
	if invalidFormat.Status != documentMCPToolResultFailed || invalidFormat.Error == nil || invalidFormat.Error.Code != string(CodeValidation) {
		t.Fatalf("invalid format result = %+v", invalidFormat)
	}
	unsupported := svc.CallTool(context.Background(), RequestContext{UserID: "user-1", RequestID: "req-unsupported"},
		DocumentMCPToolReadReportFile, json.RawMessage(`{"reportFileId":"rf-1"}`))
	if unsupported.Status != documentMCPToolResultFailed || unsupported.Error == nil || unsupported.Error.Code != string(CodeConflict) {
		t.Fatalf("unsupported content result = %+v", unsupported)
	}
}

func largeDOCXWithText(t *testing.T, text string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	document, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := document.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`)); err != nil {
		t.Fatal(err)
	}
	padding, err := writer.CreateHeader(&zip.FileHeader{
		Name:   "word/media/padding.bin",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := padding.Write(bytes.Repeat([]byte("x"), documentMCPMaxReportFileContentBytes+1024)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertSchemaRequires(t *testing.T, schema map[string]any, fields ...string) {
	t.Helper()
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema required field has unexpected type: %+v", schema["required"])
	}
	have := map[string]bool{}
	for _, value := range required {
		if text, ok := value.(string); ok {
			have[text] = true
		}
	}
	for _, field := range fields {
		if !have[field] {
			t.Fatalf("schema required = %+v, missing %q", required, field)
		}
	}
}

type fakeMCPDocumentService struct {
	structure      ReportTemplateStructure
	err            error
	materials      ReportMaterialListResult
	material       ReportMaterial
	listMatErr     error
	getMatErr      error
	listMatFilters []ReportMaterialListFilter
}

func (f *fakeMCPDocumentService) GetReportTemplateStructure(context.Context, RequestContext, string) (ReportTemplateStructure, error) {
	if f.err != nil {
		return ReportTemplateStructure{}, f.err
	}
	return f.structure, nil
}

func (f *fakeMCPDocumentService) ListReportMaterials(_ context.Context, _ RequestContext, filter ReportMaterialListFilter) (ReportMaterialListResult, error) {
	f.listMatFilters = append(f.listMatFilters, filter)
	if f.listMatErr != nil {
		return ReportMaterialListResult{}, f.listMatErr
	}
	return f.materials, nil
}

func (f *fakeMCPDocumentService) GetReportMaterial(context.Context, RequestContext, string) (ReportMaterial, error) {
	if f.getMatErr != nil {
		return ReportMaterial{}, f.getMatErr
	}
	return f.material, nil
}

type fakeMCPJobService struct {
	createJob    ReportJob
	createErr    error
	createInputs []CreateJobInput
	getJob       ReportJob
	getErr       error
}

func (f *fakeMCPJobService) CreateJob(_ context.Context, _ RequestContext, input CreateJobInput) (ReportJob, error) {
	f.createInputs = append(f.createInputs, input)
	if f.createErr != nil {
		return ReportJob{}, f.createErr
	}
	job := f.createJob
	if job.ID == "" {
		job = ReportJob{ID: "job-1", ReportID: input.ReportID, JobType: input.JobType, TargetType: input.TargetScope, TargetID: input.SectionID, Status: JobStatusPending}
	}
	return job, nil
}

func (f *fakeMCPJobService) GetJob(context.Context, RequestContext, string) (ReportJob, error) {
	if f.getErr != nil {
		return ReportJob{}, f.getErr
	}
	if f.getJob.ID == "" {
		return ReportJob{ID: "job-1", ReportID: "report-1", JobType: JobTypeContentGeneration, Status: JobStatusRunning}, nil
	}
	return f.getJob, nil
}

type fakeMCPReportService struct {
	report       Report
	err          error
	createReport Report
	createErr    error
	createInputs []CreateReportInput
	list         ReportListResult
	listErr      error
	listFilters  []ReportListFilter
}

func (f *fakeMCPReportService) CreateReport(_ context.Context, _ RequestContext, input CreateReportInput) (Report, error) {
	f.createInputs = append(f.createInputs, input)
	if f.createErr != nil {
		return Report{}, f.createErr
	}
	if f.createReport.ID != "" {
		return f.createReport, nil
	}
	return Report{ID: "report-created", Name: input.Name, ReportType: input.ReportType, TemplateID: input.TemplateID, Status: ReportStatusDraft}, nil
}

func (f *fakeMCPReportService) GetReport(context.Context, RequestContext, string) (Report, error) {
	if f.err != nil {
		return Report{}, f.err
	}
	return f.report, nil
}

func (f *fakeMCPReportService) ListReports(_ context.Context, _ RequestContext, filter ReportListFilter) (ReportListResult, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return ReportListResult{}, f.listErr
	}
	return f.list, nil
}

type fakeMCPReportSettingsService struct {
	settings ReportSettings
	err      error
}

func (f *fakeMCPReportSettingsService) GetReportSettings(context.Context) (ReportSettings, error) {
	if f.err != nil {
		return ReportSettings{}, f.err
	}
	return f.settings, nil
}

type fakeMCPReportFileService struct {
	createFile   ReportFile
	createErr    error
	createInputs []CreateReportFileInput
	getFile      ReportFile
	getErr       error
	list         ReportFileListResult
	listErr      error
	listFilters  []ReportFileListFilter
	readContent  FileContent
	readErr      error
}

func (f *fakeMCPReportFileService) CreateReportFile(_ context.Context, _ RequestContext, input CreateReportFileInput) (ReportFile, error) {
	f.createInputs = append(f.createInputs, input)
	if f.createErr != nil {
		return ReportFile{}, f.createErr
	}
	return f.createFile, nil
}

func (f *fakeMCPReportFileService) GetReportFile(context.Context, RequestContext, string) (ReportFile, error) {
	if f.getErr != nil {
		return ReportFile{}, f.getErr
	}
	return f.getFile, nil
}

func (f *fakeMCPReportFileService) ListReportFiles(_ context.Context, _ RequestContext, filter ReportFileListFilter) (ReportFileListResult, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return ReportFileListResult{}, f.listErr
	}
	return f.list, nil
}

func (f *fakeMCPReportFileService) ReadReportFileContent(context.Context, RequestContext, string) (FileContent, error) {
	if f.readErr != nil {
		return FileContent{}, f.readErr
	}
	return f.readContent, nil
}

type fakeMCPOperationRecorder struct {
	logs []OperationLog
}

func (f *fakeMCPOperationRecorder) CreateOperationLog(_ context.Context, log OperationLog) (OperationLog, error) {
	f.logs = append(f.logs, log)
	return log, nil
}

func (f *fakeMCPOperationRecorder) singleLog(t *testing.T) OperationLog {
	t.Helper()
	if len(f.logs) != 1 {
		t.Fatalf("operation log count = %d, want 1: %+v", len(f.logs), f.logs)
	}
	return f.logs[0]
}

func (f *fakeMCPOperationRecorder) lastLog(t *testing.T) OperationLog {
	t.Helper()
	if len(f.logs) == 0 {
		t.Fatal("operation log count = 0, want at least 1")
	}
	return f.logs[len(f.logs)-1]
}
