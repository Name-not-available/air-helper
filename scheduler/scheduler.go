package scheduler

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
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
	activeRequests int
	requestsMutex  sync.Mutex
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

// incrementActiveRequest increments the active request counter
func (s *Scheduler) incrementActiveRequest() {
	s.requestsMutex.Lock()
	s.activeRequests++
	activeCount := s.activeRequests
	s.requestsMutex.Unlock()
	log.Printf("Active requests: %d\n", activeCount)
}

// decrementActiveRequest decrements the active request counter and checks if restart is needed
func (s *Scheduler) decrementActiveRequest() {
	s.requestsMutex.Lock()
	s.activeRequests--
	activeCount := s.activeRequests
	s.requestsMutex.Unlock()
	log.Printf("Active requests: %d\n", activeCount)

	// If no active requests, trigger restart after a short delay to ensure cleanup
	if activeCount == 0 {
		log.Println("No active requests remaining. Scheduling restart in 2 seconds...")
		go func() {
			time.Sleep(2 * time.Second)
			// Double-check no new requests started
			s.requestsMutex.Lock()
			stillZero := s.activeRequests == 0
			s.requestsMutex.Unlock()
			if stillZero {
				s.requestRestart()
			}
		}()
	}
}

// requestRestart exits the process to allow process manager to restart it
func (s *Scheduler) requestRestart() {
	log.Println("🔄 Restarting service to clean up memory...")
	log.Println("Process will exit and be restarted by process manager")
	// Give a moment for logs to flush
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
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

	// Increment active request counter
	s.incrementActiveRequest()
	defer s.decrementActiveRequest()
	defer releaseMemory()

	log.Printf("Processing request ID %d for user %d\n", req.ID, req.UserID)

	// Update status to 'in_progress'
	if err := s.db.UpdateRequestStatus(req.ID, "in_progress"); err != nil {
		log.Printf("Error updating request status to in_progress: %v\n", err)
		return
	}

	// Get search links for this request
	searchLinks, err := s.db.GetSearchLinksByRequestID(req.ID)
	if err != nil {
		log.Printf("Error getting search links: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// If no search links exist (legacy request), create one from the URL
	if len(searchLinks) == 0 {
		log.Printf("No search links found, creating one from request URL\n")
		searchLinks, err = s.db.CreateSearchLinks(req.ID, []string{req.URL})
		if err != nil {
			log.Printf("Error creating search link: %v\n", err)
			s.handleRequestError(req, err)
			return
		}
	}

	totalLinks := len(searchLinks)
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, fmt.Sprintf("🔄 Processing request with %d link(s)...", totalLinks))

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
	filterInstance := filter.NewFilter(cfg)
	parserInstance := parser.NewParser()
	detailFetcher := fetcher.NewDetailFetcher(rodFetcher.GetBrowser())
	detailParser := parser.NewDetailParser()

	// Track seen listing URLs across all links for deduplication
	seenListingURLs := make(map[string]int) // URL -> link number that first found it

	// Collect all listings from all links
	var allEnrichedListings []models.Listing
	var allUnfilteredListings []models.Listing
	var totalListingsBeforeFilter int
	var totalPagesFetched int

	// Create retry queue from search links
	type queueItem struct {
		link       db.SearchLink
		retryCount int
	}
	queue := make([]queueItem, 0, len(searchLinks))
	for _, link := range searchLinks {
		queue = append(queue, queueItem{link: link, retryCount: link.RetryCount})
	}

	// Track statistics
	linksSuccessful := 0
	linksFailed := 0

	// Process links with retry queue
	for len(queue) > 0 {
		// Pop first item from queue
		item := queue[0]
		queue = queue[1:]
		link := item.link

		// Check if this is a retry and we need to wait
		if item.retryCount > 0 {
			waitMinutes := 3 + (item.retryCount-1) // 3 min for first retry, 4 for second, 5 for third
			if waitMinutes > 5 {
				waitMinutes = 5
			}
			s.sendStatusUpdate(req.TelegramMessageID, req.UserID, 
				fmt.Sprintf("⏳ Waiting %d minutes before retrying link %d...", waitMinutes, link.LinkNumber))
			log.Printf("Waiting %d minutes before retrying link %d\n", waitMinutes, link.LinkNumber)
			time.Sleep(time.Duration(waitMinutes) * time.Minute)
		}

		// Notify user we're starting this link
		shortURL := shortenURL(link.URL)
		if item.retryCount > 0 {
			s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
				fmt.Sprintf("🔄 Retrying link %d/%d (attempt %d/3): %s", link.LinkNumber, totalLinks, item.retryCount+1, shortURL))
		} else {
			s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
				fmt.Sprintf("🔗 Starting link %d/%d: %s", link.LinkNumber, totalLinks, shortURL))
		}

		// Update link status to in_progress
		if err := s.db.UpdateSearchLinkStatus(link.ID, "in_progress", nil); err != nil {
			log.Printf("Error updating search link status: %v\n", err)
		}

		// Process this link
		linkListings, linkUnfiltered, pagesFetched, listingsBeforeFilter, linkErr := s.processSearchLink(
			req, link, userConfig, fetcherInstance, filterInstance, parserInstance,
			detailFetcher, detailParser, seenListingURLs, cfg,
		)

		if linkErr != nil {
			errStr := linkErr.Error()
			log.Printf("Link %d failed: %v\n", link.LinkNumber, linkErr)

			// Check if we should retry
			if item.retryCount < 3 {
				// Push to end of queue for retry
				if err := s.db.IncrementSearchLinkRetry(link.ID); err != nil {
					log.Printf("Error incrementing retry count: %v\n", err)
				}
				// Update link from DB to get new retry count
				updatedLink, _ := s.db.GetSearchLinkByID(link.ID)
				if updatedLink != nil {
					queue = append(queue, queueItem{link: *updatedLink, retryCount: updatedLink.RetryCount})
				} else {
					item.retryCount++
					queue = append(queue, queueItem{link: link, retryCount: item.retryCount})
				}
				s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
					fmt.Sprintf("⚠️ Link %d failed, will retry later (attempt %d/3): %s", 
						link.LinkNumber, item.retryCount+1, truncateError(errStr)))
			} else {
				// Max retries reached, mark as permanently failed
				if err := s.db.UpdateSearchLinkStatus(link.ID, "failed", &errStr); err != nil {
					log.Printf("Error updating search link status to failed: %v\n", err)
				}
				linksFailed++
				s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
					fmt.Sprintf("❌ Link %d permanently failed after 3 attempts: %s", 
						link.LinkNumber, truncateError(errStr)))
			}
		} else {
			// Success!
			if err := s.db.UpdateSearchLinkStatus(link.ID, "done", nil); err != nil {
				log.Printf("Error updating search link status to done: %v\n", err)
			}
			if err := s.db.UpdateSearchLinkListingsCount(link.ID, len(linkListings)); err != nil {
				log.Printf("Error updating search link listings count: %v\n", err)
			}

			linksSuccessful++
			totalPagesFetched += pagesFetched
			totalListingsBeforeFilter += listingsBeforeFilter

			allEnrichedListings = append(allEnrichedListings, linkListings...)
			allUnfilteredListings = append(allUnfilteredListings, linkUnfiltered...)

			s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
				fmt.Sprintf("✅ Link %d completed: %d listings found (%d new after dedup)", 
					link.LinkNumber, listingsBeforeFilter, len(linkListings)))
		}
	}

	// All links processed (or permanently failed)
	totalFilteredListings := len(allEnrichedListings)

	if totalFilteredListings == 0 && linksSuccessful == 0 {
		err := fmt.Errorf("all %d links failed to process", totalLinks)
		log.Printf("Error: %v\n", err)
		s.handleRequestError(req, err)
		return
	}

	// Update request counts
	if err := s.db.UpdateRequestCounts(req.ID, totalFilteredListings, totalPagesFetched); err != nil {
		log.Printf("Error updating request counts: %v\n", err)
	}

	// Create sheet name from request ID and timestamp
	sheetName := fmt.Sprintf("Request_%d_%s", req.ID, time.Now().Format("20060102_150405"))

	// Format filter information
	filterInfo := fmt.Sprintf("Min Reviews: %d, Min Price: %.2f, Max Price: %.2f, Min Stars: %.2f",
		cfg.Filters.MinReviews, cfg.Filters.MinPrice, cfg.Filters.MaxPrice, cfg.Filters.MinStars)

	// Get first URL for metadata (or combine for multi-link)
	metadataURL := req.URL
	if totalLinks > 1 {
		metadataURL = fmt.Sprintf("%d links - see Link # column", totalLinks)
	}

	// Write to Google Sheets (sheet will be inserted at the beginning)
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, "📊 Writing to Google Sheets...")
	createdSheetName, sheetID, err := s.writer.CreateSheetAndWriteListings(sheetName, allEnrichedListings, allUnfilteredListings, metadataURL, filterInfo)
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
	var successMsg string
	if totalLinks == 1 {
		successMsg = fmt.Sprintf(
			"✅ Successfully fetched and added %d listings to Google Sheets!\n\n"+
				"Found %d listings before filtering.\n"+
				"Pages: %d fetched (requested: %d)\n\n"+
				"View spreadsheet: %s",
			totalFilteredListings, totalListingsBeforeFilter, totalPagesFetched, userConfig.MaxPages, sheetURL)
	} else {
		successMsg = fmt.Sprintf(
			"✅ Completed processing %d links!\n\n"+
				"Links: %d successful, %d failed\n"+
				"Listings: %d after filtering (from %d total)\n"+
				"Pages: %d fetched\n\n"+
				"View spreadsheet: %s",
			totalLinks, linksSuccessful, linksFailed,
			totalFilteredListings, totalListingsBeforeFilter, totalPagesFetched, sheetURL)
	}
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID, successMsg)
}

// processSearchLink processes a single search link and returns the enriched listings
func (s *Scheduler) processSearchLink(
	req *db.Request,
	link db.SearchLink,
	userConfig *db.UserConfig,
	fetcherInstance fetcher.Fetcher,
	filterInstance *filter.Filter,
	parserInstance *parser.Parser,
	detailFetcher *fetcher.DetailFetcher,
	detailParser *parser.DetailParser,
	seenListingURLs map[string]int, // Shared across links for deduplication
	cfg *config.FilterConfig,
) (enrichedListings []models.Listing, unfilteredListings []models.Listing, pagesFetched int, totalListings int, err error) {

	// Fetch pages for this link
	log.Printf("Fetching link %d: %s (maxPages: %d)\n", link.LinkNumber, shortenURL(link.URL), userConfig.MaxPages)
	htmlPages, err := fetcherInstance.Fetch(link.URL, userConfig.MaxPages)
	if err != nil {
		return nil, nil, 0, 0, fmt.Errorf("fetch failed: %w", err)
	}
	pagesFetched = len(htmlPages)

	if len(htmlPages) == 0 {
		return nil, nil, 0, 0, fmt.Errorf("no HTML pages collected")
	}

	// Parse listings
	var allListings []models.Listing
	for i, html := range htmlPages {
		pageNum := i + 1
		log.Printf("Link %d: Parsing page %d/%d\n", link.LinkNumber, pageNum, pagesFetched)
		
		// Send update every 10 pages
		if pageNum%10 == 0 || pageNum == 1 {
			s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
				fmt.Sprintf("📄 Link %d: Parsing page %d/%d...", link.LinkNumber, pageNum, pagesFetched))
		}
		
		listings, err := parserInstance.ParseHTML(html)
		if err != nil {
			log.Printf("Warning: Failed to parse page %d: %v\n", pageNum, err)
			continue
		}
		log.Printf("Link %d: Parsed page %d: found %d listings\n", link.LinkNumber, pageNum, len(listings))
		
		// Set page number and link number for each listing
		for j := range listings {
			listings[j].PageNumber = pageNum
			listings[j].LinkNumber = link.LinkNumber
		}
		allListings = append(allListings, listings...)
		htmlPages[i] = "" // release HTML
	}
	htmlPages = nil

	totalListings = len(allListings)
	log.Printf("Link %d: Total listings parsed: %d\n", link.LinkNumber, totalListings)

	if len(allListings) == 0 {
		return nil, nil, pagesFetched, 0, fmt.Errorf("no listings found")
	}

	// Apply filters
	filteredListings := filterInstance.ApplyFilters(allListings)
	
	// Deduplicate against already seen listings
	uniqueFilteredListings := make([]models.Listing, 0, len(filteredListings))
	for _, listing := range filteredListings {
		if _, seen := seenListingURLs[listing.URL]; !seen {
			seenListingURLs[listing.URL] = link.LinkNumber
			uniqueFilteredListings = append(uniqueFilteredListings, listing)
		} else {
			log.Printf("Link %d: Skipping duplicate listing (first seen in link %d): %s\n", 
				link.LinkNumber, seenListingURLs[listing.URL], extractURLPath(listing.URL))
		}
	}
	filteredListings = uniqueFilteredListings

	filteredCount := len(filteredListings)
	log.Printf("Link %d: %d listings after filtering and deduplication\n", link.LinkNumber, filteredCount)

	// Create map for unfiltered (but still need to dedupe)
	filteredURLs := make(map[string]bool, filteredCount)
	for _, listing := range filteredListings {
		filteredURLs[listing.URL] = true
	}

	// Keep unfiltered listings (deduplicated)
	for _, listing := range allListings {
		if !filteredURLs[listing.URL] {
			if _, seen := seenListingURLs[listing.URL]; !seen {
				seenListingURLs[listing.URL] = link.LinkNumber
				listing.LinkNumber = link.LinkNumber
				unfilteredListings = append(unfilteredListings, listing)
			}
		}
	}

	if filteredCount == 0 {
		// No filtered listings, but that's not an error
		s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
			fmt.Sprintf("📋 Link %d: %d listings parsed, 0 matched filters", link.LinkNumber, totalListings))
		return nil, unfilteredListings, pagesFetched, totalListings, nil
	}

	// Notify about filtering results
	s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
		fmt.Sprintf("📋 Link %d: %d listings parsed, %d matched filters. Enriching details...", 
			link.LinkNumber, totalListings, filteredCount))

	// Save basic listings to database
	urlToIDMap := make(map[string]int)
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

		// Save basic listing with link number
		err := s.db.SaveListingWithLinkNumber(req.ID, link.LinkNumber, listing.Title, listing.URL, price, currency, stars, reviewCount)
		if err != nil {
			log.Printf("Warning: Failed to save listing to database: %v\n", err)
			continue
		}

		listingID, err := s.db.GetListingIDByURL(req.ID, listing.URL)
		if err != nil {
			log.Printf("Warning: Failed to get listing ID: %v\n", err)
			continue
		}
		urlToIDMap[listing.URL] = listingID
	}

	// Enrich listings with detail pages
	enrichedListings = s.enrichListings(filteredListings, urlToIDMap, detailFetcher, detailParser, req, link.LinkNumber)

	return enrichedListings, unfilteredListings, pagesFetched, totalListings, nil
}

// enrichListings fetches detail pages and enriches listings
func (s *Scheduler) enrichListings(
	listings []models.Listing,
	urlToIDMap map[string]int,
	detailFetcher *fetcher.DetailFetcher,
	detailParser *parser.DetailParser,
	req *db.Request,
	linkNumber int,
) []models.Listing {
	filteredCount := len(listings)
	if filteredCount == 0 {
		return nil
	}

	// Use 2 workers for rate limiting
	numWorkers := 2
	if filteredCount < numWorkers {
		numWorkers = filteredCount
	}

	// Create channels
	jobs := make(chan struct {
		index     int
		listing   models.Listing
		listingID int
	}, filteredCount)
	results := make(chan struct {
		index   int
		listing models.Listing
		success bool
		err     error
	}, filteredCount)

	// Create worker pool
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				detailHTML, err := detailFetcher.FetchDetailPage(job.listing.URL)
				if err != nil {
					log.Printf("Worker %d: Failed to fetch detail page: %v\n", workerID, err)
					results <- struct {
						index   int
						listing models.Listing
						success bool
						err     error
					}{job.index, job.listing, false, err}
					s.db.UpdateListingStatus(job.listingID, "failed")
					continue
				}

				detailData, err := detailParser.ParseDetailPage(detailHTML)
				detailHTML = ""
				if err != nil {
					log.Printf("Worker %d: Failed to parse detail page: %v\n", workerID, err)
					results <- struct {
						index   int
						listing models.Listing
						success bool
						err     error
					}{job.index, job.listing, false, err}
					continue
				}

				// Merge detail data
				job.listing.IsSuperhost = detailData.IsSuperhost
				job.listing.IsGuestFavorite = detailData.IsGuestFavorite
				job.listing.Bedrooms = detailData.Bedrooms
				job.listing.Bathrooms = detailData.Bathrooms
				job.listing.Beds = detailData.Beds
				job.listing.Description = detailData.Description
				job.listing.HouseRules = detailData.HouseRules
				job.listing.NewestReviewDate = detailData.NewestReviewDate
				job.listing.Reviews = detailData.Reviews

				// Update database
				var isSuperhost, isGuestFavorite *bool
				var bedrooms, bathrooms, beds *float64
				var description, houseRules *string
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

				s.db.UpdateListingDetails(job.listingID, isSuperhost, isGuestFavorite, bedrooms, bathrooms, beds, description, houseRules, newestReviewDate)

				if len(job.listing.Reviews) > 0 {
					s.db.SaveReviews(job.listingID, job.listing.Reviews)
				}
				job.listing.Reviews = nil

				results <- struct {
					index   int
					listing models.Listing
					success bool
					err     error
				}{job.index, job.listing, true, nil}
			}
		}(w)
	}

	// Rate limiter
	rateLimiter := time.NewTicker(4 * time.Second)
	defer rateLimiter.Stop()

	// Send jobs
	go func() {
		defer close(jobs)
		for i, listing := range listings {
			listingID, exists := urlToIDMap[listing.URL]
			if !exists {
				results <- struct {
					index   int
					listing models.Listing
					success bool
					err     error
				}{i, listing, false, fmt.Errorf("no listing ID")}
				continue
			}
			if i > 0 {
				<-rateLimiter.C
			}
			jobs <- struct {
				index     int
				listing   models.Listing
				listingID int
			}{i, listing, listingID}
		}
	}()

	// Close results when done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results with progress updates
	enrichedListings := make([]models.Listing, filteredCount)
	processedCount := 0
	for result := range results {
		processedCount++
		if result.success {
			enrichedListings[result.index] = result.listing
		}
		
		// Send update every 20 listings or on completion
		if processedCount%20 == 0 || processedCount == filteredCount {
			s.sendStatusUpdate(req.TelegramMessageID, req.UserID,
				fmt.Sprintf("🔍 Link %d: Enriched %d/%d listings...", linkNumber, processedCount, filteredCount))
		}
	}

	// Filter out empty (failed) listings
	finalListings := make([]models.Listing, 0)
	for _, listing := range enrichedListings {
		if listing.URL != "" {
			finalListings = append(finalListings, listing)
		}
	}

	return finalListings
}

// shortenURL creates a shortened display version of a URL
func shortenURL(urlStr string) string {
	// Remove protocol
	if idx := strings.Index(urlStr, "://"); idx >= 0 {
		urlStr = urlStr[idx+3:]
	}
	// Remove www.
	urlStr = strings.TrimPrefix(urlStr, "www.")
	// Truncate if too long
	if len(urlStr) > 50 {
		urlStr = urlStr[:47] + "..."
	}
	return urlStr
}

// truncateError truncates error message for display
func truncateError(errStr string) string {
	if len(errStr) > 100 {
		return errStr[:97] + "..."
	}
	return errStr
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

// extractURLPath extracts the path from a URL, removing the domain
func extractURLPath(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	// Try to parse as URL
	if idx := strings.Index(urlStr, "://"); idx >= 0 {
		if pathIdx := strings.Index(urlStr[idx+3:], "/"); pathIdx >= 0 {
			return urlStr[idx+3+pathIdx:]
		}
		return "/"
	}
	// If no protocol, assume it's already a path
	if strings.HasPrefix(urlStr, "/") {
		return urlStr
	}
	return urlStr
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
