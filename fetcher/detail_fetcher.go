package fetcher

import (
	"fmt"
	"log"
	"time"

	"github.com/go-rod/rod"
)

// DetailFetcher fetches detail pages for individual listings
type DetailFetcher struct {
	browser *rod.Browser
}

// NewDetailFetcher creates a new DetailFetcher using an existing browser
func NewDetailFetcher(browser *rod.Browser) *DetailFetcher {
	return &DetailFetcher{
		browser: browser,
	}
}

// FetchDetailPage fetches the HTML content of a single listing detail page
func (df *DetailFetcher) FetchDetailPage(url string) (string, error) {
	// Create a new page (use MustPage with panic recovery)
	var page *rod.Page
	var pageErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				pageErr = fmt.Errorf("panic while creating page: %v", r)
				log.Printf("Panic while creating page: %v\n", r)
			}
		}()
		page = df.browser.MustPage()
	}()
	if pageErr != nil {
		return "", pageErr
	}
	if page == nil {
		return "", fmt.Errorf("failed to create page")
	}
	defer page.Close()

	// Navigate to the URL
	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("failed to navigate: %w", err)
	}

	// Wait for page to load
	page.WaitLoad()

	// Reduced wait time - WaitStable already handles most of the rendering
	// Only wait 1 second for initial JS execution
	time.Sleep(1 * time.Second)

	// Wait for page to stabilize (this is more efficient than fixed sleeps)
	// Reduced timeout from 10s to 5s and stability check from 500ms to 300ms
	if err := page.Timeout(5 * time.Second).WaitStable(300 * time.Millisecond); err != nil {
		log.Printf("Warning: Detail page did not stabilize within timeout, continuing anyway: %v\n", err)
		// If WaitStable fails, give a minimal fallback wait
		time.Sleep(500 * time.Millisecond)
	}

	// Get HTML content
	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("failed to get HTML: %w", err)
	}

	return html, nil
}
