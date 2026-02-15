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
	ID                int
	UserID            int64
	TelegramMessageID int
	URL               string
	Status            string // "created", "in_progress", "done", "failed"
	ListingsCount     int
	PagesCount        int
	SheetName         sql.NullString
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Listing represents a fetched listing stored in database
type Listing struct {
	ID               int
	RequestID        int
	LinkNumber       sql.NullInt64 // Which search link this listing came from (1-based)
	Title            string
	URL              string
	Price            sql.NullFloat64
	Currency         sql.NullString
	Stars            sql.NullFloat64
	ReviewCount      sql.NullInt64
	Status           string // "pending", "saved", "failed"
	IsSuperhost      sql.NullBool
	IsGuestFavorite  sql.NullBool
	Bedrooms         sql.NullFloat64
	Bathrooms        sql.NullFloat64
	Beds             sql.NullFloat64
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

// SearchLink represents a single search URL within a multi-link request
type SearchLink struct {
	ID            int
	RequestID     int
	LinkNumber    int    // 1-based position in the input
	URL           string
	Status        string // "pending", "in_progress", "done", "failed"
	RetryCount    int
	ListingsCount int
	LastError     sql.NullString
	CreatedAt     time.Time
	UpdatedAt     time.Time
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
	return db.SaveListingWithStatus(requestID, title, url, price, currency, stars, reviewCount, "pending")
}

// SaveListingWithStatus saves a listing to the database with a specific status
func (db *DB) SaveListingWithStatus(requestID int, title, url string, price *float64, currency *string, stars *float64, reviewCount *int, status string) error {
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, requestID, title, url, priceVal, currencyVal, starsVal, reviewCountVal, status)
	return err
}

// UpdateListingStatus updates the status of a listing
func (db *DB) UpdateListingStatus(listingID int, status string) error {
	_, err := db.conn.Exec(`
		UPDATE listings
		SET status = $1
		WHERE id = $2
	`, status, listingID)
	return err
}

// SaveEnrichedListing saves a listing with all detail page fields to the database
// Returns the listing ID
func (db *DB) SaveEnrichedListing(requestID int, title, url string, price *float64, currency *string, stars *float64, reviewCount *int,
	isSuperhost *bool, isGuestFavorite *bool, bedrooms *float64, bathrooms *float64, beds *float64,
	description *string, houseRules *string, newestReviewDate *time.Time) (int, error) {
	var priceVal sql.NullFloat64
	var currencyVal sql.NullString
	var starsVal sql.NullFloat64
	var reviewCountVal sql.NullInt64
	var isSuperhostVal sql.NullBool
	var isGuestFavoriteVal sql.NullBool
	var bedroomsVal sql.NullFloat64
	var bathroomsVal sql.NullFloat64
	var bedsVal sql.NullFloat64
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
		bedroomsVal = sql.NullFloat64{Float64: *bedrooms, Valid: true}
	}
	if bathrooms != nil {
		bathroomsVal = sql.NullFloat64{Float64: *bathrooms, Valid: true}
	}
	if beds != nil {
		bedsVal = sql.NullFloat64{Float64: *beds, Valid: true}
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

// UpdateListingDetails updates an existing listing with detail page information
func (db *DB) UpdateListingDetails(listingID int, isSuperhost *bool, isGuestFavorite *bool, bedrooms *float64, bathrooms *float64, beds *float64,
	description *string, houseRules *string, newestReviewDate *time.Time) error {
	updates := []string{}
	args := []interface{}{}
	argIndex := 1

	if isSuperhost != nil {
		updates = append(updates, fmt.Sprintf("is_superhost = $%d", argIndex))
		args = append(args, *isSuperhost)
		argIndex++
	}
	if isGuestFavorite != nil {
		updates = append(updates, fmt.Sprintf("is_guest_favorite = $%d", argIndex))
		args = append(args, *isGuestFavorite)
		argIndex++
	}
	if bedrooms != nil {
		updates = append(updates, fmt.Sprintf("bedrooms = $%d", argIndex))
		args = append(args, *bedrooms)
		argIndex++
	}
	if bathrooms != nil {
		updates = append(updates, fmt.Sprintf("bathrooms = $%d", argIndex))
		args = append(args, *bathrooms)
		argIndex++
	}
	if beds != nil {
		updates = append(updates, fmt.Sprintf("beds = $%d", argIndex))
		args = append(args, *beds)
		argIndex++
	}
	if description != nil {
		updates = append(updates, fmt.Sprintf("description = $%d", argIndex))
		args = append(args, *description)
		argIndex++
	}
	if houseRules != nil {
		updates = append(updates, fmt.Sprintf("house_rules = $%d", argIndex))
		args = append(args, *houseRules)
		argIndex++
	}
	if newestReviewDate != nil {
		updates = append(updates, fmt.Sprintf("newest_review_date = $%d", argIndex))
		args = append(args, *newestReviewDate)
		argIndex++
	}

	if len(updates) == 0 {
		return nil // Nothing to update
	}

	// Update status to 'saved' and add listing ID
	updates = append(updates, "status = 'saved'")
	args = append(args, listingID)

	query := fmt.Sprintf(`
		UPDATE listings
		SET %s
		WHERE id = $%d
	`, strings.Join(updates, ", "), argIndex)

	_, err := db.conn.Exec(query, args...)
	return err
}

// GetListingIDByURL retrieves a listing ID by URL for a given request
func (db *DB) GetListingIDByURL(requestID int, url string) (int, error) {
	var listingID int
	err := db.conn.QueryRow(`
		SELECT id FROM listings
		WHERE request_id = $1 AND url = $2
		LIMIT 1
	`, requestID, url).Scan(&listingID)
	return listingID, err
}

// GetListingURLsAndLinkNumbers returns all listing URLs and their link numbers for a request (for resume deduplication)
func (db *DB) GetListingURLsAndLinkNumbers(requestID int) ([]struct{ URL string; LinkNumber int }, error) {
	rows, err := db.conn.Query(`
		SELECT url, COALESCE(link_number, 0) FROM listings WHERE request_id = $1
	`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []struct{ URL string; LinkNumber int }
	for rows.Next() {
		var url string
		var linkNum int
		if err := rows.Scan(&url, &linkNum); err != nil {
			return nil, err
		}
		result = append(result, struct{ URL string; LinkNumber int }{url, linkNum})
	}
	return result, rows.Err()
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
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO listing_reviews (listing_id, date, score, full_text, time_on_airbnb)
		VALUES ($1, $2, $3, $4, $5)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	savedCount := 0
	for _, review := range reviews {
		var scoreVal sql.NullFloat64
		var timeOnAirbnbVal sql.NullString

		if review.Score > 0 {
			scoreVal = sql.NullFloat64{Float64: review.Score, Valid: true}
		}
		if review.TimeOnAirbnb != "" {
			timeOnAirbnbVal = sql.NullString{String: review.TimeOnAirbnb, Valid: true}
		}

		// Ensure date is not zero
		reviewDate := review.Date
		if reviewDate.IsZero() {
			reviewDate = time.Now()
		}

		_, err := stmt.Exec(listingID, reviewDate, scoreVal, review.FullText, timeOnAirbnbVal)
		if err != nil {
			return fmt.Errorf("failed to insert review (listingID=%d, date=%v): %w", listingID, reviewDate, err)
		}
		savedCount++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
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

// ============================================================================
// Search Links Methods (Multi-Link Support)
// ============================================================================

// CreateSearchLinks creates multiple search links for a request
func (db *DB) CreateSearchLinks(requestID int, urls []string) ([]SearchLink, error) {
	links := make([]SearchLink, 0, len(urls))

	for i, url := range urls {
		var link SearchLink
		err := db.conn.QueryRow(`
			INSERT INTO search_links (request_id, link_number, url, status)
			VALUES ($1, $2, $3, 'pending')
			RETURNING id, request_id, link_number, url, status, retry_count, listings_count, last_error, created_at, updated_at
		`, requestID, i+1, url).Scan(
			&link.ID, &link.RequestID, &link.LinkNumber, &link.URL, &link.Status,
			&link.RetryCount, &link.ListingsCount, &link.LastError, &link.CreatedAt, &link.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create search link %d: %w", i+1, err)
		}
		links = append(links, link)
	}

	return links, nil
}

// GetSearchLinksByRequestID retrieves all search links for a request
func (db *DB) GetSearchLinksByRequestID(requestID int) ([]SearchLink, error) {
	rows, err := db.conn.Query(`
		SELECT id, request_id, link_number, url, status, retry_count, listings_count, last_error, created_at, updated_at
		FROM search_links
		WHERE request_id = $1
		ORDER BY link_number ASC
	`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []SearchLink
	for rows.Next() {
		var link SearchLink
		err := rows.Scan(
			&link.ID, &link.RequestID, &link.LinkNumber, &link.URL, &link.Status,
			&link.RetryCount, &link.ListingsCount, &link.LastError, &link.CreatedAt, &link.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}

	return links, rows.Err()
}

// UpdateSearchLinkStatus updates the status and optionally the last error of a search link
func (db *DB) UpdateSearchLinkStatus(linkID int, status string, lastError *string) error {
	var lastErrorVal sql.NullString
	if lastError != nil {
		lastErrorVal = sql.NullString{String: *lastError, Valid: true}
	}

	_, err := db.conn.Exec(`
		UPDATE search_links
		SET status = $1, last_error = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`, status, lastErrorVal, linkID)
	return err
}

// IncrementSearchLinkRetry increments the retry count and resets status to pending
func (db *DB) IncrementSearchLinkRetry(linkID int) error {
	_, err := db.conn.Exec(`
		UPDATE search_links
		SET retry_count = retry_count + 1, status = 'pending', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, linkID)
	return err
}

// UpdateSearchLinkListingsCount updates the listings count for a search link
func (db *DB) UpdateSearchLinkListingsCount(linkID int, count int) error {
	_, err := db.conn.Exec(`
		UPDATE search_links
		SET listings_count = $1, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, count, linkID)
	return err
}

// GetSearchLinkByID retrieves a search link by ID
func (db *DB) GetSearchLinkByID(linkID int) (*SearchLink, error) {
	var link SearchLink
	err := db.conn.QueryRow(`
		SELECT id, request_id, link_number, url, status, retry_count, listings_count, last_error, created_at, updated_at
		FROM search_links
		WHERE id = $1
	`, linkID).Scan(
		&link.ID, &link.RequestID, &link.LinkNumber, &link.URL, &link.Status,
		&link.RetryCount, &link.ListingsCount, &link.LastError, &link.CreatedAt, &link.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// ============================================================================
// Updated Listing Methods with LinkNumber Support
// ============================================================================

// SaveListingWithLinkNumber saves a listing to the database with link number
func (db *DB) SaveListingWithLinkNumber(requestID int, linkNumber int, title, url string, price *float64, currency *string, stars *float64, reviewCount *int) error {
	return db.SaveListingWithStatusAndLinkNumber(requestID, linkNumber, title, url, price, currency, stars, reviewCount, "pending")
}

// SaveListingWithStatusAndLinkNumber saves a listing to the database with a specific status and link number
func (db *DB) SaveListingWithStatusAndLinkNumber(requestID int, linkNumber int, title, url string, price *float64, currency *string, stars *float64, reviewCount *int, status string) error {
	var priceVal sql.NullFloat64
	var currencyVal sql.NullString
	var starsVal sql.NullFloat64
	var reviewCountVal sql.NullInt64
	var linkNumberVal sql.NullInt64

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
	if linkNumber > 0 {
		linkNumberVal = sql.NullInt64{Int64: int64(linkNumber), Valid: true}
	}

	_, err := db.conn.Exec(`
		INSERT INTO listings (request_id, link_number, title, url, price, currency, stars, review_count, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, requestID, linkNumberVal, title, url, priceVal, currencyVal, starsVal, reviewCountVal, status)
	return err
}

// SaveEnrichedListingWithLinkNumber saves a listing with all detail page fields and link number to the database
// Returns the listing ID
func (db *DB) SaveEnrichedListingWithLinkNumber(requestID int, linkNumber int, title, url string, price *float64, currency *string, stars *float64, reviewCount *int,
	isSuperhost *bool, isGuestFavorite *bool, bedrooms *float64, bathrooms *float64, beds *float64,
	description *string, houseRules *string, newestReviewDate *time.Time) (int, error) {
	var priceVal sql.NullFloat64
	var currencyVal sql.NullString
	var starsVal sql.NullFloat64
	var reviewCountVal sql.NullInt64
	var linkNumberVal sql.NullInt64
	var isSuperhostVal sql.NullBool
	var isGuestFavoriteVal sql.NullBool
	var bedroomsVal sql.NullFloat64
	var bathroomsVal sql.NullFloat64
	var bedsVal sql.NullFloat64
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
	if linkNumber > 0 {
		linkNumberVal = sql.NullInt64{Int64: int64(linkNumber), Valid: true}
	}
	if isSuperhost != nil {
		isSuperhostVal = sql.NullBool{Bool: *isSuperhost, Valid: true}
	}
	if isGuestFavorite != nil {
		isGuestFavoriteVal = sql.NullBool{Bool: *isGuestFavorite, Valid: true}
	}
	if bedrooms != nil {
		bedroomsVal = sql.NullFloat64{Float64: *bedrooms, Valid: true}
	}
	if bathrooms != nil {
		bathroomsVal = sql.NullFloat64{Float64: *bathrooms, Valid: true}
	}
	if beds != nil {
		bedsVal = sql.NullFloat64{Float64: *beds, Valid: true}
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
		INSERT INTO listings (request_id, link_number, title, url, price, currency, stars, review_count, 
			is_superhost, is_guest_favorite, bedrooms, bathrooms, beds, description, house_rules, newest_review_date, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'saved')
		RETURNING id
	`, requestID, linkNumberVal, title, url, priceVal, currencyVal, starsVal, reviewCountVal,
		isSuperhostVal, isGuestFavoriteVal, bedroomsVal, bathroomsVal, bedsVal,
		descriptionVal, houseRulesVal, newestReviewDateVal).Scan(&listingID)
	return listingID, err
}
