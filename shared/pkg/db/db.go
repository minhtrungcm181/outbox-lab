package db

import (
	"errors"
	"notification-hub/shared/pkg/config"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

var ErrUnsupportedDriver = errors.New("unsupported DB driver")

func Open() (*gorm.DB, error) {
	cfg := config.MustLoad()
	gormLogger := logger.Default.LogMode(logger.Silent)
	gcfg := &gorm.Config{
		Logger:                 gormLogger,
		SkipDefaultTransaction: false,
		PrepareStmt:            true,
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	}
	var (
		db  *gorm.DB
		err error
	)
	db, err = gorm.Open(postgres.Open(cfg.DBDSN), gcfg)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(60 * time.Minute)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)
	return db, nil
}
