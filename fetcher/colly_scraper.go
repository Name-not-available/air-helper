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

	// Set rate limiting
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*bnb.*",
		Parallelism: 1,
		Delay:       2 * time.Second,
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
		htmlPages = append(htmlPages, string(r.Body))
		pageCount++
		visited[r.Request.URL.String()] = true
	})

	// Visit the initial URL
	if err := cf.collector.Visit(url); err != nil {
		return nil, fmt.Errorf("failed to visit URL: %w", err)
	}

	// Handle pagination - look for next page links
	cf.collector.OnHTML("a[aria-label='Next'], a[href*='items_offset']", func(e *colly.HTMLElement) {
		if pageCount >= maxPages {
			return
		}
		nextURL := e.Attr("href")
		if nextURL != "" {
			// Handle relative URLs
			if strings.HasPrefix(nextURL, "/") {
				nextURL = "https://www.airbnb.com" + nextURL
			}
			// Avoid visiting the same URL twice
			if !visited[nextURL] {
				cf.collector.Visit(nextURL)
			}
		}
	})

	// Wait for all requests to complete
	cf.collector.Wait()

	if len(htmlPages) == 0 {
		log.Println("Warning: No HTML pages collected. Bnb may be using JavaScript rendering.")
		log.Println("Consider upgrading to a headless browser implementation.")
	}

	return htmlPages, nil
}




