package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"bnb-fetcher/config"
	"bnb-fetcher/db"
	"bnb-fetcher/fetcher"
	"bnb-fetcher/filter"
	"bnb-fetcher/models"
	"bnb-fetcher/parser"
	"bnb-fetcher/sheets"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Scheduler processes scraping requests from the database
type Scheduler struct {
	db             *db.DB
	bot            *tgbotapi.BotAPI
	writer         *sheets.Writer
	spreadsheetURL string
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewScheduler creates a new scheduler (browser will be created on-demand)
func NewScheduler(database *db.DB, bot *tgbotapi.BotAPI, writer *sheets.Writer, spreadsheetURL string) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())

	return &Scheduler{
		db:             database,
		bot:            bot,
		writer:         writer,
		spreadsheetURL: spreadsheetURL,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start starts the scheduler in a goroutine
func (s *Scheduler) Start() {
	go s.run()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.cancel()
	log.Println("Scheduler stopped")
}

// run is the main scheduler loop
func (s *Scheduler) run() {
	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			log.Println("Scheduler stopped")
			return
		case <-ticker.C:
			s.processNextRequest()
		}
	}
}

// processNextRequest processes the next request with status 'created'
func (s *Scheduler) processNextRequest() {
	req, err := s.db.GetNextCreatedRequest()
	if err != nil {
		log.Printf("Error getting next request: %v\n", err)
		return
	}

	if req == nil {
		// No requests to process
		return
	}

	log.Printf("Processing request ID %d for user %d\n", req.ID, req.UserID)

	// Update status to 'in_progress'
	if err := s.db.UpdateRequestStatus(req.ID, "in_progress"); err != nil {
		log.Printf("Error updating request status to in_progress: %v\n", err)
		return
	}

	// Send status update to Telegram
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, "🔄 Processing request... Starting scraping...")

	// Get user config
	userConfig, err := s.db.GetUserConfig(req.UserID)
	if err != nil {
		log.Printf("Error getting user config: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// Convert user config to filter config
	cfg := &config.FilterConfig{}
	cfg.Filters.MinReviews = userConfig.MinReviews
	cfg.Filters.MinPrice = userConfig.MinPrice
	cfg.Filters.MaxPrice = userConfig.MaxPrice
	cfg.Filters.MinStars = userConfig.MinStars

	// Create browser only when needed (on-demand)
	log.Printf("Initializing browser for request ID %d...\n", req.ID)
	rodFetcher, err := fetcher.NewRodFetcher()
	if err != nil {
		log.Printf("Error creating fetcher: %v\n", err)
		s.handleRequestError(req, err)
		return
	}
	defer func() {
		log.Printf("Closing browser after request ID %d...\n", req.ID)
		if err := rodFetcher.Close(); err != nil {
			log.Printf("Warning: Failed to close browser: %v\n", err)
		} else {
			log.Printf("Browser closed successfully for request ID %d\n", req.ID)
		}
	}()

	fetcherInstance := fetcher.Fetcher(rodFetcher)

	// Fetch pages with status updates
	// We'll fetch page by page and send updates
	log.Printf("Using maxPages from user config: %d\n", userConfig.MaxPages)
	htmlPages, err := s.fetchWithUpdates(fetcherInstance, req.URL, userConfig.MaxPages, req.TelegramMessageID, req.UserID)
	if err != nil {
		log.Printf("Error scraping: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	if len(htmlPages) == 0 {
		err := fmt.Errorf("no HTML pages were collected")
		log.Printf("Error: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// Parse listings
	parserInstance := parser.NewParser()
	var allListings []models.Listing

	for i, html := range htmlPages {
		listings, err := parserInstance.ParseHTML(html)
		if err != nil {
			log.Printf("Warning: Failed to parse page %d: %v\n", i+1, err)
			continue
		}
		log.Printf("Parsed page %d: found %d listings\n", i+1, len(listings))
		// Set page number for each listing
		pageNumber := i + 1
		for j := range listings {
			listings[j].PageNumber = pageNumber
		}
		allListings = append(allListings, listings...)
	}
	
	log.Printf("Total listings parsed from all pages: %d\n", len(allListings))

	if len(allListings) == 0 {
		err := fmt.Errorf("no listings found in the fetched HTML")
		log.Printf("Error: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// Apply filters
	filterInstance := filter.NewFilter(cfg)
	filteredListings := filterInstance.ApplyFilters(allListings)

	// Save basic listings to database first (with status 'pending')
	// Store URL -> listingID mapping for later updates
	urlToIDMap := make(map[string]int)
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("💾 Saving %d listings to database...", len(filteredListings)))

	for _, listing := range filteredListings {
		var price *float64
		var currency *string
		var stars *float64
		var reviewCount *int

		if listing.Price > 0 {
			price = &listing.Price
		}
		if listing.Currency != "" {
			currency = &listing.Currency
		}
		if listing.Stars > 0 {
			stars = &listing.Stars
		}
		if listing.ReviewCount > 0 {
			reviewCount = &listing.ReviewCount
		}

		// Save basic listing with status 'pending'
		err := s.db.SaveListing(req.ID, listing.Title, listing.URL, price, currency, stars, reviewCount)
		if err != nil {
			log.Printf("Warning: Failed to save basic listing to database: %v\n", err)
			continue
		}

		// Get the listing ID we just created
		listingID, err := s.db.GetListingIDByURL(req.ID, listing.URL)
		if err != nil {
			log.Printf("Warning: Failed to get listing ID for URL %s: %v\n", listing.URL, err)
			continue
		}
		urlToIDMap[listing.URL] = listingID
	}

	log.Printf("Saved %d basic listings to database\n", len(urlToIDMap))

	// Fetch detail pages and enrich listings incrementally
	detailFetcher := fetcher.NewDetailFetcher(rodFetcher.GetBrowser())
	detailParser := parser.NewDetailParser()

	enrichedListings := make([]models.Listing, 0, len(filteredListings))
	successCount := 0
	failCount := 0

	for i, listing := range filteredListings {
		listingID, exists := urlToIDMap[listing.URL]
		if !exists {
			log.Printf("Warning: No listing ID found for URL: %s\n", listing.URL)
			failCount++
			continue
		}

		// Send status update
		s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("📄 Fetching details for listing %d/%d...", i+1, len(filteredListings)))
		log.Printf("Fetching detail page for listing %d/%d: %s\n", i+1, len(filteredListings), listing.URL)

		// Fetch detail page
		detailHTML, err := detailFetcher.FetchDetailPage(listing.URL)
		if err != nil {
			log.Printf("Warning: Failed to fetch detail page for %s: %v\n", listing.URL, err)
			s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("⚠️ Failed to fetch details for listing %d/%d", i+1, len(filteredListings)))
			failCount++
			// Update status to failed
			s.db.UpdateListingStatus(listingID, "failed")
			continue
		}

		// Parse detail page
		detailData, err := detailParser.ParseDetailPage(detailHTML)
		if err != nil {
			log.Printf("Warning: Failed to parse detail page for %s: %v\n", listing.URL, err)
			s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("⚠️ Failed to parse details for listing %d/%d", i+1, len(filteredListings)))
			failCount++
			continue
		}

		// Merge detail data with listing data
		listing.IsSuperhost = detailData.IsSuperhost
		listing.IsGuestFavorite = detailData.IsGuestFavorite
		listing.Bedrooms = detailData.Bedrooms
		listing.Bathrooms = detailData.Bathrooms
		listing.Beds = detailData.Beds
		listing.Description = detailData.Description
		listing.HouseRules = detailData.HouseRules
		listing.NewestReviewDate = detailData.NewestReviewDate
		listing.Reviews = detailData.Reviews

		// Update listing in database immediately
		var isSuperhost *bool
		var isGuestFavorite *bool
		var bedrooms *int
		var bathrooms *int
		var beds *int
		var description *string
		var houseRules *string
		var newestReviewDate *time.Time

		if listing.Bedrooms > 0 {
			bedrooms = &listing.Bedrooms
		}
		if listing.Bathrooms > 0 {
			bathrooms = &listing.Bathrooms
		}
		if listing.Beds > 0 {
			beds = &listing.Beds
		}
		isSuperhost = &listing.IsSuperhost
		isGuestFavorite = &listing.IsGuestFavorite
		if listing.Description != "" {
			description = &listing.Description
		}
		if listing.HouseRules != "" {
			houseRules = &listing.HouseRules
		}
		newestReviewDate = listing.NewestReviewDate

		err = s.db.UpdateListingDetails(listingID, isSuperhost, isGuestFavorite, bedrooms, bathrooms, beds, description, houseRules, newestReviewDate)
		if err != nil {
			log.Printf("Warning: Failed to update listing details for listing %d: %v\n", listingID, err)
		} else {
			log.Printf("Updated listing %d with detail information\n", listingID)
		}

		// Save reviews immediately
		if len(listing.Reviews) > 0 {
			log.Printf("Saving %d reviews for listing %d\n", len(listing.Reviews), listingID)
			if err := s.db.SaveReviews(listingID, listing.Reviews); err != nil {
				log.Printf("Error: Failed to save %d reviews for listing %d: %v\n", len(listing.Reviews), listingID, err)
			} else {
				log.Printf("Successfully saved %d reviews for listing %d\n", len(listing.Reviews), listingID)
			}
		} else {
			log.Printf("No reviews found for listing %d\n", listingID)
		}

		enrichedListings = append(enrichedListings, listing)
		successCount++

		// Send success status
		s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("✅ Processed listing %d/%d (Bedrooms: %d, Bathrooms: %d, Reviews: %d)", i+1, len(filteredListings), listing.Bedrooms, listing.Bathrooms, len(listing.Reviews)))

		// Add delay between detail page fetches
		if i < len(filteredListings)-1 {
			time.Sleep(3 * time.Second)
		}
	}

	log.Printf("Detail parsing complete: %d successful, %d failed\n", successCount, failCount)

	// Update request counts
	if err := s.db.UpdateRequestCounts(req.ID, len(filteredListings), len(htmlPages)); err != nil {
		log.Printf("Error updating request counts: %v\n", err)
	}

	// Create sheet name from request ID and timestamp
	sheetName := fmt.Sprintf("Request_%d_%s", req.ID, time.Now().Format("20060102_150405"))

	// Format filter information
	filterInfo := fmt.Sprintf("Min Reviews: %d, Min Price: %.2f, Max Price: %.2f, Min Stars: %.2f",
		cfg.Filters.MinReviews, cfg.Filters.MinPrice, cfg.Filters.MaxPrice, cfg.Filters.MinStars)

	// Write to Google Sheets (sheet will be inserted at the beginning)
	createdSheetName, sheetID, err := s.writer.CreateSheetAndWriteListings(sheetName, enrichedListings, req.URL, filterInfo)
	if err != nil {
		log.Printf("Error writing to Google Sheets: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// Update request with sheet name
	if err := s.db.UpdateRequestSheetName(req.ID, createdSheetName); err != nil {
		log.Printf("Warning: Failed to update sheet name: %v\n", err)
	}

	// Update status to 'done'
	if err := s.db.UpdateRequestStatus(req.ID, "done"); err != nil {
		log.Printf("Error updating request status to done: %v\n", err)
		return
	}

	// Create URL that opens the specific sheet
	sheetURL := s.createSheetURL(sheetID)

	// Send success message
	successMsg := fmt.Sprintf(
		"✅ Successfully fetched and added %d listings to Google Sheets!\n\n"+
			"Found %d listings before filtering.\n"+
			"Pages: %d fetched (requested: %d)\n\n"+
			"View spreadsheet: %s",
		len(filteredListings), len(allListings), len(htmlPages), userConfig.MaxPages, sheetURL)
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, successMsg)
}

// handleRequestError handles errors during request processing
func (s *Scheduler) handleRequestError(req *db.Request, err error) {
	if updateErr := s.db.UpdateRequestStatus(req.ID, "failed"); updateErr != nil {
		log.Printf("Error updating request status to failed: %v\n", updateErr)
	}

	errorMsg := fmt.Sprintf("❌ Error processing request: %v", err)
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, errorMsg)
}

// fetchWithUpdates fetches pages and sends status updates
func (s *Scheduler) fetchWithUpdates(fetcherInstance fetcher.Fetcher, url string, maxPages int, messageID int, userID int64) ([]string, error) {
	// For now, use the standard fetcher and send updates based on results
	// In a more advanced version, we could modify the fetcher to accept callbacks
	htmlPages, err := fetcherInstance.Fetch(url, maxPages)
	if err != nil {
		return nil, err
	}

	// Send status updates for each page
	for i := range htmlPages {
		s.sendStatusUpdate(messageID, userID, fmt.Sprintf("📄 Page %d/%d fetched", i+1, len(htmlPages)))
	}

	return htmlPages, nil
}

// createSheetURL creates a URL that opens a specific sheet in the spreadsheet
func (s *Scheduler) createSheetURL(sheetID int64) string {
	// Extract spreadsheet ID from the base URL
	spreadsheetID := sheets.ExtractSpreadsheetID(s.spreadsheetURL)
	if spreadsheetID == "" {
		// Fallback to original URL if we can't extract ID
		return s.spreadsheetURL
	}

	// Create URL with gid parameter to open specific sheet
	// Format: https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/edit#gid=SHEET_ID
	return fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/edit#gid=%d", spreadsheetID, sheetID)
}

// sendStatusUpdate sends a status update message to Telegram
func (s *Scheduler) sendStatusUpdate(messageID int, userID int64, text string) {
	msg := tgbotapi.NewMessage(userID, text)
	msg.ReplyToMessageID = messageID
	msg.ParseMode = "HTML"
	_, err := s.bot.Send(msg)
	if err != nil {
		log.Printf("Error sending status update: %v\n", err)
	}
}
