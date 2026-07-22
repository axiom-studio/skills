package main

import (
	"context"
	"testing"

	"github.com/axiom-studio/skills.sdk/executor"
)

func TestOutreachWebhookExecutorAdaptsExactInvocation(t *testing.T) {
	var invocation outreachInvocation
	outreachExecutor := &OutreachWebhookExecutor{invoker: outreachInvokerFunc(func(_ context.Context, got outreachInvocation) (map[string]interface{}, error) {
		invocation = got
		return map[string]interface{}{"outreachReceipt": map[string]interface{}{"provider": "test"}}, nil
	})}
	config := map[string]interface{}{
		"targetUri": "https://hooks.example.com/replies/thread-1", "body": "reviewed",
		outreachActionCallIDTransportKey: "action-1", outreachRunIDTransportKey: "run-1", outreachDeploymentIDTransportKey: "researcher",
	}
	result, err := outreachExecutor.Execute(t.Context(), &executor.StepDefinition{Config: config}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.ActionCallID != "action-1" || invocation.RunID != "run-1" || invocation.DeploymentID != "researcher" ||
		invocation.Arguments["body"] != "reviewed" || result.Output["outreachReceipt"] == nil {
		t.Fatalf("invocation=%#v result=%#v", invocation, result)
	}
	invocation.Arguments["body"] = "mutated"
	if config["body"] != "reviewed" {
		t.Fatal("executor leaked mutable config into invocation")
	}
}

func TestOutreachWebhookExecutorFailsClosedWithoutIdentity(t *testing.T) {
	outreachExecutor := &OutreachWebhookExecutor{invoker: outreachInvokerFunc(func(context.Context, outreachInvocation) (map[string]interface{}, error) {
		t.Fatal("incomplete invocation reached transport")
		return nil, nil
	})}
	base := map[string]interface{}{outreachActionCallIDTransportKey: "action-1", outreachRunIDTransportKey: "run-1", outreachDeploymentIDTransportKey: "researcher"}
	for _, missing := range []string{outreachActionCallIDTransportKey, outreachRunIDTransportKey, outreachDeploymentIDTransportKey} {
		config := cloneMap(base)
		delete(config, missing)
		if _, err := outreachExecutor.Execute(t.Context(), &executor.StepDefinition{Config: config}, nil); err == nil {
			t.Fatalf("missing %s was accepted", missing)
		}
	}
}
