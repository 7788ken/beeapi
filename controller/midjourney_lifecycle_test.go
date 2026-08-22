package controller

import (
	"context"
	"testing"
)

func TestPollingLifecycleFunctionContracts(t *testing.T) {
	var midjourneyProducer func(context.Context) = UpdateMidjourneyTaskBulk
	var midjourneyRound func(context.Context) = UpdateMidjourneyTaskOnce
	var taskProducer func(context.Context) = UpdateTaskBulk
	if midjourneyProducer == nil || midjourneyRound == nil || taskProducer == nil {
		t.Fatal("polling lifecycle function is nil")
	}
}
