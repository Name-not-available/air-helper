package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

// DB wraps the database connection
type DB struct {
	conn *sql.DB
}

// NewDB creates a new database connection
func NewDB() (*DB, error) {
	// Get connection string from environment variable
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		// Try to build from individual components
		host := getEnvOrDefault("DB_HOST", "localhost")
		port := getEnvOrDefault("DB_PORT", "5432")
		user := getEnvOrDefault("DB_USER", "telegram_bnb_helper")
		password := getEnvOrDefault("DB_PASSWORD", "")
		dbname := getEnvOrDefault("DB_NAME", "telegram_bnb_helper")
		sslmode := getEnvOrDefault("DB_SSLMODE", "disable")

		connStr = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s search_path=telegram_bnb_helper",
			host, port, user, password, dbname, sslmode)
	}

	conn, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{conn: conn}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// initSchema creates the necessary tables if they don't exist
func (db *DB) initSchema() error {
	// Create schema if it doesn't exist
	_, err := db.conn.Exec(`CREATE SCHEMA IF NOT EXISTS telegram_bnb_helper`)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Set search path
	_, err = db.conn.Exec(`SET search_path TO telegram_bnb_helper`)
	if err != nil {
		return fmt.Errorf("failed to set search path: %w", err)
	}

	// Create user_configs table
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS user_configs (
			user_id BIGINT PRIMARY KEY,
			max_pages INTEGER NOT NULL DEFAULT 5,
			min_reviews INTEGER NOT NULL DEFAULT 10,
			min_price DOUBLE PRECISION NOT NULL DEFAULT 0,
			max_price DOUBLE PRECISION NOT NULL DEFAULT 30000000,
			min_stars DOUBLE PRECISION NOT NULL DEFAULT 4.0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create user_configs table: %w", err)
	}

	// Create requests table
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS requests (
			id SERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL,
			telegram_message_id INTEGER NOT NULL,
			url TEXT NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'created',
			listings_count INTEGER DEFAULT 0,
			pages_count INTEGER DEFAULT 0,
			sheet_name VARCHAR(255),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT valid_status CHECK (status IN ('created', 'in_progress', 'done', 'failed'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create requests table: %w", err)
	}

	// Create listings table
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS listings (
			id SERIAL PRIMARY KEY,
			request_id INTEGER NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			price DOUBLE PRECISION,
			currency VARCHAR(10),
			stars DOUBLE PRECISION,
			review_count INTEGER,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT valid_status CHECK (status IN ('pending', 'saved', 'failed'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create listings table: %w", err)
	}

	// Create indexes
	_, err = db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_requests_status ON requests(status)`)
	if err != nil {
		log.Printf("Warning: Failed to create index on requests.status: %v\n", err)
	}

	_, err = db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_requests_user_id ON requests(user_id)`)
	if err != nil {
		log.Printf("Warning: Failed to create index on requests.user_id: %v\n", err)
	}

	_, err = db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_listings_request_id ON listings(request_id)`)
	if err != nil {
		log.Printf("Warning: Failed to create index on listings.request_id: %v\n", err)
	}

	log.Println("Database schema initialized successfully")
	return nil
}

// GetConn returns the underlying database connection
func (db *DB) GetConn() *sql.DB {
	return db.conn
}

