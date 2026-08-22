package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/service"
)

type resourceCloseStep struct {
	name  string
	close func() error
}

func stopBackgroundTasks(ctx context.Context) error {
	return backgroundtask.Stop(ctx)
}

func closeResourceSteps(steps []resourceCloseStep) error {
	var closeErrors []error
	for _, step := range steps {
		if step.close == nil {
			closeErrors = append(closeErrors, fmt.Errorf("%s close function is nil", step.name))
			continue
		}
		if err := step.close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close %s: %w", step.name, err))
		}
	}
	return errors.Join(closeErrors...)
}

// closeApplicationResources must run only after HTTP, detached work, billing,
// audit and all periodic background producers have drained.
func closeApplicationResources() error {
	return closeResourceSteps([]resourceCloseStep{
		{name: "performance metrics", close: perfmetrics.FlushAll},
		{name: "pyroscope", close: common.StopPyroScope},
		{name: "outbound HTTP transports", close: func() error {
			service.CloseHTTPTransports()
			return nil
		}},
		{name: "Redis", close: common.CloseRedis},
		{name: "LOG_DB", close: model.CloseLogDB},
		{name: "main DB", close: model.CloseMainDB},
	})
}
