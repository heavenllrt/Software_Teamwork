package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sakayori-Iroha-168/Software_Teamwork/services/document/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListToolsExposesDocumentSchemas(t *testing.T) {
	toolService := service.NewMCPToolService(service.MCPToolServiceConfig{})
	session, closeSession := newTestSession(t, NewHandler(Config{
		ToolService:  toolService,
		ServiceToken: "mcp-token",
		TokenHeader:  "Authorization",
	}), http.Header{"Authorization": {"Bearer mcp-token"}})
	defer closeSession()

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() failed: %v", err)
	}
	if len(result.Tools) != 16 {
		t.Fatalf("ListTools returned %d tools, want 16", len(result.Tools))
	}

	var exportTool *mcp.Tool
	var contentTool *mcp.Tool
	for i := range result.Tools {
		if result.Tools[i].Name == service.DocumentMCPToolExportReportDOCX {
			exportTool = result.Tools[i]
		}
		if result.Tools[i].Name == service.DocumentMCPToolGenerateReportFromContent {
			contentTool = result.Tools[i]
		}
	}
	if exportTool == nil {
		t.Fatalf("%s tool not found", service.DocumentMCPToolExportReportDOCX)
	}
	schema, ok := exportTool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("input schema type = %T, want object", exportTool.InputSchema)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
	required, ok := schema["required"].([]any)
	if !ok || !contains(required, "reportId") {
		t.Fatalf("schema required = %#v, want reportId", schema["required"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || properties["reportId"] == nil || properties["exportOptions"] == nil {
		t.Fatalf("schema properties = %#v, want reportId and exportOptions", schema["properties"])
	}
	if contentTool == nil {
		t.Fatalf("%s tool not found", service.DocumentMCPToolGenerateReportFromContent)
	}
	contentSchema, ok := contentTool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("content tool input schema type = %T, want object", contentTool.InputSchema)
	}
	if contentSchema["additionalProperties"] != false {
		t.Fatalf("content tool schema = %#v, want strict object", contentSchema)
	}
	contentRequired, ok := contentSchema["required"].([]any)
	if !ok || !contains(contentRequired, "content") {
		t.Fatalf("content schema required = %#v, want content", contentSchema["required"])
	}
}

func TestCallToolPassesTrustedContextAndReturnsStructuredResult(t *testing.T) {
	toolService := &fakeToolService{
		result: service.MCPToolCallResult{
			Status: "succeeded",
			Job: &service.MCPReportJobSummary{
				ID:       "job-1",
				ReportID: "report-1",
				Status:   "succeeded",
			},
		},
	}
	session, closeSession := newTestSession(t, NewHandler(Config{
		ToolService:  toolService,
		ServiceToken: "mcp-token",
		TokenHeader:  "Authorization",
	}), http.Header{
		"Authorization":      {"Bearer mcp-token"},
		"X-Request-Id":       {"req-mcp"},
		"X-User-Id":          {"user-1"},
		"X-User-Roles":       {"admin,report_writer"},
		"X-User-Permissions": {"report:write,report:read"},
		"X-Caller-Service":   {"qa"},
		"X-Forwarded-For":    {"203.0.113.10"},
	})
	defer closeSession()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      service.DocumentMCPToolGetGenerationStatus,
		Arguments: map[string]any{"jobId": "job-1"},
	})
	if err != nil {
		t.Fatalf("CallTool() failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool IsError = true, want false")
	}

	if toolService.call.Name != service.DocumentMCPToolGetGenerationStatus {
		t.Fatalf("tool name = %q", toolService.call.Name)
	}
	if toolService.call.Args["jobId"] != "job-1" {
		t.Fatalf("arguments = %#v, want jobId", toolService.call.Args)
	}
	ctx := toolService.call.Context
	if ctx.RequestID != "req-mcp" || ctx.UserID != "user-1" || ctx.CallerService != "qa" {
		t.Fatalf("request context = %#v", ctx)
	}
	if ctx.ServiceToken != "mcp-token" {
		t.Fatalf("service token = %q, want mcp-token", ctx.ServiceToken)
	}
	if len(ctx.Roles) != 2 || ctx.Roles[0] != "admin" || ctx.Permissions[1] != "report:read" {
		t.Fatalf("roles/permissions = %#v / %#v", ctx.Roles, ctx.Permissions)
	}

	structured := structuredResult(t, result)
	if structured.Status != "succeeded" || structured.Job == nil || structured.Job.ID != "job-1" {
		t.Fatalf("structured result = %#v", structured)
	}
}

func TestCallToolReturnsSafeToolErrors(t *testing.T) {
	for _, code := range []string{
		string(service.CodeValidation),
		string(service.CodeForbidden),
		string(service.CodeDependency),
	} {
		t.Run(code, func(t *testing.T) {
			toolService := &fakeToolService{
				result: service.MCPToolCallResult{
					Status: "failed",
					Error:  &service.MCPToolError{Code: code, Message: "safe summary"},
				},
			}
			session, closeSession := newTestSession(t, NewHandler(Config{
				ToolService:  toolService,
				ServiceToken: "mcp-token",
			}), http.Header{"Authorization": {"Bearer mcp-token"}})
			defer closeSession()

			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      service.DocumentMCPToolExportReportDOCX,
				Arguments: map[string]any{"reportId": "report-1"},
			})
			if err != nil {
				t.Fatalf("CallTool() failed: %v", err)
			}
			if !result.IsError {
				t.Fatalf("CallTool IsError = false, want true")
			}
			structured := structuredResult(t, result)
			if structured.Error == nil || structured.Error.Code != code {
				t.Fatalf("structured error = %#v, want code %s", structured.Error, code)
			}
			if text := result.Content[0].(*mcp.TextContent).Text; containsSensitiveSnippet(text) {
				t.Fatalf("tool content leaked sensitive snippet: %s", text)
			}
		})
	}
}

func TestHandlerRejectsInvalidServiceToken(t *testing.T) {
	server := httptest.NewServer(NewHandler(Config{
		ToolService:  service.NewMCPToolService(service.MCPToolServiceConfig{}),
		ServiceToken: "mcp-token",
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest() failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHandlerRejectsWhenServiceTokenIsNotConfigured(t *testing.T) {
	server := httptest.NewServer(NewHandler(Config{
		ToolService: service.NewMCPToolService(service.MCPToolServiceConfig{}),
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest() failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer anything")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestHandlerRejectsOversizedRequestBodyBeforeToolDecode(t *testing.T) {
	toolService := &fakeToolService{}
	server := httptest.NewServer(NewHandler(Config{
		ToolService:         toolService,
		ServiceToken:        "mcp-token",
		MaxRequestBodyBytes: 64,
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(strings.Repeat("x", 65)))
	if err != nil {
		t.Fatalf("NewRequest() failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer mcp-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if toolService.call.Name != "" {
		t.Fatalf("oversized request reached tool service: %+v", toolService.call)
	}
}

func newTestSession(t *testing.T, handler http.Handler, headers http.Header) (*mcp.ClientSession, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := mcp.NewClient(&mcp.Implementation{Name: "qa-test", Version: "0.1.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL,
		HTTPClient: &http.Client{Transport: headerTransport{
			Base:    http.DefaultTransport,
			Headers: headers,
		}},
	}, nil)
	if err != nil {
		server.Close()
		t.Fatalf("Connect() failed: %v", err)
	}
	return session, func() {
		_ = session.Close()
		server.Close()
	}
}

type headerTransport struct {
	Base    http.RoundTripper
	Headers http.Header
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	cloned := r.Clone(r.Context())
	cloned.Header = r.Header.Clone()
	for name, values := range t.Headers {
		for _, value := range values {
			cloned.Header.Add(name, value)
		}
	}
	return base.RoundTrip(cloned)
}

type fakeToolService struct {
	result service.MCPToolCallResult
	call   capturedCall
}

type capturedCall struct {
	Context service.RequestContext
	Name    string
	Args    map[string]any
}

func (f *fakeToolService) ListTools(context.Context) []service.MCPToolDefinition {
	return service.NewMCPToolService(service.MCPToolServiceConfig{}).ListTools(context.Background())
}

func (f *fakeToolService) CallTool(_ context.Context, reqCtx service.RequestContext, name string, arguments json.RawMessage) service.MCPToolCallResult {
	var args map[string]any
	_ = json.Unmarshal(arguments, &args)
	f.call = capturedCall{Context: reqCtx, Name: name, Args: args}
	result := f.result
	result.RequestID = reqCtx.RequestID
	result.ToolName = name
	return result
}

func structuredResult(t *testing.T, result *mcp.CallToolResult) service.MCPToolCallResult {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var structured service.MCPToolCallResult
	if err := json.Unmarshal(raw, &structured); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	return structured
}

func contains(items []any, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func containsSensitiveSnippet(value string) bool {
	for _, snippet := range []string{"api_key", "service-token", "object_key", "internal://", "provider raw"} {
		if strings.Contains(value, snippet) {
			return true
		}
	}
	return false
}
