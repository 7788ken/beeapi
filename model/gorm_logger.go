package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/proto"
	"github.com/QuantumNous/new-api/common"
	sqlitedriver "github.com/glebarez/go-sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	defaultSlowThresholdMs = 200
	maxSlowThresholdMs     = 60 * 60 * 1000
)

func newGormConfig() *gorm.Config {
	return &gorm.Config{
		PrepareStmt: true, // precompile SQL
		Logger:      newGormLogger(),
	}
}

func newGormLogger() logger.Interface {
	slowThresholdMs := common.GetEnvOrDefault("SQL_SLOW_THRESHOLD_MS", defaultSlowThresholdMs)
	if slowThresholdMs < 0 || slowThresholdMs > maxSlowThresholdMs {
		common.SysError(fmt.Sprintf("invalid SQL_SLOW_THRESHOLD_MS %d (allowed 0-%d, 0 disables slow query log), using default %d", slowThresholdMs, maxSlowThresholdMs, defaultSlowThresholdMs))
		slowThresholdMs = defaultSlowThresholdMs
	}
	return logger.New(gormLogWriter{}, logger.Config{
		SlowThreshold:             time.Duration(slowThresholdMs) * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      !common.DebugEnabled,
		Colorful:                  false,
	})
}

// gormLogWriter 把 GORM 日志接到 fork 自己的日志系统（含文件轮转），而不是另起一路 os.Stdout。
type gormLogWriter struct{}

func (gormLogWriter) Printf(format string, args ...interface{}) {
	if !common.DebugEnabled {
		for i, arg := range args {
			if err, ok := arg.(error); ok {
				args[i] = sanitizeDBError(err)
			}
		}
	}
	common.SysError(fmt.Sprintf(format, args...))
}

// ParameterizedQueries 只把 SQL 里的值换成占位符，驱动错误消息（如 MySQL 1062 会带上
// tokens.key / users.access_token 的实际值）仍是明文，这里收敛为错误码；DEBUG=true 保留原文。
func sanitizeDBError(err error) error {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return fmt.Errorf("mysql error %d", mysqlErr.Number)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("postgres error SQLSTATE %s", pgErr.Code)
	}
	var chErr *proto.Exception
	if errors.As(err, &chErr) {
		return fmt.Errorf("clickhouse error %d", chErr.Code)
	}
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		return fmt.Errorf("sqlite error %d", sqliteErr.Code())
	}
	return err
}
