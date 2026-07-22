package main

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestDocumentPDFExecutorReturnsTransientBytes(t *testing.T) {
	pdfExecutor := NewDocumentPDFExecutor(plainTextPDFRenderer{})
	result, err := pdfExecutor.Execute(context.Background(), &executor.StepDefinition{Config: map[string]interface{}{
		"title": "Research", "body": "Source: https://forum.example/thread/7", "filename": "research.pdf", "requirementName": "report",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(result.Output["_transientPdfBase64"].(string))
	if err != nil || string(raw[:8]) != "%PDF-1.4" || result.Output["pages"] != 1 {
		t.Fatalf("PDF output=%#v err=%v", result.Output, err)
	}
}
