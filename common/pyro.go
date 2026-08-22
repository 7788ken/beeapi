package common

import (
	"errors"
	"runtime"
	"sync"

	"github.com/grafana/pyroscope-go"
)

var (
	pyroscopeMu       sync.Mutex
	pyroscopeProfiler *pyroscope.Profiler
)

func StartPyroScope() error {
	pyroscopeUrl := GetEnvOrDefaultString("PYROSCOPE_URL", "")
	if pyroscopeUrl == "" {
		return nil
	}

	pyroscopeAppName := GetEnvOrDefaultString("PYROSCOPE_APP_NAME", "new-api")
	pyroscopeBasicAuthUser := GetEnvOrDefaultString("PYROSCOPE_BASIC_AUTH_USER", "")
	pyroscopeBasicAuthPassword := GetEnvOrDefaultString("PYROSCOPE_BASIC_AUTH_PASSWORD", "")
	pyroscopeHostname := GetEnvOrDefaultString("HOSTNAME", "new-api")

	mutexRate := GetEnvOrDefault("PYROSCOPE_MUTEX_RATE", 5)
	blockRate := GetEnvOrDefault("PYROSCOPE_BLOCK_RATE", 5)

	runtime.SetMutexProfileFraction(mutexRate)
	runtime.SetBlockProfileRate(blockRate)

	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: pyroscopeAppName,

		ServerAddress:     pyroscopeUrl,
		BasicAuthUser:     pyroscopeBasicAuthUser,
		BasicAuthPassword: pyroscopeBasicAuthPassword,

		Logger: nil,

		Tags: map[string]string{"hostname": pyroscopeHostname},

		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
	if err != nil {
		return err
	}
	pyroscopeMu.Lock()
	if pyroscopeProfiler != nil {
		pyroscopeMu.Unlock()
		_ = profiler.Stop()
		return errors.New("pyroscope profiler already started")
	}
	pyroscopeProfiler = profiler
	pyroscopeMu.Unlock()
	return nil
}

func StopPyroScope() error {
	pyroscopeMu.Lock()
	profiler := pyroscopeProfiler
	pyroscopeMu.Unlock()
	if profiler == nil {
		return nil
	}
	if err := profiler.Stop(); err != nil {
		return err
	}
	pyroscopeMu.Lock()
	if pyroscopeProfiler == profiler {
		pyroscopeProfiler = nil
	}
	pyroscopeMu.Unlock()
	return nil
}
