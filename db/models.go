package db

import (
	"database/sql"
	"time"
)

// UserConfig represents user-specific configuration
type UserConfig struct {
	UserID     int64
	MaxPages   int
	MinReviews int
	MinPrice   float64
	MaxPrice   float64
	MinStars   float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Request represents a scraping request
type Request struct {
	ID                 int
	UserID             int64
	TelegramMessageID  int
	URL                string
	Status             string // "created", "in_progress", "done", "failed"
	ListingsCount      int
	PagesCount         int
	SheetName          sql.NullString
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Listing represents a scraped listing stored in database
type Listing struct {
	ID          int
	RequestID   int
	Title       string
	URL         string
	Price       sql.NullFloat64
	Currency    sql.NullString
	Stars       sql.NullFloat64
	ReviewCount sql.NullInt64
	Status      string // "pending", "saved", "failed"
	CreatedAt   time.Time
}

// GetUserConfig retrieves user configuration, creating default if not exists
func (db *DB) GetUserConfig(userID int64) (*UserConfig, error) {
	var cfg UserConfig
	err := db.conn.QueryRow(`
		SELECT user_id, max_pages, min_reviews, min_price, max_price, min_stars, created_at, updated_at
		FROM user_configs
		WHERE user_id = $1
	`, userID).Scan(
		&cfg.UserID, &cfg.MaxPages, &cfg.MinReviews, &cfg.MinPrice,
		&cfg.MaxPrice, &cfg.MinStars, &cfg.CreatedAt, &cfg.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		// Create default config
		cfg = UserConfig{
			UserID:     userID,
			MaxPages:   5,
			MinReviews: 10,
			MinPrice:   0,
			MaxPrice:   30000000,
			MinStars:   4.0,
		}
		_, err = db.conn.Exec(`
			INSERT INTO user_configs (user_id, max_pages, min_reviews, min_price, max_price, min_stars)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, cfg.UserID, cfg.MaxPages, cfg.MinReviews, cfg.MinPrice, cfg.MaxPrice, cfg.MinStars)
		if err != nil {
			return nil, err
		}
		return &cfg, nil
	}

	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

// CreateRequest creates a new scraping request
func (db *DB) CreateRequest(userID int64, telegramMessageID int, url string) (*Request, error) {
	var req Request
	var sheetName sql.NullString
	err := db.conn.QueryRow(`
		INSERT INTO requests (user_id, telegram_message_id, url, status)
		VALUES ($1, $2, $3, 'created')
		RETURNING id, user_id, telegram_message_id, url, status, listings_count, pages_count, sheet_name, created_at, updated_at
	`, userID, telegramMessageID, url).Scan(
		&req.ID, &req.UserID, &req.TelegramMessageID, &req.URL, &req.Status,
		&req.ListingsCount, &req.PagesCount, &sheetName, &req.CreatedAt, &req.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	req.SheetName = sheetName
	return &req, nil
}

// GetNextCreatedRequest gets the next request with status 'created'
func (db *DB) GetNextCreatedRequest() (*Request, error) {
	var req Request
	var sheetName sql.NullString
	err := db.conn.QueryRow(`
		SELECT id, user_id, telegram_message_id, url, status, listings_count, pages_count, sheet_name, created_at, updated_at
		FROM requests
		WHERE status = 'created'
		ORDER BY created_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(
		&req.ID, &req.UserID, &req.TelegramMessageID, &req.URL, &req.Status,
		&req.ListingsCount, &req.PagesCount, &sheetName, &req.CreatedAt, &req.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	req.SheetName = sheetName
	return &req, nil
}

// UpdateRequestStatus updates the status of a request
func (db *DB) UpdateRequestStatus(requestID int, status string) error {
	_, err := db.conn.Exec(`
		UPDATE requests
		SET status = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, status, requestID)
	return err
}

// UpdateRequestCounts updates listings and pages count for a request
func (db *DB) UpdateRequestCounts(requestID int, listingsCount, pagesCount int) error {
	_, err := db.conn.Exec(`
		UPDATE requests
		SET listings_count = $1, pages_count = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`, listingsCount, pagesCount, requestID)
	return err
}

// UpdateRequestSheetName updates the sheet name for a request
func (db *DB) UpdateRequestSheetName(requestID int, sheetName string) error {
	_, err := db.conn.Exec(`
		UPDATE requests
		SET sheet_name = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, sheetName, requestID)
	return err
}

// SaveListing saves a listing to the database
func (db *DB) SaveListing(requestID int, title, url string, price *float64, currency *string, stars *float64, reviewCount *int) error {
	var priceVal sql.NullFloat64
	var currencyVal sql.NullString
	var starsVal sql.NullFloat64
	var reviewCountVal sql.NullInt64

	if price != nil {
		priceVal = sql.NullFloat64{Float64: *price, Valid: true}
	}
	if currency != nil {
		currencyVal = sql.NullString{String: *currency, Valid: true}
	}
	if stars != nil {
		starsVal = sql.NullFloat64{Float64: *stars, Valid: true}
	}
	if reviewCount != nil {
		reviewCountVal = sql.NullInt64{Int64: int64(*reviewCount), Valid: true}
	}

	_, err := db.conn.Exec(`
		INSERT INTO listings (request_id, title, url, price, currency, stars, review_count, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'saved')
	`, requestID, title, url, priceVal, currencyVal, starsVal, reviewCountVal)
	return err
}

// GetRequestByID retrieves a request by ID
func (db *DB) GetRequestByID(requestID int) (*Request, error) {
	var req Request
	var sheetName sql.NullString
	err := db.conn.QueryRow(`
		SELECT id, user_id, telegram_message_id, url, status, listings_count, pages_count, sheet_name, created_at, updated_at
		FROM requests
		WHERE id = $1
	`, requestID).Scan(
		&req.ID, &req.UserID, &req.TelegramMessageID, &req.URL, &req.Status,
		&req.ListingsCount, &req.PagesCount, &sheetName, &req.CreatedAt, &req.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	req.SheetName = sheetName
	return &req, nil
}

