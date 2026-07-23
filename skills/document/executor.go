package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
)

const (
	documentSkillID      = "openseal.document"
	documentSkillVersion = "1.0.2"
	NodeTypeDocumentPDF  = documentSkillID
)

type DocumentPDFExecutor struct{ renderer pdfRenderer }

func NewDocumentPDFExecutor(renderer pdfRenderer) *DocumentPDFExecutor {
	return &DocumentPDFExecutor{renderer: renderer}
}

func (e *DocumentPDFExecutor) Type() string { return NodeTypeDocumentPDF }

func (e *DocumentPDFExecutor) Execute(_ context.Context, step *executor.StepDefinition, _ executor.TemplateResolver) (*executor.StepResult, error) {
	if e == nil || e.renderer == nil || step == nil || step.Config == nil {
		return nil, fmt.Errorf("document PDF renderer is not configured")
	}
	title, _ := step.Config["title"].(string)
	body, _ := step.Config["body"].(string)
	filename, _ := step.Config["filename"].(string)
	requirement, _ := step.Config["requirementName"].(string)
	if strings.TrimSpace(filename) == "" || strings.TrimSpace(requirement) == "" {
		return nil, fmt.Errorf("PDF filename and requirementName are required")
	}
	rendered, err := e.renderer.RenderPDF(pdfReport{Title: title, Body: body})
	if err != nil {
		return nil, err
	}
	return &executor.StepResult{Output: map[string]interface{}{
		"_transientPdfBase64": base64.StdEncoding.EncodeToString(rendered.Bytes),
		"filename":            filename, "requirementName": requirement, "pages": rendered.Pages,
	}}, nil
}
