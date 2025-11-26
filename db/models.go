package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"bnb-fetcher/models"
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

// Listing represents a fetched listing stored in database
type Listing struct {
	ID               int
	RequestID        int
	Title            string
	URL              string
	Price            sql.NullFloat64
	Currency         sql.NullString
	Stars            sql.NullFloat64
	ReviewCount      sql.NullInt64
	Status           string // "pending", "saved", "failed"
	IsSuperhost      sql.NullBool
	IsGuestFavorite  sql.NullBool
	Bedrooms         sql.NullInt64
	Bathrooms        sql.NullInt64
	Beds             sql.NullInt64
	Description      sql.NullString
	HouseRules       sql.NullString
	NewestReviewDate sql.NullTime
	CreatedAt        time.Time
}

// Review represents a review stored in database
type Review struct {
	ID           int
	ListingID    int
	Date         time.Time
	Score        sql.NullFloat64
	FullText     string
	TimeOnAirbnb sql.NullString
	CreatedAt    time.Time
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
			MaxPrice:   2000,
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

// SaveEnrichedListing saves a listing with all detail page fields to the database
// Returns the listing ID
func (db *DB) SaveEnrichedListing(requestID int, title, url string, price *float64, currency *string, stars *float64, reviewCount *int,
	isSuperhost *bool, isGuestFavorite *bool, bedrooms *int, bathrooms *int, beds *int,
	description *string, houseRules *string, newestReviewDate *time.Time) (int, error) {
	var priceVal sql.NullFloat64
	var currencyVal sql.NullString
	var starsVal sql.NullFloat64
	var reviewCountVal sql.NullInt64
	var isSuperhostVal sql.NullBool
	var isGuestFavoriteVal sql.NullBool
	var bedroomsVal sql.NullInt64
	var bathroomsVal sql.NullInt64
	var bedsVal sql.NullInt64
	var descriptionVal sql.NullString
	var houseRulesVal sql.NullString
	var newestReviewDateVal sql.NullTime

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
	if isSuperhost != nil {
		isSuperhostVal = sql.NullBool{Bool: *isSuperhost, Valid: true}
	}
	if isGuestFavorite != nil {
		isGuestFavoriteVal = sql.NullBool{Bool: *isGuestFavorite, Valid: true}
	}
	if bedrooms != nil {
		bedroomsVal = sql.NullInt64{Int64: int64(*bedrooms), Valid: true}
	}
	if bathrooms != nil {
		bathroomsVal = sql.NullInt64{Int64: int64(*bathrooms), Valid: true}
	}
	if beds != nil {
		bedsVal = sql.NullInt64{Int64: int64(*beds), Valid: true}
	}
	if description != nil {
		descriptionVal = sql.NullString{String: *description, Valid: true}
	}
	if houseRules != nil {
		houseRulesVal = sql.NullString{String: *houseRules, Valid: true}
	}
	if newestReviewDate != nil {
		newestReviewDateVal = sql.NullTime{Time: *newestReviewDate, Valid: true}
	}

	var listingID int
	err := db.conn.QueryRow(`
		INSERT INTO listings (request_id, title, url, price, currency, stars, review_count, 
			is_superhost, is_guest_favorite, bedrooms, bathrooms, beds, description, house_rules, newest_review_date, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'saved')
		RETURNING id
	`, requestID, title, url, priceVal, currencyVal, starsVal, reviewCountVal,
		isSuperhostVal, isGuestFavoriteVal, bedroomsVal, bathroomsVal, bedsVal,
		descriptionVal, houseRulesVal, newestReviewDateVal).Scan(&listingID)
	return listingID, err
}

// SaveReviews saves multiple reviews for a listing
// Accepts models.Review slice and converts to database format
func (db *DB) SaveReviews(listingID int, reviews []models.Review) error {
	if len(reviews) == 0 {
		return nil
	}

	// Use a transaction for bulk insert
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO listing_reviews (listing_id, date, score, full_text, time_on_airbnb)
		VALUES ($1, $2, $3, $4, $5)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, review := range reviews {
		var scoreVal sql.NullFloat64
		var timeOnAirbnbVal sql.NullString

		if review.Score > 0 {
			scoreVal = sql.NullFloat64{Float64: review.Score, Valid: true}
		}
		if review.TimeOnAirbnb != "" {
			timeOnAirbnbVal = sql.NullString{String: review.TimeOnAirbnb, Valid: true}
		}

		_, err := stmt.Exec(listingID, review.Date, scoreVal, review.FullText, timeOnAirbnbVal)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
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

// UpdateUserConfig updates user configuration
func (db *DB) UpdateUserConfig(userID int64, maxPages *int, minReviews *int, minPrice *float64, maxPrice *float64, minStars *float64) error {
	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if maxPages != nil {
		updates = append(updates, fmt.Sprintf("max_pages = $%d", argIndex))
		args = append(args, *maxPages)
		argIndex++
	}
	if minReviews != nil {
		updates = append(updates, fmt.Sprintf("min_reviews = $%d", argIndex))
		args = append(args, *minReviews)
		argIndex++
	}
	if minPrice != nil {
		updates = append(updates, fmt.Sprintf("min_price = $%d", argIndex))
		args = append(args, *minPrice)
		argIndex++
	}
	if maxPrice != nil {
		updates = append(updates, fmt.Sprintf("max_price = $%d", argIndex))
		args = append(args, *maxPrice)
		argIndex++
	}
	if minStars != nil {
		updates = append(updates, fmt.Sprintf("min_stars = $%d", argIndex))
		args = append(args, *minStars)
		argIndex++
	}

	if len(updates) == 0 {
		return nil // Nothing to update
	}

	// Add updated_at and user_id
	updates = append(updates, fmt.Sprintf("updated_at = CURRENT_TIMESTAMP"))
	args = append(args, userID)

	query := fmt.Sprintf(`
		UPDATE user_configs
		SET %s
		WHERE user_id = $%d
	`, strings.Join(updates, ", "), argIndex)

	_, err := db.conn.Exec(query, args...)
	return err
}

