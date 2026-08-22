package service

import (
	"context"
	"testing"
)

func TestTaskPollingLifecycleFunctionContracts(t *testing.T) {
	var producer func(context.Context) = TaskPollingLoop
	var round func(context.Context) = RunTaskPollingOnce
	if producer == nil || round == nil {
		t.Fatal("task polling lifecycle function is nil")
	}
}
