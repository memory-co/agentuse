// Package store owns the Server metadata database, ORM models and migrations.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"net/url"
	"path/filepath"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ErrNotFound 表示要找的行不存在(或不属于当前用户)。
var ErrNotFound = errors.New("store: not found")

// Config selects the metadata backend. SQLite is the default; DSN is its file
// path or a MySQL/PostgreSQL connection string, depending on Driver.
type Config struct {
	Driver string
	DSN    string
}

type Store struct {
	db  *gorm.DB
	now func() time.Time
}

func Open(path string) (*Store, error) { return OpenConfig(Config{Driver: "sqlite", DSN: path}) }

func OpenConfig(config Config) (*Store, error) {
	if config.Driver == "" {
		config.Driver = "sqlite"
	}
	var dialector gorm.Dialector
	switch config.Driver {
	case "sqlite":
		path := config.DSN
		if path == "" {
			return nil, errors.New("sqlite database path is required")
		}
		dsn := "file::memory:"
		if path != ":memory:" {
			absolute, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			dsn = (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
		}
		dsn += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate"
		dialector = gormlite.Open(dsn)
	case "mysql":
		parsed, err := mysqldriver.ParseDSN(config.DSN)
		if err != nil || parsed.DBName == "" {
			return nil, errors.New("invalid MySQL DSN: a database name is required")
		}
		parsed.ClientFoundRows = true
		dialector = mysql.Open(parsed.FormatDSN())
	case "postgres":
		if config.DSN == "" {
			return nil, errors.New("PostgreSQL DSN is required")
		}
		if _, err := pgx.ParseConfig(config.DSN); err != nil {
			return nil, errors.New("invalid PostgreSQL DSN")
		}
		dialector = postgres.Open(config.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver %q (use sqlite, mysql, or postgres)", config.Driver)
	}
	var db *gorm.DB
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for {
		db, err = gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), SkipDefaultTransaction: true, TranslateError: true, DisableForeignKeyConstraintWhenMigrating: true})
		if err == nil {
			break
		}
		if db != nil {
			if pool, e := db.DB(); e == nil {
				pool.Close()
			}
		}
		// SQLite cannot run its busy handler for every journal-mode transition.
		// Concurrent first opens may therefore fail before migration can lock.
		if config.Driver != "sqlite" || !(errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED)) || time.Now().After(deadline) {
			return nil, fmt.Errorf("store: connect %s: %w", config.Driver, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(16)
	pool.SetMaxIdleConns(4)
	pool.SetConnMaxLifetime(30 * time.Minute)
	if config.Driver == "sqlite" {
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		pool.SetConnMaxLifetime(0)
	}
	if err = migrate(db); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error {
	pool, err := s.db.DB()
	if err != nil {
		return err
	}
	return pool.Close()
}

func (s *Store) unix() int64 { return s.now().Unix() }

// HashSecret 把令牌 / 密钥变成存库用的哈希。
func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// RandomToken 生成带前缀的随机令牌，如 ut_xxxx。
func RandomToken(prefix string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func RandomID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func nullStr(v sql.NullString) string { return v.String }

// Connection supplies the ORM executor for service-owned transactions.
func (s *Store) Connection() *gorm.DB { return s.db }

func (s *Store) Tx(ctx context.Context, fn func(*gorm.DB) error) error {
	tx, err := Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit().Error
}
