package scraper

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// RodScraper implements the Scraper interface using rod (headless browser)
type RodScraper struct {
	browser *rod.Browser
}

// NewRodScraper creates a new RodScraper instance
func NewRodScraper() (*RodScraper, error) {
	// Get user data directory from environment or use default
	// This should be mounted as a volume to use disk instead of memory
	userDataDir := os.Getenv("BOT_DATA_DIR")
	if userDataDir == "" {
		userDataDir = "/tmp/air-data" // Default to /tmp/air-data if not set
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		log.Printf("Warning: Failed to create bot data directory %s: %v\n", userDataDir, err)
		userDataDir = "" // Fall back to default if we can't create it
	}

	// Try to use system Chrome first, fallback to downloading Chromium
	launcher := launcher.New().
		Headless(true).
		Set("disable-blink-features", "AutomationControlled").
		NoSandbox(true).
		Leakless(false). // Disable leakless to avoid antivirus issues
		UserDataDir(userDataDir).
		// Additional flags for Linux compatibility
		Set("disable-dev-shm-usage").
		Set("disable-gpu").
		Set("no-first-run").
		Set("no-default-browser-check").
		Set("disable-extensions").
		Set("disable-background-networking").
		Set("disable-background-timer-throttling").
		Set("disable-renderer-backgrounding").
		Set("disable-backgrounding-occluded-windows").
		Set("disable-breakpad").
		Set("disable-client-side-phishing-detection").
		Set("disable-default-apps").
		Set("disable-hang-monitor").
		Set("disable-popup-blocking").
		Set("disable-prompt-on-repost").
		Set("disable-sync").
		Set("disable-translate").
		Set("metrics-recording-only").
		Set("mute-audio").
		Set("no-zygote").
		Set("safebrowsing-disable-auto-update").
		Set("enable-automation").
		Set("use-mock-keychain").
		// Memory optimization flags
		Set("memory-pressure-off").
		Set("disable-background-timer-throttling").
		Set("disable-backgrounding-occluded-windows").
		Set("disable-renderer-backgrounding").
		Set("disable-ipc-flooding-protection").
		Set("disable-features", "TranslateUI,BlinkGenPropertyTrees")

	// Try to find Chrome in common locations (Windows)
	chromePaths := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}

	username := os.Getenv("USERNAME")
	if username != "" {
		chromePaths = append(chromePaths, `C:\Users\`+username+`\AppData\Local\Google\Chrome\Application\chrome.exe`)
	}

	// Try Linux Chrome/Chromium paths
	linuxPaths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
	}

	// Check if running on Linux
	if os.Getenv("PATH") != "" {
		for _, path := range linuxPaths {
			if _, err := os.Stat(path); err == nil {
				launcher = launcher.Bin(path)
				break
			}
		}
	}

	// Check Windows paths
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			launcher = launcher.Bin(path)
			break
		}
	}

	browserURL, err := launcher.Launch()
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w\n\nNote: On Linux, you may need to install Chromium dependencies:\n  apt-get update && apt-get install -y chromium chromium-sandbox || yum install -y chromium", err)
	}

	browser := rod.New().ControlURL(browserURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to browser: %w", err)
	}

	return &RodScraper{
		browser: browser,
	}, nil
}

// Close closes the browser
func (rs *RodScraper) Close() error {
	if rs.browser != nil {
		return rs.browser.Close()
	}
	return nil
}

// Scrape implements the Scraper interface
func (rs *RodScraper) Scrape(url string, maxPages int) ([]string, error) {
	var htmlPages []string
	pageCount := 0

	log.Printf("Starting scrape with maxPages: %d\n", maxPages)

	// Create a new page
	page := rs.browser.MustPage()
	defer page.Close()

	// Navigate to the URL
	if err := page.Navigate(url); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}

	// Wait for page to load and listings to appear
	page.WaitLoad()
	time.Sleep(3 * time.Second) // Give JavaScript time to render

	// Try to wait for listing elements to appear
	page.Timeout(10 * time.Second).MustWaitStable()

	// Get HTML content
	html, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("failed to get HTML: %w", err)
	}
	htmlPages = append(htmlPages, html)
	pageCount++
	log.Printf("Scraped page %d/%d\n", pageCount, maxPages)

	// Handle pagination
	for pageCount < maxPages {
		// Look for "Next" button or pagination link
		nextButton, err := page.Timeout(5 * time.Second).Element("a[aria-label='Next'], button[aria-label='Next'], a[href*='items_offset']")
		if err != nil {
			// No next button found, stop pagination
			log.Printf("No more pages found after page %d\n", pageCount)
			break
		}

		// Check if button is visible and enabled
		visible, _ := nextButton.Visible()
		if !visible {
			log.Printf("Next button not visible, stopping pagination\n")
			break
		}

		// Click next button
		if err := nextButton.Click("left", 1); err != nil {
			log.Printf("Failed to click next button: %v\n", err)
			break
		}

		// Wait for new content to load
		page.WaitLoad()
		time.Sleep(3 * time.Second)
		page.Timeout(10 * time.Second).MustWaitStable()

		// Get HTML content
		html, err := page.HTML()
		if err != nil {
			log.Printf("Failed to get HTML for page %d: %v\n", pageCount+1, err)
			break
		}
		htmlPages = append(htmlPages, html)
		pageCount++
		log.Printf("Scraped page %d/%d\n", pageCount, maxPages)
	}

	log.Printf("Scraping completed. Total pages scraped: %d (requested: %d)\n", len(htmlPages), maxPages)

	if len(htmlPages) == 0 {
		log.Println("Warning: No HTML pages collected.")
	}

	return htmlPages, nil
}
