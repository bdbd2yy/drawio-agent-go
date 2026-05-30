package persistence

import (
	"fmt"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type MySQLConfig struct {
	Required bool
	DSN      string
}

func OpenMySQL(config MySQLConfig) (*gorm.DB, error) {
	if strings.TrimSpace(config.DSN) == "" {
		if config.Required {
			return nil, fmt.Errorf("mysql dsn is required")
		}
		return nil, nil
	}
	db, err := gorm.Open(mysql.Open(config.DSN), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("mysql db handle: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return db, nil
}
