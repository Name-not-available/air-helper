package fetcher

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	rodlauncher "github.com/go-rod/rod/lib/launcher"
)

// RodFetcher implements the Fetcher interface using rod (headless browser)
type RodFetcher struct {
	browser  *rod.Browser
	launcher *rodlauncher.Launcher
}

// NewRodFetcher creates a new RodFetcher instance
func NewRodFetcher() (*RodFetcher, error) {
	// Get user data directory from environment or use default
	// Prefer mounted memory at /tmp/air-data to offload pressure from RAM
	userDataDir := os.Getenv("BOT_DATA_DIR")
	if userDataDir == "" {
		if info, err := os.Stat("/tmp/air-data"); err == nil && info.IsDir() {
			userDataDir = filepath.Join("/tmp/air-data", "browser-data")
		} else {
			userDataDir = filepath.Join(os.TempDir(), "bnb-data")
		}
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		log.Printf("Warning: Failed to create bot data directory %s: %v\n", userDataDir, err)
		userDataDir = "" // Fall back to default if we can't create it
	}

	// Try to use system Chrome first, fallback to downloading Chromium
	rodLauncher := rodlauncher.New().
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
				rodLauncher = rodLauncher.Bin(path)
				break
			}
		}
	}

	// Check Windows paths
	for _, path := range chromePaths {
		if _, err := os.Stat(path); err == nil {
			rodLauncher = rodLauncher.Bin(path)
			break
		}
	}

	browserURL, err := rodLauncher.Launch()
	if err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w\n\nNote: On Linux, you may need to install Chromium dependencies:\n  apt-get update && apt-get install -y chromium chromium-sandbox || yum install -y chromium", err)
	}

	browser := rod.New().ControlURL(browserURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to browser: %w", err)
	}

	return &RodFetcher{
		browser:  browser,
		launcher: rodLauncher,
	}, nil
}

// Close closes the browser
func (rf *RodFetcher) Close() error {
	var err error
	if rf.browser != nil {
		err = rf.browser.Close()
	}
	if rf.launcher != nil {
		rf.launcher.Kill()
	}
	return err
}

// GetBrowser returns the underlying browser instance
func (rf *RodFetcher) GetBrowser() *rod.Browser {
	return rf.browser
}

// findNextPageLink finds the next page link within the pagination navigation.
// It scopes the search to nav[aria-label='Search results pagination'] to avoid
// clicking on carousel/calendar controls. Returns the href URL, the element, and any error.
// Selectors tried in order:
//   - a[rel='next'] within the nav
//   - a[aria-label='Next'] or a[aria-label='next'] within the nav
//   - button[data-testid='pagination-right-button'] within the nav
func (rf *RodFetcher) findNextPageLink(page *rod.Page) (string, *rod.Element, error) {
	// First, try to find the pagination nav
	nav, err := page.Timeout(3 * time.Second).Element("nav[aria-label='Search results pagination']")
	if err != nil {
		return "", nil, fmt.Errorf("pagination nav not found: %w", err)
	}

	// Strategy 1: Look for link with rel='next' within the nav
	nextLink, err := nav.Timeout(2 * time.Second).Element("a[rel='next']")
	if err == nil {
		href, _ := nextLink.Attribute("href")
		if href != nil && *href != "" {
			return *href, nextLink, nil
		}
	}

	// Strategy 2: Look for link with aria-label='Next' within the nav
	nextLink, err = nav.Timeout(2 * time.Second).Element("a[aria-label='Next'], a[aria-label='next']")
	if err == nil {
		href, _ := nextLink.Attribute("href")
		if href != nil && *href != "" {
			return *href, nextLink, nil
		}
	}

	// Strategy 3: Look for button with pagination data-testid within the nav
	nextButton, err := nav.Timeout(2 * time.Second).Element("button[data-testid='pagination-right-button']")
	if err == nil {
		// For buttons, we need to check if they have an href or if we need to click
		// Try to find a parent link or check if button triggers navigation
		// For now, return error to use click fallback - but this should be rare
		return "", nextButton, fmt.Errorf("found button but no href - button-based pagination not yet supported")
	}

	// Strategy 4: Look for any link/button with "next" in aria-label within nav
	allLinks, _ := nav.Elements("a, button")
	for _, elem := range allLinks {
		ariaLabelPtr, _ := elem.Attribute("aria-label")
		if ariaLabelPtr != nil {
			ariaLabel := strings.ToLower(*ariaLabelPtr)
			if strings.Contains(ariaLabel, "next") && !strings.Contains(ariaLabel, "previous") {
				visible, _ := elem.Visible()
				if visible {
					// Check if it's a link with href
					href, _ := elem.Attribute("href")
					if href != nil && *href != "" {
						return *href, elem, nil
					}
				}
			}
		}
	}

	return "", nil, fmt.Errorf("no next page link found in pagination nav")
}

// extractItemsOffset extracts the items_offset parameter from a URL.
// Returns -1 if not found or if parsing fails.
func (rf *RodFetcher) extractItemsOffset(urlStr string) int {
	if urlStr == "" {
		return -1
	}

	// Parse URL to get query parameters
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return -1
	}

	// Get items_offset from query
	offsetStr := parsedURL.Query().Get("items_offset")
	if offsetStr == "" {
		return -1
	}

	// Parse to int
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		return -1
	}

	return offset
}

// Fetch implements the Fetcher interface
func (rf *RodFetcher) Fetch(url string, maxPages int) ([]string, error) {
	var htmlPages []string
	pageCount := 0

	log.Printf("Starting fetch with maxPages: %d\n", maxPages)

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
		page = rf.browser.MustPage()
	}()
	if pageErr != nil {
		return nil, pageErr
	}
	if page == nil {
		return nil, fmt.Errorf("failed to create page")
	}
	defer page.Close()

	// Navigate to the URL
	if err := page.Navigate(url); err != nil {
		return nil, fmt.Errorf("failed to navigate: %w", err)
	}

	// Wait for page to load and listings to appear
	page.WaitLoad()
	time.Sleep(3 * time.Second) // Give JavaScript time to render

	// Try to wait for listing elements to appear (with timeout and error handling)
	if err := page.Timeout(10 * time.Second).WaitStable(500 * time.Millisecond); err != nil {
		log.Printf("Warning: Page did not stabilize within timeout, continuing anyway: %v\n", err)
	}

	// Get HTML content
	html, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("failed to get HTML: %w", err)
	}
	htmlPages = append(htmlPages, html)
	pageCount++

	// Get current URL and extract items_offset for validation
	currentURLResult, err := page.Eval(`() => window.location.href`)
	currentURLStr := ""
	if err == nil && currentURLResult != nil {
		currentURLStr = currentURLResult.Value.Str()
	}
	log.Printf("Fetched page %d/%d (URL: %s)\n", pageCount, maxPages, currentURLStr)

	// Extract items_offset from current URL for validation
	currentOffset := rf.extractItemsOffset(currentURLStr)
	log.Printf("Current items_offset: %d\n", currentOffset)

	// Handle pagination
	for pageCount < maxPages {
		// Add delay between page requests (3-5 seconds)
		// Use 4 seconds as average between 3-5
		time.Sleep(4 * time.Second)

		// Get current URL before navigation attempt
		beforeURLResult, err := page.Eval(`() => window.location.href`)
		beforeURLStr := ""
		if err == nil && beforeURLResult != nil {
			beforeURLStr = beforeURLResult.Value.Str()
		}
		log.Printf("Before pagination attempt - Current URL: %s\n", beforeURLStr)

		// Find next page link within pagination nav
		nextURL, nextElement, err := rf.findNextPageLink(page)
		if err != nil || nextURL == "" {
			log.Printf("No more pages found after page %d: %v\n", pageCount, err)
			break
		}

		// Log what we found
		if nextElement != nil {
			tagNameResult, err := nextElement.Eval(`() => this.tagName`)
			tagName := ""
			if err == nil && tagNameResult != nil {
				tagName = tagNameResult.Value.Str()
			}
			ariaLabel, _ := nextElement.Attribute("aria-label")
			href, _ := nextElement.Attribute("href")
			log.Printf("Found next page element - Tag: %s, aria-label: %v, href: %v\n",
				tagName, ariaLabel, href)
		}
		log.Printf("Next page URL: %s\n", nextURL)

		// Normalize URL (handle relative URLs)
		if strings.HasPrefix(nextURL, "/") {
			nextURL = "https://www.airbnb.com" + nextURL
		}

		// Navigate to next page
		if err := page.Navigate(nextURL); err != nil {
			log.Printf("Failed to navigate to next page: %v\n", err)
			break
		}

		// Wait for page to load
		page.WaitLoad()
		time.Sleep(3 * time.Second) // Give JavaScript time to render

		// Wait for page to stabilize
		if err := page.Timeout(15 * time.Second).WaitStable(500 * time.Millisecond); err != nil {
			log.Printf("Warning: Page did not stabilize after navigation, continuing anyway: %v\n", err)
		}

		// Additional wait to ensure listings are rendered
		time.Sleep(2 * time.Second)

		// Get URL after navigation to validate progress
		afterURLResult, err := page.Eval(`() => window.location.href`)
		afterURLStr := ""
		if err == nil && afterURLResult != nil {
			afterURLStr = afterURLResult.Value.Str()
		}
		log.Printf("After navigation - Current URL: %s\n", afterURLStr)

		// Validate that we actually moved to a new page by checking items_offset
		newOffset := rf.extractItemsOffset(afterURLStr)
		log.Printf("New items_offset: %d (previous: %d)\n", newOffset, currentOffset)

		if newOffset <= currentOffset && newOffset >= 0 {
			log.Printf("Warning: items_offset did not increase (was %d, now %d). Page may not have advanced.\n",
				currentOffset, newOffset)
			// Check HTML content as fallback validation
			html, err := page.HTML()
			if err != nil {
				log.Printf("Failed to get HTML for validation: %v\n", err)
				break
			}
			// Compare with last page - if HTML is identical, it's a duplicate
			if len(htmlPages) > 0 && html == htmlPages[len(htmlPages)-1] {
				log.Printf("HTML is identical to previous page, stopping pagination\n")
				break
			}
		}

		// Update current offset for next iteration
		currentOffset = newOffset

		// Get HTML content
		html, err := page.HTML()
		if err != nil {
			log.Printf("Failed to get HTML for page %d: %v\n", pageCount+1, err)
			break
		}

		// Check if we got the same content (compare HTML to detect duplicates)
		isDuplicate := false
		if len(htmlPages) > 0 {
			// Compare with last page - if HTML is identical, it's a duplicate
			if html == htmlPages[len(htmlPages)-1] {
				log.Printf("Warning: Page %d HTML is identical to previous page (offset: %d), skipping duplicate\n",
					pageCount+1, newOffset)
				isDuplicate = true
			}
		}

		if !isDuplicate {
			htmlPages = append(htmlPages, html)
			pageCount++
			log.Printf("Fetched page %d/%d (HTML size: %d bytes, offset: %d)\n",
				pageCount, maxPages, len(html), newOffset)
		} else {
			// If we got a duplicate, stop pagination
			log.Printf("Stopping pagination due to duplicate content\n")
			break
		}
	}

	log.Printf("Fetching completed. Total pages fetched: %d (requested: %d)\n", len(htmlPages), maxPages)

	if len(htmlPages) == 0 {
		log.Println("Warning: No HTML pages collected.")
	}

	return htmlPages, nil
}
