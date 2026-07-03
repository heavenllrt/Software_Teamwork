package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultAgentConfigEnablesAttachmentSearch(t *testing.T) {
	config := DefaultAgentConfig()
	if !containsString(config.EnabledToolNames, "search_session_attachments") {
		t.Fatalf("enabledToolNames=%v, want search_session_attachments", config.EnabledToolNames)
	}
	if !containsString(config.EnabledToolNames, "document__generate_report_from_content") {
		t.Fatalf("enabledToolNames=%v, want document__generate_report_from_content", config.EnabledToolNames)
	}
	if !containsString(config.EnabledToolNames, "document__list_reports") {
		t.Fatalf("enabledToolNames=%v, want document__list_reports", config.EnabledToolNames)
	}
	if !containsString(config.EnabledToolNames, "document__read_report_file") {
		t.Fatalf("enabledToolNames=%v, want document__read_report_file", config.EnabledToolNames)
	}
	if containsString(config.EnabledToolNames, "document__list_materials") || containsString(config.EnabledToolNames, "document__get_material") {
		t.Fatalf("enabledToolNames=%v, material metadata tools must stay opt-in", config.EnabledToolNames)
	}
}

func TestReportGenerationDirectiveMentionsContentReportTool(t *testing.T) {
	directive := requestDirective(AskInput{Mode: "report_generation"})
	if !strings.Contains(directive, "document__generate_report_from_content") ||
		!strings.Contains(directive, "search_session_attachments") ||
		!strings.Contains(directive, "include_report_source=true") ||
		!strings.Contains(directive, "report_source_excerpt") {
		t.Fatalf("directive=%q, want attachment-to-report tool guidance", directive)
	}
}

func TestDefaultAgentConfigEnablesKnowledgeMCPTools(t *testing.T) {
	config := DefaultAgentConfig()
	for _, name := range []string{
		"knowledge__search", "knowledge__list_documents",
		"knowledge__get_document", "knowledge__get_chunk",
	} {
		if !containsString(config.EnabledToolNames, name) {
			t.Fatalf("enabledToolNames=%v, want %s", config.EnabledToolNames, name)
		}
	}
}

func TestNormalizeCitationPreservesAvailableAttachmentSource(t *testing.T) {
	citation := NormalizeCitation(Citation{
		AttachmentID:      "attachment-1",
		DocumentName:      "guide.pdf",
		IsSourceAvailable: true,
	})
	if !citation.IsSourceAvailable || citation.Source == nil || !citation.Source.Available {
		t.Fatalf("attachment citation source = %+v, want available", citation)
	}
	if citation.Source.DownloadEndpoint != "" {
		t.Fatalf("attachment citation download endpoint = %q, want empty", citation.Source.DownloadEndpoint)
	}
}

type resourceRepositoryStub struct {
	activeQAConfig   QAConfigVersion
	activeLLMConfig  LLMConfigVersion
	createdQAInput   CreateQAConfigVersionInput
	createdLLMInput  CreateLLMConfigVersionInput
	createQACalled   bool
	createLLMCalled  bool
	createQAErr      error
	createLLMErr     error
	restoredQAID     string
	restoredLLMID    string
	savedInput       RetrievalTestInput
	savedRunErr      error
	saveCalled       bool
	messageCitations []Citation
	citation         Citation
	lookupCitations  []Citation
	listEventsCalled bool
}

func (r *resourceRepositoryStub) GetResponseRun(context.Context, string, string) (ResponseRun, error) {
	return ResponseRun{}, nil
}
func (r *resourceRepositoryStub) CancelResponseRun(context.Context, string, string) (ResponseRun, error) {
	return ResponseRun{}, nil
}
func (r *resourceRepositoryStub) ListStreamEvents(context.Context, string, string, string, int) ([]StreamEvent, error) {
	r.listEventsCalled = true
	return nil, nil
}
func (r *resourceRepositoryStub) ListMessageCitations(context.Context, string, string) ([]Citation, error) {
	return append([]Citation(nil), r.messageCitations...), nil
}
func (r *resourceRepositoryStub) GetCitation(context.Context, string, string) (Citation, error) {
	return r.citation, nil
}
func (r *resourceRepositoryStub) LookupCitations(context.Context, string, []string) ([]Citation, error) {
	return append([]Citation(nil), r.lookupCitations...), nil
}
func (r *resourceRepositoryStub) ListToolCalls(context.Context, string, string) ([]AgentToolCall, error) {
	return nil, nil
}
func (r *resourceRepositoryStub) GetActiveQAConfigVersion(context.Context) (QAConfigVersion, error) {
	return r.activeQAConfig, nil
}
func (r *resourceRepositoryStub) CreateQAConfigVersionResource(_ context.Context, _ string, input CreateQAConfigVersionInput) (QAConfigVersion, error) {
	r.createQACalled = true
	r.createdQAInput = input
	replacedID := ""
	if r.activeQAConfig.IsActive {
		replacedID = r.activeQAConfig.ID
		r.activeQAConfig.IsActive = false
	}
	r.activeQAConfig = QAConfigVersion{ID: "qa-config-id", IsActive: shouldActivateConfig(input.Activate), ReplacedActiveVersionID: replacedID}
	return r.activeQAConfig, r.createQAErr
}
func (r *resourceRepositoryStub) RestoreQAConfigActiveVersion(_ context.Context, failedVersionID, restoreVersionID string) error {
	r.restoredQAID = restoreVersionID
	if r.activeQAConfig.ID == failedVersionID {
		r.activeQAConfig.IsActive = false
		if restoreVersionID != "" {
			r.activeQAConfig = QAConfigVersion{ID: restoreVersionID, IsActive: true}
		}
	}
	return nil
}
func (r *resourceRepositoryStub) GetActiveLLMConfigVersion(context.Context) (LLMConfigVersion, error) {
	return r.activeLLMConfig, nil
}
func (r *resourceRepositoryStub) CreateLLMConfigVersionResource(_ context.Context, _ string, input CreateLLMConfigVersionInput) (LLMConfigVersion, error) {
	r.createLLMCalled = true
	r.createdLLMInput = input
	replacedID := ""
	if r.activeLLMConfig.IsActive {
		replacedID = r.activeLLMConfig.ID
		r.activeLLMConfig.IsActive = false
	}
	r.activeLLMConfig = LLMConfigVersion{ID: "llm-config-id", IsActive: shouldActivateConfig(input.Activate), ReplacedActiveVersionID: replacedID}
	return r.activeLLMConfig, r.createLLMErr
}
func (r *resourceRepositoryStub) RestoreLLMConfigActiveVersion(_ context.Context, failedVersionID, restoreVersionID string) error {
	r.restoredLLMID = restoreVersionID
	if r.activeLLMConfig.ID == failedVersionID {
		r.activeLLMConfig.IsActive = false
		if restoreVersionID != "" {
			r.activeLLMConfig = LLMConfigVersion{ID: restoreVersionID, IsActive: true}
		}
	}
	return nil
}
func (r *resourceRepositoryStub) SaveLLMConnectionTest(context.Context, string, LLMProfileTestResult) (LLMProfileTestResult, error) {
	return LLMProfileTestResult{}, nil
}
func (r *resourceRepositoryStub) SaveRetrievalTestRun(_ context.Context, _ string, input RetrievalTestInput, results []RetrievalTestResult, duration time.Duration, runErr error) (RetrievalTestRun, error) {
	r.saveCalled = true
	r.savedInput = input
	r.savedRunErr = runErr
	status := "completed"
	errorMessage := ""
	if runErr != nil {
		status = "failed"
		errorMessage = "knowledge retrieval failed"
	}
	return RetrievalTestRun{ID: "rt-1", Question: input.Question, Status: status, ResultCount: len(results), LatencyMS: duration.Milliseconds(), ErrorMessage: errorMessage, Results: results}, nil
}
func (r *resourceRepositoryStub) GetRetrievalTestRun(context.Context, string, string) (RetrievalTestRun, error) {
	return RetrievalTestRun{}, nil
}
func (r *resourceRepositoryStub) GetMetricsOverview(context.Context, string, int) (MetricsOverview, error) {
	return MetricsOverview{}, nil
}
func (r *resourceRepositoryStub) GetMetricsTrend(context.Context, int) (MetricsTrend, error) {
	return MetricsTrend{}, nil
}
func (r *resourceRepositoryStub) GetTopQueries(context.Context, int, int) ([]TopQuery, error) {
	return nil, nil
}
func (r *resourceRepositoryStub) GetIntentDistribution(context.Context, int) ([]IntentDistribution, error) {
	return nil, nil
}

type knowledgeRetrieverStub struct {
	input   RetrievalTestInput
	results []RetrievalTestResult
	err     error
}

func (r *knowledgeRetrieverStub) Retrieve(_ context.Context, _ string, input RetrievalTestInput) ([]RetrievalTestResult, error) {
	r.input = input
	return r.results, r.err
}

type llmTesterStub struct{}

func (llmTesterStub) TestLLM(context.Context, RuntimeLLMConfig) (LLMConnectionTestResult, error) {
	return LLMConnectionTestResult{}, nil
}

type runCancellerStub struct{}

func (runCancellerStub) CancelActiveRun(string) {}

type resourceReloaderStub struct {
	called int
	err    error
}

func (r *resourceReloaderStub) Reload(context.Context) error {
	r.called++
	return r.err
}

func TestListStreamEventsRejectsCursorOverflow(t *testing.T) {
	repository := &resourceRepositoryStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resources.ListStreamEvents(context.Background(), "user-1", "session-1", "run-1", math.MaxInt32+1)
	appErr, ok := Classify(err)
	if !ok || appErr.Code != CodeValidation || appErr.Fields["afterEventSeq"] == "" {
		t.Fatalf("error=%v, want afterEventSeq validation", err)
	}
	if repository.listEventsCalled {
		t.Fatal("repository should not be called for invalid afterEventSeq")
	}
}

func TestCreateRetrievalTestRunMergesActiveConfigAndOverrides(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{
		ID:                      "qa-config-id",
		DefaultKnowledgeBaseIDs: []string{"kb-default"},
		Retrieval:               RetrievalSettings{TopK: 5, ScoreThreshold: .7, EnableRerank: false},
	}}
	retriever := &knowledgeRetrieverStub{results: []RetrievalTestResult{{DocumentID: "doc-1", Metadata: map[string]any{}}}}
	resources, err := NewResourceService(repository, retriever, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	run, err := resources.CreateRetrievalTestRun(context.Background(), "user-1", RetrievalTestInput{
		Question:  "  what is qa  ",
		Retrieval: RetrievalSettings{TopK: 8},
		Overrides: RetrievalSettings{
			SimilarityThreshold: .35,
			UseRerank:           true,
			RerankTopN:          4,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	wantRetrieval := RetrievalSettings{TopK: 8, ScoreThreshold: .35, EnableRerank: true, RerankTopN: 4}
	if retriever.input.Question != "what is qa" || !reflect.DeepEqual(retriever.input.KnowledgeBaseIDs, []string{"kb-default"}) || retriever.input.QAConfigVersionID != "qa-config-id" || !reflect.DeepEqual(retriever.input.Retrieval, wantRetrieval) {
		t.Fatalf("retriever input=%+v", retriever.input)
	}
	if !repository.saveCalled || !reflect.DeepEqual(repository.savedInput.Retrieval, wantRetrieval) {
		t.Fatalf("saved input=%+v", repository.savedInput)
	}
	if run.Status != "completed" || run.ResultCount != 1 {
		t.Fatalf("run=%+v", run)
	}
}

func TestCreateRetrievalTestRunFallsBackToDefaultsAfterKnowledgeBaseNormalization(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{
		ID:                      "qa-config-id",
		DefaultKnowledgeBaseIDs: []string{" kb-default ", "kb-default"},
		Retrieval:               RetrievalSettings{TopK: 5, ScoreThreshold: .7},
	}}
	retriever := &knowledgeRetrieverStub{results: []RetrievalTestResult{{DocumentID: "doc-1", Metadata: map[string]any{}}}}
	resources, err := NewResourceService(repository, retriever, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resources.CreateRetrievalTestRun(context.Background(), "user-1", RetrievalTestInput{
		Question:         "query",
		KnowledgeBaseIDs: []string{" ", ""},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(retriever.input.KnowledgeBaseIDs, []string{"kb-default"}) {
		t.Fatalf("knowledgeBaseIds=%+v, want default knowledge base", retriever.input.KnowledgeBaseIDs)
	}
	if !reflect.DeepEqual(repository.savedInput.KnowledgeBaseIDs, []string{"kb-default"}) {
		t.Fatalf("saved knowledgeBaseIds=%+v, want default knowledge base", repository.savedInput.KnowledgeBaseIDs)
	}
}

func TestCreateRetrievalTestRunCanDisableActiveRerank(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{
		ID:        "qa-config-id",
		Retrieval: RetrievalSettings{TopK: 5, ScoreThreshold: .7, EnableRerank: true, RerankTopN: 3},
	}}
	retriever := &knowledgeRetrieverStub{results: []RetrievalTestResult{{DocumentID: "doc-1", Metadata: map[string]any{}}}}
	resources, err := NewResourceService(repository, retriever, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	var input RetrievalTestInput
	if err := json.Unmarshal([]byte(`{"question":"query","overrides":{"enableRerank":false}}`), &input); err != nil {
		t.Fatal(err)
	}
	run, err := resources.CreateRetrievalTestRun(context.Background(), "user-1", input)
	if err != nil {
		t.Fatal(err)
	}

	wantRetrieval := RetrievalSettings{TopK: 5, ScoreThreshold: .7, EnableRerank: false, RerankTopN: 3}
	if !reflect.DeepEqual(retriever.input.Retrieval, wantRetrieval) {
		t.Fatalf("retrieval=%+v, want %+v", retriever.input.Retrieval, wantRetrieval)
	}
	if run.Status != "completed" {
		t.Fatalf("run=%+v", run)
	}
}

func TestCreateRetrievalTestRunCanClearActiveThresholds(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{
		ID:        "qa-config-id",
		Retrieval: RetrievalSettings{TopK: 5, ScoreThreshold: .7, RerankThreshold: .5},
	}}
	retriever := &knowledgeRetrieverStub{results: []RetrievalTestResult{{DocumentID: "doc-1", Metadata: map[string]any{}}}}
	resources, err := NewResourceService(repository, retriever, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	var input RetrievalTestInput
	if err := json.Unmarshal([]byte(`{"question":"query","overrides":{"scoreThreshold":0,"rerankThreshold":0}}`), &input); err != nil {
		t.Fatal(err)
	}
	run, err := resources.CreateRetrievalTestRun(context.Background(), "user-1", input)
	if err != nil {
		t.Fatal(err)
	}

	wantRetrieval := RetrievalSettings{TopK: 5, scoreThresholdSet: true, rerankThresholdSet: true}
	if !reflect.DeepEqual(retriever.input.Retrieval, wantRetrieval) {
		t.Fatalf("retrieval=%+v, want %+v", retriever.input.Retrieval, wantRetrieval)
	}
	if run.Status != "completed" {
		t.Fatalf("run=%+v", run)
	}
}

func TestCreateRetrievalTestRunRejectsRerankTopNGreaterThanTopK(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{ID: "qa-config-id", Retrieval: RetrievalSettings{TopK: 5}}}
	retriever := &knowledgeRetrieverStub{results: []RetrievalTestResult{{DocumentID: "doc-1", Metadata: map[string]any{}}}}
	resources, err := NewResourceService(repository, retriever, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resources.CreateRetrievalTestRun(context.Background(), "user-1", RetrievalTestInput{
		Question:  "query",
		Overrides: RetrievalSettings{RerankTopN: 6},
	})
	appErr, ok := Classify(err)
	if !ok || appErr.Code != CodeValidation || appErr.Fields["retrieval.rerankTopN"] == "" {
		t.Fatalf("error=%v, want retrieval.rerankTopN validation", err)
	}
	if repository.saveCalled || retriever.input.Question != "" {
		t.Fatalf("unexpected retrieval or save: input=%+v saveCalled=%v", retriever.input, repository.saveCalled)
	}
}

func TestCreateRetrievalTestRunSavesKnowledgeFailureSummary(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{ID: "qa-config-id", Retrieval: RetrievalSettings{TopK: 5}}}
	retriever := &knowledgeRetrieverStub{err: errors.New("dial tcp internal.service: timeout")}
	resources, err := NewResourceService(repository, retriever, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	run, err := resources.CreateRetrievalTestRun(context.Background(), "user-1", RetrievalTestInput{Question: "query"})
	if err != nil {
		t.Fatalf("error=%v, want saved failed run", err)
	}
	if !repository.saveCalled || repository.savedRunErr == nil {
		t.Fatalf("saveCalled=%v runErr=%v", repository.saveCalled, repository.savedRunErr)
	}
	if run.Status != "failed" || run.ErrorMessage != "knowledge retrieval failed" {
		t.Fatalf("run=%+v", run)
	}
}

func TestCreateRetrievalTestRunPreservesKnowledgeForbidden(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{ID: "qa-config-id", Retrieval: RetrievalSettings{TopK: 5}}}
	retriever := &knowledgeRetrieverStub{err: NewError(CodeForbidden, "knowledge base access is forbidden", nil)}
	resources, err := NewResourceService(repository, retriever, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	run, err := resources.CreateRetrievalTestRun(context.Background(), "user-1", RetrievalTestInput{Question: "query"})
	if err != nil {
		t.Fatalf("error=%v, want saved failed run", err)
	}
	if run.Status != "failed" || run.ErrorMessage != "knowledge retrieval failed" {
		t.Fatalf("run=%+v", run)
	}
}

func TestCreateQAConfigVersionPreservesExplicitEmptyAgentToolWhitelist(t *testing.T) {
	repository := &resourceRepositoryStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resources.CreateQAConfigVersion(context.Background(), "user-1", CreateQAConfigVersionInput{
		Agent:            AgentConfig{EnabledToolNames: []string{}},
		EnabledToolNames: []string{"search_knowledge", "search_knowledge"},
	})
	if err != nil {
		t.Fatalf("error=%v, want explicit empty nested whitelist to skip legacy duplicate validation", err)
	}
	if !repository.createQACalled {
		t.Fatal("CreateQAConfigVersionResource was not called")
	}
	if repository.createdQAInput.Agent.EnabledToolNames == nil || len(repository.createdQAInput.Agent.EnabledToolNames) != 0 {
		t.Fatalf("agent.enabledToolNames=%#v, want explicit empty whitelist", repository.createdQAInput.Agent.EnabledToolNames)
	}
}

func TestCreateQAConfigVersionTreatsExplicitZeroScoreThresholdAsConfiguredDuringValidation(t *testing.T) {
	repository := &resourceRepositoryStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resources.CreateQAConfigVersion(context.Background(), "user-1", CreateQAConfigVersionInput{
		Retrieval:           RetrievalSettings{TopK: 5, ScoreThreshold: 0}.WithScoreThresholdConfigured(),
		SimilarityThreshold: 2,
	})
	if err != nil {
		t.Fatalf("error=%v, want explicit zero threshold to ignore legacy invalid threshold", err)
	}
	if !repository.createQACalled {
		t.Fatal("CreateQAConfigVersionResource was not called")
	}
}

func TestCreateQAConfigVersionReloadsRuntimeWhenActivated(t *testing.T) {
	repository := &resourceRepositoryStub{}
	reloader := &resourceReloaderStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateQAConfigVersion(context.Background(), "user-1", CreateQAConfigVersionInput{
		Retrieval: RetrievalSettings{TopK: 5, ScoreThreshold: .2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reloader.called != 1 {
		t.Fatalf("reload called %d times, want 1", reloader.called)
	}
}

func TestCreateQAConfigVersionSkipsRuntimeReloadWhenInactive(t *testing.T) {
	repository := &resourceRepositoryStub{}
	reloader := &resourceReloaderStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)
	activate := false

	_, err = resources.CreateQAConfigVersion(context.Background(), "user-1", CreateQAConfigVersionInput{
		Retrieval: RetrievalSettings{TopK: 5, ScoreThreshold: .2},
		Activate:  &activate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reloader.called != 0 {
		t.Fatalf("reload called %d times, want 0", reloader.called)
	}
}

func TestCreateQAConfigVersionRollsBackActiveVersionWhenReloadFails(t *testing.T) {
	repository := &resourceRepositoryStub{activeQAConfig: QAConfigVersion{ID: "qa-old", IsActive: true}}
	reloader := &resourceReloaderStub{err: errors.New("reload failed")}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateQAConfigVersion(context.Background(), "user-1", CreateQAConfigVersionInput{
		Retrieval: RetrievalSettings{TopK: 5, ScoreThreshold: .2},
	})

	appErr, ok := Classify(err)
	if !ok || appErr.Code != CodeDependency {
		t.Fatalf("error=%v, want dependency error", err)
	}
	if repository.restoredQAID != "qa-old" {
		t.Fatalf("restored qa id=%q, want qa-old", repository.restoredQAID)
	}
	if repository.activeQAConfig.ID != "qa-old" || !repository.activeQAConfig.IsActive {
		t.Fatalf("active QA config=%+v, want restored old active", repository.activeQAConfig)
	}
}

func TestCreateQAConfigVersionDeactivatesFailedVersionWhenReloadFailsWithoutPreviousActive(t *testing.T) {
	repository := &resourceRepositoryStub{}
	reloader := &resourceReloaderStub{err: errors.New("reload failed")}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateQAConfigVersion(context.Background(), "user-1", CreateQAConfigVersionInput{
		Retrieval: RetrievalSettings{TopK: 5, ScoreThreshold: .2},
	})

	appErr, ok := Classify(err)
	if !ok || appErr.Code != CodeDependency {
		t.Fatalf("error=%v, want dependency error", err)
	}
	if repository.restoredQAID != "" {
		t.Fatalf("restored qa id=%q, want empty", repository.restoredQAID)
	}
	if repository.activeQAConfig.ID != "qa-config-id" || repository.activeQAConfig.IsActive {
		t.Fatalf("active QA config=%+v, want failed config inactive", repository.activeQAConfig)
	}
}

func TestCreateQAConfigVersionRollsBackWhenPostCommitReadFails(t *testing.T) {
	repository := &resourceRepositoryStub{
		activeQAConfig: QAConfigVersion{ID: "qa-old", IsActive: true},
		createQAErr:    errors.New("post commit read failed"),
	}
	reloader := &resourceReloaderStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateQAConfigVersion(context.Background(), "user-1", CreateQAConfigVersionInput{
		Retrieval: RetrievalSettings{TopK: 5, ScoreThreshold: .2},
	})

	if err == nil {
		t.Fatal("expected post-commit create error")
	}
	if reloader.called != 0 {
		t.Fatalf("reload called %d times, want 0", reloader.called)
	}
	if repository.restoredQAID != "qa-old" {
		t.Fatalf("restored qa id=%q, want qa-old", repository.restoredQAID)
	}
	if repository.activeQAConfig.ID != "qa-old" || !repository.activeQAConfig.IsActive {
		t.Fatalf("active QA config=%+v, want restored old active", repository.activeQAConfig)
	}
}

func TestCreateLLMConfigVersionReloadsRuntimeWhenActivated(t *testing.T) {
	repository := &resourceRepositoryStub{}
	reloader := &resourceReloaderStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateLLMConfigVersion(context.Background(), "user-1", CreateLLMConfigVersionInput{
		Provider: "ai-gateway", ProfileID: "profile-1", ModelName: "K2.6", TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reloader.called != 1 {
		t.Fatalf("reload called %d times, want 1", reloader.called)
	}
	if !repository.createLLMCalled || repository.createdLLMInput.ModelName != "K2.6" {
		t.Fatalf("llm input not persisted through stub: called=%v input=%+v", repository.createLLMCalled, repository.createdLLMInput)
	}
}

func TestCreateLLMConfigVersionRollsBackActiveVersionWhenReloadFails(t *testing.T) {
	repository := &resourceRepositoryStub{activeLLMConfig: LLMConfigVersion{ID: "llm-old", IsActive: true}}
	reloader := &resourceReloaderStub{err: errors.New("reload failed")}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateLLMConfigVersion(context.Background(), "user-1", CreateLLMConfigVersionInput{
		Provider: "ai-gateway", ProfileID: "profile-1", ModelName: "K2.6", TimeoutSeconds: 60,
	})

	appErr, ok := Classify(err)
	if !ok || appErr.Code != CodeDependency {
		t.Fatalf("error=%v, want dependency error", err)
	}
	if repository.restoredLLMID != "llm-old" {
		t.Fatalf("restored llm id=%q, want llm-old", repository.restoredLLMID)
	}
	if repository.activeLLMConfig.ID != "llm-old" || !repository.activeLLMConfig.IsActive {
		t.Fatalf("active LLM config=%+v, want restored old active", repository.activeLLMConfig)
	}
}

func TestCreateLLMConfigVersionDeactivatesFailedVersionWhenReloadFailsWithoutPreviousActive(t *testing.T) {
	repository := &resourceRepositoryStub{}
	reloader := &resourceReloaderStub{err: errors.New("reload failed")}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateLLMConfigVersion(context.Background(), "user-1", CreateLLMConfigVersionInput{
		Provider: "ai-gateway", ProfileID: "profile-1", ModelName: "K2.6", TimeoutSeconds: 60,
	})

	appErr, ok := Classify(err)
	if !ok || appErr.Code != CodeDependency {
		t.Fatalf("error=%v, want dependency error", err)
	}
	if repository.restoredLLMID != "" {
		t.Fatalf("restored llm id=%q, want empty", repository.restoredLLMID)
	}
	if repository.activeLLMConfig.ID != "llm-config-id" || repository.activeLLMConfig.IsActive {
		t.Fatalf("active LLM config=%+v, want failed config inactive", repository.activeLLMConfig)
	}
}

func TestCreateLLMConfigVersionRollsBackWhenPostCommitReadFails(t *testing.T) {
	repository := &resourceRepositoryStub{
		activeLLMConfig: LLMConfigVersion{ID: "llm-old", IsActive: true},
		createLLMErr:    errors.New("post commit read failed"),
	}
	reloader := &resourceReloaderStub{}
	resources, err := NewResourceService(repository, &knowledgeRetrieverStub{}, llmTesterStub{}, RuntimeLLMConfig{}, runCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	resources.SetReloader(reloader)

	_, err = resources.CreateLLMConfigVersion(context.Background(), "user-1", CreateLLMConfigVersionInput{
		Provider: "ai-gateway", ProfileID: "profile-1", ModelName: "K2.6", TimeoutSeconds: 60,
	})

	if err == nil {
		t.Fatal("expected post-commit create error")
	}
	if reloader.called != 0 {
		t.Fatalf("reload called %d times, want 0", reloader.called)
	}
	if repository.restoredLLMID != "llm-old" {
		t.Fatalf("restored llm id=%q, want llm-old", repository.restoredLLMID)
	}
	if repository.activeLLMConfig.ID != "llm-old" || !repository.activeLLMConfig.IsActive {
		t.Fatalf("active LLM config=%+v, want restored old active", repository.activeLLMConfig)
	}
}

func TestResourceServiceRevalidatesCitationSourceAvailability(t *testing.T) {
	repository := &resourceRepositoryStub{
		messageCitations: []Citation{{
			ID:                "citation-1",
			MessageID:         "message-1",
			CitationNo:        1,
			DocumentID:        "doc-1",
			DocumentName:      "Manual",
			Text:              "saved quote",
			ContentPreview:    "saved preview",
			IsSourceAvailable: true,
			Metadata:          map[string]any{"pageLabel": "1"},
		}},
	}
	retriever := &resourceRetrieverStub{availability: map[string]bool{"doc-1": true}}
	resources, err := NewResourceService(repository, retriever, resourceLLMTester{}, RuntimeLLMConfig{}, resourceCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	citations, err := resources.ListMessageCitations(context.Background(), "user-1", "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if retriever.userID != "user-1" || !reflect.DeepEqual(retriever.documentIDs, []string{"doc-1"}) {
		t.Fatalf("source check user=%q ids=%+v", retriever.userID, retriever.documentIDs)
	}
	if len(citations) != 1 {
		t.Fatalf("citations=%+v", citations)
	}
	citation := citations[0]
	if !citation.IsSourceAvailable || citation.Source == nil || !citation.Source.Available {
		t.Fatalf("source should be available: %+v", citation)
	}
	if citation.Source.DownloadEndpoint != "/api/v1/documents/doc-1/content" || citation.Source.Reason != "" {
		t.Fatalf("unexpected source mapping: %+v", citation.Source)
	}
}

func TestResourceServicePreservesSnapshotWhenSourceCheckFails(t *testing.T) {
	repository := &resourceRepositoryStub{
		citation: Citation{
			ID:                "citation-1",
			MessageID:         "message-1",
			CitationNo:        1,
			DocumentID:        "doc-1",
			DocumentName:      "Manual",
			Text:              "saved quote",
			ContentPreview:    "saved preview",
			Context:           "saved context",
			IsSourceAvailable: true,
			Metadata:          map[string]any{"pageLabel": "1", "objectKey": "secret"},
		},
	}
	retriever := &resourceRetrieverStub{checkErr: errors.New("knowledge unavailable")}
	resources, err := NewResourceService(repository, retriever, resourceLLMTester{}, RuntimeLLMConfig{}, resourceCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	citation, err := resources.GetCitation(context.Background(), "user-1", "citation-1")
	if err != nil {
		t.Fatal(err)
	}
	if citation.IsSourceAvailable || citation.Source == nil || citation.Source.Available {
		t.Fatalf("source should be unavailable (fail closed after check failure): %+v", citation)
	}
	if citation.Source.Reason != citationSourceUnavailableReason || citation.Source.DownloadEndpoint != "" {
		t.Fatalf("unexpected unavailable source mapping after check failure: %+v", citation.Source)
	}
	if citation.Text != "saved quote" || citation.ContentPreview != "saved preview" || citation.Content != "saved preview" {
		t.Fatalf("snapshot text was not preserved: %+v", citation)
	}
	if citation.Metadata["pageLabel"] != "1" {
		t.Fatalf("safe metadata was not preserved: %#v", citation.Metadata)
	}
	if _, ok := citation.Metadata["objectKey"]; ok {
		t.Fatalf("sensitive metadata leaked: %#v", citation.Metadata)
	}
}

type resourceRetrieverStub struct {
	availability map[string]bool
	checkErr     error
	userID       string
	documentIDs  []string
}

func (r *resourceRetrieverStub) Retrieve(context.Context, string, RetrievalTestInput) ([]RetrievalTestResult, error) {
	return nil, nil
}

func (r *resourceRetrieverStub) CheckCitationSources(_ context.Context, userID string, documentIDs []string) (map[string]bool, error) {
	r.userID = userID
	r.documentIDs = append([]string(nil), documentIDs...)
	return r.availability, r.checkErr
}

type resourceLLMTester struct{}

func (resourceLLMTester) TestLLM(context.Context, RuntimeLLMConfig) (LLMConnectionTestResult, error) {
	return LLMConnectionTestResult{Success: true}, nil
}

type resourceCancellerStub struct{}

func (resourceCancellerStub) CancelActiveRun(string) {}

type knowledgeStatsStub struct {
	kbCount  int
	docCount int
	err      error
}

func (s knowledgeStatsStub) GetStats(context.Context, string) (int, int, error) {
	return s.kbCount, s.docCount, s.err
}

func TestGetMetricsOverviewEnrichesWithKnowledgeStats(t *testing.T) {
	repo := &resourceRepositoryStub{}
	svc, err := NewResourceService(repo, &knowledgeRetrieverStub{}, resourceLLMTester{}, RuntimeLLMConfig{}, resourceCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := svc.GetMetricsOverview(context.Background(), "test-user", 7)
	if err != nil {
		t.Fatal(err)
	}
	if overview.KnowledgeBaseCount != 0 || overview.DocumentCount != 0 {
		t.Fatalf("without knowledge stats provider, counts should be zero, got kb=%d doc=%d", overview.KnowledgeBaseCount, overview.DocumentCount)
	}
}

func TestGetMetricsOverviewWithKnowledgeStats(t *testing.T) {
	repo := &resourceRepositoryStub{}
	svc, err := NewResourceService(repo, &knowledgeRetrieverStub{}, resourceLLMTester{}, RuntimeLLMConfig{}, resourceCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	svc.knowledgeStats = knowledgeStatsStub{kbCount: 5, docCount: 42}
	overview, err := svc.GetMetricsOverview(context.Background(), "test-user", 7)
	if err != nil {
		t.Fatal(err)
	}
	if overview.KnowledgeBaseCount != 5 || overview.DocumentCount != 42 {
		t.Fatalf("expected kb=5 doc=42, got kb=%d doc=%d", overview.KnowledgeBaseCount, overview.DocumentCount)
	}
}

func TestGetMetricsOverviewStatsFailureReturnsZero(t *testing.T) {
	repo := &resourceRepositoryStub{}
	svc, err := NewResourceService(repo, &knowledgeRetrieverStub{}, resourceLLMTester{}, RuntimeLLMConfig{}, resourceCancellerStub{})
	if err != nil {
		t.Fatal(err)
	}
	svc.knowledgeStats = knowledgeStatsStub{err: errors.New("knowledge unavailable")}
	overview, err := svc.GetMetricsOverview(context.Background(), "test-user", 7)
	if err != nil {
		t.Fatal(err)
	}
	if overview.KnowledgeBaseCount != 0 || overview.DocumentCount != 0 {
		t.Fatalf("knowledge failure should return zero counts, got kb=%d doc=%d", overview.KnowledgeBaseCount, overview.DocumentCount)
	}
}
