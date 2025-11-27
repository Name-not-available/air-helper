package scheduler

import (
	"context"
	"fmt"
	"log"
	"math"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
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

func formatRoomCount(value float64) string {
	if value <= 0 {
		return "-"
	}
	if math.Abs(value-math.Round(value)) < 0.001 {
		return fmt.Sprintf("%.0f", value)
	}
	formatted := fmt.Sprintf("%.2f", value)
	formatted = strings.TrimRight(formatted, "0")
	return strings.TrimRight(formatted, ".")
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

	defer releaseMemory()

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
	pagesFetched := len(htmlPages)

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
		htmlPages[i] = "" // release HTML string promptly
	}
	htmlPages = nil

	totalListings := len(allListings)
	log.Printf("Total listings parsed from all pages: %d\n", totalListings)

	if len(allListings) == 0 {
		err := fmt.Errorf("no listings found in the fetched HTML")
		log.Printf("Error: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// Apply filters
	filterInstance := filter.NewFilter(cfg)
	filteredListings := filterInstance.ApplyFilters(allListings)
	allListings = nil
	filteredCount := len(filteredListings)

	// Save basic listings to database first (with status 'pending')
	// Store URL -> listingID mapping for later updates
	urlToIDMap := make(map[string]int)
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("💾 Saving %d listings to database...", filteredCount))

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

	// Fetch detail pages and enrich listings in parallel using worker pool
	// Use 4 workers for parallel processing (can be adjusted based on performance)
	numWorkers := 4
	if filteredCount < numWorkers {
		numWorkers = filteredCount
	}

	detailFetcher := fetcher.NewDetailFetcher(rodFetcher.GetBrowser())
	detailParser := parser.NewDetailParser()

	// Create channels for work distribution
	jobs := make(chan struct {
		index    int
		listing  models.Listing
		listingID int
	}, filteredCount)
	results := make(chan struct {
		index    int
		listing  models.Listing
		success  bool
		err      error
	}, filteredCount)

	// Create worker pool
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				// Fetch detail page
				detailHTML, err := detailFetcher.FetchDetailPage(job.listing.URL)
				if err != nil {
					log.Printf("Worker %d: Failed to fetch detail page for %s: %v\n", workerID, job.listing.URL, err)
					results <- struct {
						index    int
						listing  models.Listing
						success  bool
						err      error
					}{job.index, job.listing, false, err}
					s.db.UpdateListingStatus(job.listingID, "failed")
					continue
				}

				// Parse detail page
				detailData, err := detailParser.ParseDetailPage(detailHTML)
				detailHTML = "" // Release memory immediately
				if err != nil {
					log.Printf("Worker %d: Failed to parse detail page for %s: %v\n", workerID, job.listing.URL, err)
					results <- struct {
						index    int
						listing  models.Listing
						success  bool
						err      error
					}{job.index, job.listing, false, err}
					continue
				}

				// Merge detail data with listing data
				job.listing.IsSuperhost = detailData.IsSuperhost
				job.listing.IsGuestFavorite = detailData.IsGuestFavorite
				job.listing.Bedrooms = detailData.Bedrooms
				job.listing.Bathrooms = detailData.Bathrooms
				job.listing.Beds = detailData.Beds
				job.listing.Description = detailData.Description
				job.listing.HouseRules = detailData.HouseRules
				job.listing.NewestReviewDate = detailData.NewestReviewDate
				job.listing.Reviews = detailData.Reviews

				// Update listing in database
				var isSuperhost *bool
				var isGuestFavorite *bool
				var bedrooms *float64
				var bathrooms *float64
				var beds *float64
				var description *string
				var houseRules *string
				var newestReviewDate *time.Time

				if job.listing.Bedrooms > 0 {
					bedrooms = &job.listing.Bedrooms
				}
				if job.listing.Bathrooms > 0 {
					bathrooms = &job.listing.Bathrooms
				}
				if job.listing.Beds > 0 {
					beds = &job.listing.Beds
				}
				isSuperhost = &job.listing.IsSuperhost
				isGuestFavorite = &job.listing.IsGuestFavorite
				if job.listing.Description != "" {
					description = &job.listing.Description
				}
				if job.listing.HouseRules != "" {
					houseRules = &job.listing.HouseRules
				}
				newestReviewDate = job.listing.NewestReviewDate

				err = s.db.UpdateListingDetails(job.listingID, isSuperhost, isGuestFavorite, bedrooms, bathrooms, beds, description, houseRules, newestReviewDate)
				if err != nil {
					log.Printf("Worker %d: Failed to update listing details for listing %d: %v\n", workerID, job.listingID, err)
				}

				// Save reviews
				if len(job.listing.Reviews) > 0 {
					if err := s.db.SaveReviews(job.listingID, job.listing.Reviews); err != nil {
						log.Printf("Worker %d: Failed to save reviews for listing %d: %v\n", workerID, job.listingID, err)
					}
				}
				// Release review memory
				job.listing.Reviews = nil

				results <- struct {
					index    int
					listing  models.Listing
					success  bool
					err      error
				}{job.index, job.listing, true, nil}
			}
		}(w)
	}

	// Send jobs to workers
	go func() {
		defer close(jobs)
		for i, listing := range filteredListings {
			listingID, exists := urlToIDMap[listing.URL]
			if !exists {
				log.Printf("Warning: No listing ID found for URL: %s\n", listing.URL)
				results <- struct {
					index    int
					listing  models.Listing
					success  bool
					err      error
				}{i, listing, false, fmt.Errorf("no listing ID found")}
				continue
			}
			jobs <- struct {
				index    int
				listing  models.Listing
				listingID int
			}{i, listing, listingID}
		}
	}()

	// Close results channel when all workers are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and maintain order
	enrichedListings := make([]models.Listing, filteredCount)
	successCount := 0
	failCount := 0
	processedCount := 0

	// Process results as they come in
	for result := range results {
		processedCount++
		if result.success {
			enrichedListings[result.index] = result.listing
			successCount++
			// Send status update every 5 listings or on completion
			if processedCount%5 == 0 || processedCount == filteredCount {
				s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("📄 Processed %d/%d listings...", processedCount, filteredCount))
			}
		} else {
			failCount++
			log.Printf("Failed to process listing %d: %v\n", result.index+1, result.err)
		}
	}

	// Filter out empty listings (failed ones)
	finalListings := make([]models.Listing, 0, successCount)
	for _, listing := range enrichedListings {
		if listing.URL != "" {
			finalListings = append(finalListings, listing)
		}
	}
	enrichedListings = finalListings

	urlToIDMap = nil

	log.Printf("Detail parsing complete: %d successful, %d failed\n", successCount, failCount)
	filteredListings = nil

	// Update request counts
	if err := s.db.UpdateRequestCounts(req.ID, filteredCount, pagesFetched); err != nil {
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
	enrichedListings = nil

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
		filteredCount, totalListings, pagesFetched, userConfig.MaxPages, sheetURL)
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

func releaseMemory() {
	runtime.GC()
	debug.FreeOSMemory()
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
