package fetcher

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
)

// RodFetcher implements the Fetcher interface using rod (headless browser)
type RodFetcher struct {
	browser *rod.Browser
}

// NewRodFetcher creates a new RodFetcher instance
func NewRodFetcher() (*RodFetcher, error) {
	// Get user data directory from environment or use default
	// This should be mounted as a volume to use disk instead of memory
	userDataDir := os.Getenv("BOT_DATA_DIR")
	if userDataDir == "" {
		userDataDir = "/tmp/bnb-data" // Default to /tmp/bnb-data if not set
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

	return &RodFetcher{
		browser: browser,
	}, nil
}

// Close closes the browser
func (rf *RodFetcher) Close() error {
	if rf.browser != nil {
		return rf.browser.Close()
	}
	return nil
}

// Fetch implements the Fetcher interface
func (rf *RodFetcher) Fetch(url string, maxPages int) ([]string, error) {
	var htmlPages []string
	pageCount := 0

	log.Printf("Starting fetch with maxPages: %d\n", maxPages)

	// Create a new page
	page := rf.browser.MustPage()
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
		log.Printf("Fetched page %d/%d\n", pageCount, maxPages)

	// Handle pagination
	for pageCount < maxPages {
		// Try multiple strategies to find the next page button/link
		var nextButton *rod.Element
		var err error

		// Strategy 1: Look for button with "Next" aria-label
		nextButton, err = page.Timeout(3 * time.Second).Element("button[aria-label*='Next'], button[aria-label*='next']")
		if err != nil {
			// Strategy 2: Look for link with "Next" aria-label
			nextButton, err = page.Timeout(3 * time.Second).Element("a[aria-label*='Next'], a[aria-label*='next']")
		}
		if err != nil {
			// Strategy 3: Look for pagination button with arrow icon
			nextButton, err = page.Timeout(3 * time.Second).Element("button[data-testid*='pagination'], a[data-testid*='pagination']")
		}
		if err != nil {
			// Strategy 4: Look for any button/link containing "next" in text or aria-label (case insensitive)
			nextButton, err = page.Timeout(3 * time.Second).Element("button, a")
			if err == nil {
				// Check if any button has "next" in its text or aria-label
				buttons, _ := page.Elements("button, a")
				nextButton = nil
				for _, btn := range buttons {
					text, _ := btn.Text()
					ariaLabelPtr, _ := btn.Attribute("aria-label")
					ariaLabel := ""
					if ariaLabelPtr != nil {
						ariaLabel = *ariaLabelPtr
					}
					if (strings.Contains(strings.ToLower(text), "next") ||
						strings.Contains(strings.ToLower(ariaLabel), "next")) &&
						!strings.Contains(strings.ToLower(text), "previous") &&
						!strings.Contains(strings.ToLower(ariaLabel), "previous") {
						visible, _ := btn.Visible()
						if visible {
							nextButton = btn
							break
						}
					}
				}
				if nextButton == nil {
					err = fmt.Errorf("no next button found")
				}
			}
		}

		if err != nil || nextButton == nil {
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

		// Scroll to the button to ensure it's in view
		nextButton.ScrollIntoView()
		time.Sleep(1 * time.Second) // Wait after scrolling

		// Use JavaScript click which is more reliable and doesn't timeout
		// Get the element's selector or use a more reliable click method
		nextButton.MustClick()

		// Wait for new content to load - wait longer for dynamic content
		page.WaitLoad()
		time.Sleep(4 * time.Second) // Increased wait time for content to load

		// Wait for the page to stabilize (content has changed)
		page.Timeout(15 * time.Second).MustWaitStable()

		// Additional wait to ensure listings are rendered
		time.Sleep(2 * time.Second)

		// Get HTML content
		html, err := page.HTML()
		if err != nil {
			log.Printf("Failed to get HTML for page %d: %v\n", pageCount+1, err)
			break
		}

		// Check if we got the same content (simple check - compare HTML length)
		// This is a basic check, but if HTML is identical, we're not getting new content
		if len(htmlPages) > 0 && len(html) == len(htmlPages[len(htmlPages)-1]) {
			log.Printf("Warning: Page %d HTML length matches previous page, might be duplicate content\n", pageCount+1)
		}

		htmlPages = append(htmlPages, html)
		pageCount++
		log.Printf("Fetched page %d/%d (HTML size: %d bytes)\n", pageCount, maxPages, len(html))
	}

	log.Printf("Fetching completed. Total pages fetched: %d (requested: %d)\n", len(htmlPages), maxPages)

	if len(htmlPages) == 0 {
		log.Println("Warning: No HTML pages collected.")
	}

	return htmlPages, nil
}
