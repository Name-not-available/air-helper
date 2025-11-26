package fetcher

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

// CollyFetcher implements the Fetcher interface using colly
type CollyFetcher struct {
	collector *colly.Collector
}

// NewCollyFetcher creates a new CollyFetcher instance
func NewCollyFetcher() *CollyFetcher {
	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
	)

	// Set rate limiting - 3-5 seconds between requests
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*bnb.*",
		Parallelism: 1,
		Delay:       4 * time.Second, // 4 seconds average (between 3-5)
	})

	// Set error handler
	c.OnError(func(r *colly.Response, err error) {
		log.Printf("Error fetching %s: %v\n", r.Request.URL, err)
	})

	return &CollyFetcher{
		collector: c,
	}
}

// Fetch implements the Fetcher interface
func (cf *CollyFetcher) Fetch(url string, maxPages int) ([]string, error) {
	var htmlPages []string
	pageCount := 0
	visited := make(map[string]bool)

	// Set up callback to collect HTML from response
	cf.collector.OnResponse(func(r *colly.Response) {
		urlStr := r.Request.URL.String()
		htmlContent := string(r.Body)

		// Check if we've already visited this URL to prevent duplicates
		if visited[urlStr] {
			log.Printf("Skipping duplicate URL: %s\n", urlStr)
			return
		}

		// Check if HTML content is duplicate (compare with last page)
		if len(htmlPages) > 0 && htmlContent == htmlPages[len(htmlPages)-1] {
			log.Printf("Skipping duplicate HTML content from URL: %s\n", urlStr)
			visited[urlStr] = true
			return
		}

		visited[urlStr] = true
		htmlPages = append(htmlPages, htmlContent)
		pageCount++
		log.Printf("Fetched page %d/%d: %s\n", pageCount, maxPages, urlStr)
	})

	// Visit the initial URL
	if err := cf.collector.Visit(url); err != nil {
		return nil, fmt.Errorf("failed to visit URL: %w", err)
	}

	// Handle pagination - look for page links inside the pagination nav
	// Visit all pagination links, but duplicates will be filtered by visited map
	cf.collector.OnHTML("nav[aria-label='Search results pagination'] a", func(e *colly.HTMLElement) {
		if pageCount >= maxPages {
			return
		}

		nextURL := e.Attr("href")
		if nextURL == "" {
			return
		}

		// Handle relative URLs
		if strings.HasPrefix(nextURL, "/") {
			nextURL = "https://www.airbnb.com" + nextURL
		}

		// Check if we've already visited this URL
		if visited[nextURL] {
			return
		}

		// Only visit if we haven't reached max pages
		if pageCount < maxPages {
			cf.collector.Visit(nextURL)
		}
	})

	// Wait for all requests to complete
	cf.collector.Wait()

	if len(htmlPages) == 0 {
		log.Println("Warning: No HTML pages collected. Bnb may be using JavaScript rendering.")
		log.Println("Consider upgrading to a headless browser implementation.")
	}

	log.Printf("Fetching completed. Total pages fetched: %d (requested: %d)\n", len(htmlPages), maxPages)

	return htmlPages, nil
}
