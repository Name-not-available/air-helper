package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"airbnb-scraper/config"
	"airbnb-scraper/db"
	"airbnb-scraper/filter"
	"airbnb-scraper/models"
	"airbnb-scraper/parser"
	"airbnb-scraper/scraper"
	"airbnb-scraper/sheets"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Scheduler processes scraping requests from the database
type Scheduler struct {
	db            *db.DB
	bot           *tgbotapi.BotAPI
	writer        *sheets.Writer
	spreadsheetURL string
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewScheduler creates a new scheduler
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

	// Create scraper
	rodScraper, err := scraper.NewRodScraper()
	if err != nil {
		log.Printf("Error creating scraper: %v\n", err)
		s.handleRequestError(req, err)
		return
	}
	defer func() {
		if err := rodScraper.Close(); err != nil {
			log.Printf("Warning: Failed to close browser: %v\n", err)
		}
	}()

	scraperInstance := scraper.Scraper(rodScraper)

	// Scrape pages with status updates
	// We'll scrape page by page and send updates
	htmlPages, err := s.scrapeWithUpdates(scraperInstance, req.URL, userConfig.MaxPages, req.TelegramMessageID, req.UserID)
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
		allListings = append(allListings, listings...)
	}

	if len(allListings) == 0 {
		err := fmt.Errorf("no listings found in the scraped HTML")
		log.Printf("Error: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// Apply filters
	filterInstance := filter.NewFilter(cfg)
	filteredListings := filterInstance.ApplyFilters(allListings)

	// Save listings to database
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

		if err := s.db.SaveListing(req.ID, listing.Title, listing.URL, price, currency, stars, reviewCount); err != nil {
			log.Printf("Warning: Failed to save listing to database: %v\n", err)
		}
	}

	// Update request counts
	if err := s.db.UpdateRequestCounts(req.ID, len(filteredListings), len(htmlPages)); err != nil {
		log.Printf("Error updating request counts: %v\n", err)
	}

	// Create sheet name from request ID and timestamp
	sheetName := fmt.Sprintf("Request_%d_%s", req.ID, time.Now().Format("20060102_150405"))

	// Write to Google Sheets
	createdSheetName, err := s.writer.CreateSheetAndWriteListings(sheetName, filteredListings)
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

	// Send success message
	successMsg := fmt.Sprintf(
		"✅ Successfully scraped and added %d listings to Google Sheets!\n\n"+
			"Found %d listings before filtering.\n"+
			"Pages scraped: %d\n\n"+
			"View spreadsheet: %s",
		len(filteredListings), len(allListings), len(htmlPages), s.spreadsheetURL)
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

// scrapeWithUpdates scrapes pages and sends status updates
func (s *Scheduler) scrapeWithUpdates(scraperInstance scraper.Scraper, url string, maxPages int, messageID int, userID int64) ([]string, error) {
	// For now, use the standard scraper and send updates based on results
	// In a more advanced version, we could modify the scraper to accept callbacks
	htmlPages, err := scraperInstance.Scrape(url, maxPages)
	if err != nil {
		return nil, err
	}

	// Send status updates for each page
	for i := range htmlPages {
		s.sendStatusUpdate(messageID, userID, fmt.Sprintf("📄 Page %d/%d scraped", i+1, len(htmlPages)))
	}

	return htmlPages, nil
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

