package model

import (
	"math/big"
	"strconv"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// mysqlDialector is the MySQL dialector with one change to AutoMigrate: a column default is
// compared by value, not by spelling. MySQL reads a boolean default back as 0/1 and a
// decimal(10,6) default as 0.000000, while the struct tags say false/true and 0. GORM 1.25
// compares the two strings, so every master start ran ALTER TABLE on the same 13 tables,
// users and channels among them (BE-70). The concrete *mysql.Dialector is embedded so that
// optional capabilities such as SavePoint and Apply stay visible to GORM.
type mysqlDialector struct {
	*mysql.Dialector
}

func newMySQLDialector(dsn string) gorm.Dialector {
	return mysqlDialector{Dialector: mysql.Open(dsn).(*mysql.Dialector)}
}

func (d mysqlDialector) Migrator(db *gorm.DB) gorm.Migrator {
	return mysqlMigrator{Migrator: d.Dialector.Migrator(db).(mysql.Migrator)}
}

type mysqlMigrator struct {
	mysql.Migrator
}

func (m mysqlMigrator) MigrateColumn(value interface{}, field *schema.Field, columnType gorm.ColumnType) error {
	return m.Migrator.MigrateColumn(value, field, mysqlColumnDefault{storedColumn: columnType, field: field})
}

// storedColumn is gorm.ColumnType under another name: the interface has a ColumnType()
// method, which an embedded field called ColumnType would shadow.
type storedColumn = gorm.ColumnType

// mysqlColumnDefault hands GORM the stored default in the tag's own spelling when both name
// the same value; every other difference still reaches GORM's comparison unchanged.
type mysqlColumnDefault struct {
	storedColumn
	field *schema.Field
}

func (c mysqlColumnDefault) DefaultValue() (string, bool) {
	stored, ok := c.storedColumn.DefaultValue()
	if ok && sameColumnDefault(c.field, stored) {
		return c.field.DefaultValue, true
	}
	return stored, ok
}

func sameColumnDefault(field *schema.Field, stored string) bool {
	if !field.HasDefaultValue {
		return false
	}
	tagged := field.DefaultValue
	switch field.GORMDataType {
	case schema.Bool:
		a, errA := strconv.ParseBool(stored)
		b, errB := strconv.ParseBool(tagged)
		return errA == nil && errB == nil && a == b
	case schema.Int, schema.Uint, schema.Float:
		a, okA := new(big.Rat).SetString(stored)
		b, okB := new(big.Rat).SetString(tagged)
		return okA && okB && a.Cmp(b) == 0
	}
	return false
}
