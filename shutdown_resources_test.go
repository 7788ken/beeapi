package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCloseResourceStepsPreservesOrderAndAttemptsEveryClose(t *testing.T) {
	var order []string
	redisErr := errors.New("redis close failed")
	steps := []resourceCloseStep{
		{name: "outbound", close: func() error {
			order = append(order, "outbound")
			return nil
		}},
		{name: "redis", close: func() error {
			order = append(order, "redis")
			return redisErr
		}},
		{name: "log-db", close: func() error {
			order = append(order, "log-db")
			return nil
		}},
		{name: "main-db", close: func() error {
			order = append(order, "main-db")
			return nil
		}},
	}

	err := closeResourceSteps(steps)
	if !errors.Is(err, redisErr) {
		t.Fatalf("closeResourceSteps() error = %v, want redis error", err)
	}
	if !strings.Contains(err.Error(), "close redis") {
		t.Fatalf("closeResourceSteps() error = %v, want resource name", err)
	}
	wantOrder := []string{"outbound", "redis", "log-db", "main-db"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("close order = %v, want %v", order, wantOrder)
	}
}
