package fetcher

// Fetcher interface defines the contract for fetching implementations
type Fetcher interface {
	// Fetch retrieves HTML content from the given URL and returns HTML strings
	// maxPages specifies the maximum number of pages to fetch
	Fetch(url string, maxPages int) ([]string, error)
}




