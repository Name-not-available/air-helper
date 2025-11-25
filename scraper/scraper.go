package scraper

// Scraper interface defines the contract for scraping implementations
type Scraper interface {
	// Scrape fetches HTML content from the given URL and returns HTML strings
	// maxPages specifies the maximum number of pages to scrape
	Scrape(url string, maxPages int) ([]string, error)
}




