package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/zeroward/waygate/internal/config"
)

type DB struct {
	SQL          *sql.DB
	AuthDB       string
	CharactersDB string
	WorldDB      string
}

func Open(cfg config.Config) (*DB, error) {
	mc := mysql.NewConfig()
	mc.User = cfg.MySQLUser
	mc.Passwd = cfg.MySQLPassword
	mc.Net = "tcp"
	mc.Addr = fmt.Sprintf("%s:%d", cfg.MySQLHost, cfg.MySQLPort)
	mc.ParseTime = true
	mc.Loc = time.UTC
	mc.Params = map[string]string{
		"charset":        "utf8mb4",
		"timeout":        "5s",
		"readTimeout":    "10s",
		"writeTimeout":   "10s",
		"rejectReadOnly": "true",
	}

	sqlDB, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("mysql ping: %w", err)
	}
	return &DB{
		SQL:          sqlDB,
		AuthDB:       cfg.AuthDB,
		CharactersDB: cfg.CharactersDB,
		WorldDB:      cfg.WorldDB,
	}, nil
}

func (d *DB) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}

func (d *DB) QAuth(table string) string {
	return "`" + d.AuthDB + "`.`" + table + "`"
}

func (d *DB) QChar(table string) string {
	return "`" + d.CharactersDB + "`.`" + table + "`"
}

func (d *DB) QWorld(table string) string {
	return "`" + d.WorldDB + "`.`" + table + "`"
}
