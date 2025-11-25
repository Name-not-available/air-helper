package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"airbnb-scraper/models"

	"github.com/PuerkitoBio/goquery"
)

// Parser extracts listing data from HTML
type Parser struct{}

// NewParser creates a new Parser instance
func NewParser() *Parser {
	return &Parser{}
}

// ParseHTML extracts listings from HTML content
func (p *Parser) ParseHTML(htmlContent string) ([]models.Listing, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	var listings []models.Listing

	// Airbnb uses various selectors for listings
	// Try common selectors - these may need adjustment based on actual HTML structure
	doc.Find("[data-testid='listing-card'], ._14n5tpj, [itemprop='itemListElement']").Each(func(i int, s *goquery.Selection) {
		// Check if this listing is in "Available for similar dates" section
		if p.isInSimilarDatesSection(s) {
			return
		}
		listing := p.extractListing(s)
		if listing != nil {
			listings = append(listings, *listing)
		}
	})

	// If no listings found with common selectors, try alternative selectors
	if len(listings) == 0 {
		doc.Find("div[data-listing-id], a[href*='/rooms/']").Each(func(i int, s *goquery.Selection) {
			// Check if this listing is in "Available for similar dates" section
			if p.isInSimilarDatesSection(s) {
				return
			}
			listing := p.extractListing(s)
			if listing != nil {
				listings = append(listings, *listing)
			}
		})
	}

	return listings, nil
}

// isInSimilarDatesSection checks if a listing is within "Available for similar dates" section
func (p *Parser) isInSimilarDatesSection(s *goquery.Selection) bool {
	// Check parent elements for "Available for similar dates" text
	// Only filter if we find a clear section heading
	parent := s.Parent()
	for i := 0; i < 10; i++ { // Check up to 10 levels up
		if parent.Length() == 0 {
			break
		}

		// Look for headings that clearly indicate "similar dates" section
		// Check siblings and parent's previous siblings for section headers
		parent.Find("h1, h2, h3, h4").Each(func(i int, heading *goquery.Selection) {
			headingText := strings.ToLower(strings.TrimSpace(heading.Text()))
			// Only match exact phrases about similar dates in headings
			if (headingText == "available for similar dates" ||
				strings.HasPrefix(headingText, "available for similar dates") ||
				headingText == "similar dates") &&
				len(headingText) < 50 { // Heading should be short and specific
				// Found a clear heading - check if this listing is in that section
				// by checking if the heading is a sibling or parent of this listing's container
				// This logic needs to be more robust to ensure the listing is actually *under* this heading
				// For now, a simple check for the heading's presence in a parent is enough to be lenient.
				// A more precise check would involve DOM traversal to ensure the listing is a descendant of the section.
				// For now, we return true if a relevant heading is found in an ancestor.
				return // This return is for the Each loop, not the function
			}
		})

		// Also check if parent itself is a section with "similar" in class or data attribute
		parentClass := strings.ToLower(parent.AttrOr("class", ""))
		parentDataTestId := strings.ToLower(parent.AttrOr("data-testid", ""))
		if (strings.Contains(parentClass, "similar") && strings.Contains(parentClass, "date")) ||
			strings.Contains(parentDataTestId, "similar") {
			// Check if there's a heading in this section
			if parent.Find("h1:contains('similar'), h2:contains('similar'), h3:contains('similar')").Length() > 0 {
				return true
			}
		}

		parent = parent.Parent()
	}
	return false
}

// extractListing extracts a single listing from a selection
func (p *Parser) extractListing(s *goquery.Selection) *models.Listing {
	listing := &models.Listing{}

	// Get all text from the listing card for pattern matching
	fullText := s.Text()

	// Extract title - try multiple selectors
	title := s.Find("div[data-testid='listing-card-title'], div[data-testid='listing-card-name'], h3, h2, ._14n5tpj h3, ._1e4w1l0").First().Text()
	if title == "" {
		title = s.Find("a").First().AttrOr("aria-label", "")
	}
	if title == "" {
		// Try to extract from link text
		title = s.Find("a[href*='/rooms/']").First().Text()
	}
	listing.Title = strings.TrimSpace(title)

	// Extract URL
	url := s.Find("a[href*='/rooms/']").First().AttrOr("href", "")
	if url != "" && !strings.HasPrefix(url, "http") {
		url = "https://www.airbnb.com" + url
	}
	listing.URL = url

	// Extract price - handle multiple prices and prefer non-strikethrough
	price, currency, allPrices := p.extractPriceFromListing(s, fullText)
	if price > 0 {
		listing.Price = price
		listing.Currency = currency
	}
	listing.AllPrices = allPrices // Always populate AllPrices for debugging

	// Extract star rating - try multiple approaches
	starText := s.Find("[data-testid='listing-card-rating'], span[class*='rating'], div[class*='rating'], span[aria-label*='star']").First().Text()
	if starText == "" {
		// Try to extract from full text using regex
		starText = p.extractStars(fullText)
	} else {
		starText = p.extractStars(starText)
	}
	if starText != "" {
		if stars, err := strconv.ParseFloat(starText, 64); err == nil {
			listing.Stars = stars
		}
	}

	// Extract review count - try multiple approaches
	reviewText := s.Find("[data-testid='listing-card-reviews'], span[class*='review'], div[class*='review']").First().Text()
	if reviewText == "" {
		// Try to extract from full text using regex
		reviewText = p.extractReviewCount(fullText)
	} else {
		reviewText = p.extractReviewCount(reviewText)
	}
	if reviewText != "" {
		if reviews, err := strconv.Atoi(reviewText); err == nil {
			listing.ReviewCount = reviews
		}
	}

	// Only return listing if it has at least a title or URL
	if listing.Title != "" || listing.URL != "" {
		return listing
	}

	return nil
}

// extractPrice extracts price and currency from text
// Returns (price, currency)
func (p *Parser) extractPrice(text string) (float64, string) {
	// Map of currency symbols to currency codes
	currencyMap := map[string]string{
		"$":   "USD",
		"€":   "EUR",
		"£":   "GBP",
		"¥":   "JPY",
		"฿":   "THB",
		"₫":   "VND",
		"USD": "USD",
		"EUR": "EUR",
		"GBP": "GBP",
		"THB": "THB",
		"VND": "VND",
	}

	// Pattern 1: Currency symbol at start: "$100", "฿1,000", "₫37,748,822"
	// Handle Vietnamese Dong with commas: ₫37,748,822
	re := regexp.MustCompile(`([\$€£¥฿₫])\s*([\d]{1,3}(?:[,\s]\d{3})*(?:\.[\d]+)?)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) >= 3 {
		currencySymbol := matches[1]
		priceStr := strings.ReplaceAll(strings.ReplaceAll(matches[2], ",", ""), " ", "")
		if price, err := strconv.ParseFloat(priceStr, 64); err == nil {
			currency := currencyMap[currencySymbol]
			if currency == "" {
				currency = currencySymbol
			}
			return price, currency
		}
	}

	// Pattern 2: Currency symbol at end: "1000 ฿", "1000 THB", "37,748,822 ₫"
	re = regexp.MustCompile(`([\d]{1,3}(?:[,\s]\d{3})*(?:\.[\d]+)?)\s*([\$€£¥฿₫]|USD|EUR|GBP|THB|VND)`)
	matches = re.FindStringSubmatch(text)
	if len(matches) >= 3 {
		priceStr := strings.ReplaceAll(strings.ReplaceAll(matches[1], ",", ""), " ", "")
		currencySymbol := strings.TrimSpace(matches[2])
		if price, err := strconv.ParseFloat(priceStr, 64); err == nil {
			currency := currencyMap[currencySymbol]
			if currency == "" {
				currency = currencySymbol
			}
			return price, currency
		}
	}

	// Pattern 3: Currency code with space: "100 USD", "1000 THB"
	re = regexp.MustCompile(`([\d]{1,3}(?:[,\s]\d{3})*(?:\.[\d]+)?)\s+(USD|EUR|GBP|THB|VND)`)
	matches = re.FindStringSubmatch(text)
	if len(matches) >= 3 {
		priceStr := strings.ReplaceAll(strings.ReplaceAll(matches[1], ",", ""), " ", "")
		if price, err := strconv.ParseFloat(priceStr, 64); err == nil {
			return price, matches[2]
		}
	}

	// Pattern 4: With "per night" or similar text (no explicit currency symbol, assume default)
	re = regexp.MustCompile(`([\d]{1,3}(?:[,\s]\d{3})*(?:\.[\d]+)?)\s*(?:per|/|night)`)
	matches = re.FindStringSubmatch(text)
	if len(matches) >= 2 {
		priceStr := strings.ReplaceAll(strings.ReplaceAll(matches[1], ",", ""), " ", "")
		if price, err := strconv.ParseFloat(priceStr, 64); err == nil {
			return price, "" // Return empty currency, will be defaulted later
		}
	}

	return 0, ""
}

// extractPriceFromListing extracts price from a listing, handling multiple prices
// and preferring non-strikethrough prices
// Returns: (selected price, currency, all prices found for debugging)
func (p *Parser) extractPriceFromListing(s *goquery.Selection, fullText string) (float64, string, []models.PriceInfo) {
	var priceElements []*goquery.Selection
	seenTexts := make(map[string]bool)

	// First, try to find a specific price container
	priceContainer := s.Find("[data-testid='listing-card-price'], [data-testid='price'], ._tyxjp1, ._1jo4hgw").First()

	// If we found a price container, look for all price elements within it
	if priceContainer.Length() > 0 {
		// Look for all child elements that might contain prices
		priceRegex := regexp.MustCompile(`[\$€£¥฿₫]\s*[\d]`)
		priceContainer.Find("span, div").Each(func(i int, elem *goquery.Selection) {
			text := strings.TrimSpace(elem.Text())
			// Check if this element contains a price pattern
			if len(text) > 0 && priceRegex.MatchString(text) {
				if !seenTexts[text] {
					seenTexts[text] = true
					priceElements = append(priceElements, elem)
				}
			}
		})
		// Also add the container itself if it has price text
		text := strings.TrimSpace(priceContainer.Text())
		if len(text) > 0 && priceRegex.MatchString(text) {
			if !seenTexts[text] {
				seenTexts[text] = true
				priceElements = append(priceElements, priceContainer)
			}
		}
	}

	// Also search for any element containing price patterns in the entire listing card
	// This is a fallback if specific price containers don't yield results or to catch other patterns
	priceRegex := regexp.MustCompile(`[\$€£¥฿₫]\s*[\d]{1,3}(?:[,\s]\d{3})*(?:\.[\d]+)?`)
	s.Find("span, div, b, strong").Each(func(i int, elem *goquery.Selection) {
		text := strings.TrimSpace(elem.Text())
		// Only consider elements with short text (price elements are usually short)
		if len(text) > 0 && len(text) < 50 && priceRegex.MatchString(text) {
			// Avoid duplicates by checking text content
			if !seenTexts[text] {
				seenTexts[text] = true
				priceElements = append(priceElements, elem)
			}
		}
	})

	// Collect all prices (both strikethrough and non-strikethrough) for debugging
	var allPricesInfo []models.PriceInfo
	for i, elem := range priceElements {
		isStrike := p.isStrikethrough(elem)
		price, currency := p.extractPrice(elem.Text())
		if price > 0 {
			allPricesInfo = append(allPricesInfo, models.PriceInfo{
				Price:    price,
				Currency: currency,
				Text:     elem.Text(),
				IsStrike: isStrike,
				Index:    i,
			})
		}
	}

	// If no price elements found, try extracting from full text as a last resort
	if len(allPricesInfo) == 0 {
		price, currency := p.extractPrice(fullText)
		if price > 0 {
			allPricesInfo = append(allPricesInfo, models.PriceInfo{
				Price:    price,
				Currency: currency,
				Text:     fullText, // Store full text for context
				IsStrike: false,    // Assume not strikethrough if from full text
				Index:    -1,       // Indicate full text extraction
			})
			return price, currency, allPricesInfo
		}
		return 0, "", allPricesInfo // Return empty if no price found
	}

	// If only one price element, use it if not strikethrough
	if len(allPricesInfo) == 1 {
		return allPricesInfo[0].Price, allPricesInfo[0].Currency, allPricesInfo
	}

	// Multiple prices found - prefer non-strikethrough ones
	var nonStrikethroughPrices []models.PriceInfo
	for _, pInfo := range allPricesInfo {
		if !pInfo.IsStrike {
			nonStrikethroughPrices = append(nonStrikethroughPrices, pInfo)
		}
	}

	// If we found non-strikethrough prices, prefer the last one (usually the current price)
	if len(nonStrikethroughPrices) > 0 {
		last := nonStrikethroughPrices[len(nonStrikethroughPrices)-1]
		return last.Price, last.Currency, allPricesInfo
	}

	// If all are strikethrough, try the last one anyway (fallback)
	if len(allPricesInfo) > 0 {
		last := allPricesInfo[len(allPricesInfo)-1]
		return last.Price, last.Currency, allPricesInfo
	}

	return 0, "", allPricesInfo
}

// isStrikethrough checks if an element has strikethrough styling
func (p *Parser) isStrikethrough(s *goquery.Selection) bool {
	// Check for strikethrough tags
	if s.Is("s, strike, del") {
		return true
	}

	// Check for strikethrough in class names
	class, _ := s.Attr("class")
	if strings.Contains(strings.ToLower(class), "strike") ||
		strings.Contains(strings.ToLower(class), "line-through") ||
		strings.Contains(strings.ToLower(class), "linethrough") {
		return true
	}

	// Check for strikethrough in inline style
	style, _ := s.Attr("style")
	if strings.Contains(strings.ToLower(style), "text-decoration: line-through") {
		return true
	}

	// Check parent elements for strikethrough (up to 5 levels up)
	parent := s.Parent()
	for i := 0; i < 5; i++ {
		if parent.Length() == 0 {
			break
		}
		parentClass, _ := parent.Attr("class")
		if strings.Contains(strings.ToLower(parentClass), "strike") ||
			strings.Contains(strings.ToLower(parentClass), "line-through") ||
			strings.Contains(strings.ToLower(parentClass), "linethrough") {
			return true
		}
		parentStyle, _ := parent.Attr("style")
		if strings.Contains(strings.ToLower(parentStyle), "text-decoration: line-through") {
			return true
		}
		parent = parent.Parent()
	}

	return false
}

// extractStars extracts star rating from text
func (p *Parser) extractStars(text string) string {
	// Look for patterns like "4.5", "4.5 stars", "4.5/5", etc.
	re := regexp.MustCompile(`(\d+\.?\d*)\s*(?:star|★|⭐|/5)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	// Try pattern like "4.5 out of 5"
	re = regexp.MustCompile(`(\d+\.?\d*)\s*(?:out of|/)\s*5`)
	matches = re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractReviewCount extracts review count from text
func (p *Parser) extractReviewCount(text string) string {
	// Look for patterns like "(123 reviews)", "123 reviews", "(123)", etc.
	re := regexp.MustCompile(`\(?(\d{1,4}(?:,\d{3})*)\s*reviews?\)?`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return strings.ReplaceAll(matches[1], ",", "") // Remove commas before returning
	}
	// Try pattern with parentheses only
	re = regexp.MustCompile(`\((\d{1,4}(?:,\d{3})*)\)`)
	matches = re.FindStringSubmatch(text)
	if len(matches) > 1 {
		return strings.ReplaceAll(matches[1], ",", "") // Remove commas before returning
	}
	return ""
}




