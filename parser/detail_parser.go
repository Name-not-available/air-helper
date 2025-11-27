package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"bnb-fetcher/models"

	"github.com/PuerkitoBio/goquery"
)

// DetailParser extracts detailed information from listing detail pages
type DetailParser struct{}

// NewDetailParser creates a new DetailParser instance
func NewDetailParser() *DetailParser {
	return &DetailParser{}
}

// ParseDetailPage extracts all detail information from a listing detail page
func (dp *DetailParser) ParseDetailPage(htmlContent string) (*models.Listing, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	listing := &models.Listing{}

	// Extract is_superhost
	listing.IsSuperhost = dp.extractSuperhost(doc)

	// Extract is_guest_favorite
	listing.IsGuestFavorite = dp.extractGuestFavorite(doc)

	// Extract bedrooms, bathrooms, beds
	listing.Bedrooms, listing.Bathrooms, listing.Beds = dp.extractRoomCounts(doc)

	// Extract description
	listing.Description = dp.extractDescription(doc)

	// Extract house rules
	listing.HouseRules = dp.extractHouseRules(doc)

	// Extract reviews
	reviews, newestDate := dp.extractReviews(doc)
	listing.Reviews = reviews
	if newestDate != nil {
		listing.NewestReviewDate = newestDate
	}

	return listing, nil
}

// extractSuperhost checks if the listing is from a superhost
func (dp *DetailParser) extractSuperhost(doc *goquery.Document) bool {
	// Look for superhost badge or text
	superhostSelectors := []string{
		"[data-testid='superhost-badge']",
		"._1y6fhhr:contains('Superhost')",
		"span:contains('Superhost')",
		"[aria-label*='Superhost']",
	}

	for _, selector := range superhostSelectors {
		if doc.Find(selector).Length() > 0 {
			return true
		}
	}

	// Check for text content
	doc.Find("body").Each(func(i int, s *goquery.Selection) {
		text := strings.ToLower(s.Text())
		if strings.Contains(text, "superhost") {
			// Make sure it's not just mentioning it in a review
			if !strings.Contains(text, "review") || strings.Contains(text, "superhost badge") {
				// This is a simple check - might need refinement
			}
		}
	})

	return false
}

// extractGuestFavorite checks if the listing is a guest favorite
func (dp *DetailParser) extractGuestFavorite(doc *goquery.Document) bool {
	// Look for guest favorite badge or text
	guestFavoriteSelectors := []string{
		"[data-testid='guest-favorite-badge']",
		"._1y6fhhr:contains('Guest favorite')",
		"span:contains('Guest favorite')",
		"[aria-label*='Guest favorite']",
		"[aria-label*='guest favorite']",
	}

	for _, selector := range guestFavoriteSelectors {
		if doc.Find(selector).Length() > 0 {
			return true
		}
	}

	// Check for text content
	doc.Find("body").Each(func(i int, s *goquery.Selection) {
		text := strings.ToLower(s.Text())
		if strings.Contains(text, "guest favorite") {
			return
		}
	})

	return false
}

// unicodeFractionMap maps unicode fraction characters to their decimal values
var unicodeFractionMap = map[rune]float64{
	'¼': 0.25,
	'½': 0.5,
	'¾': 0.75,
	'⅓': 1.0 / 3.0,
	'⅔': 2.0 / 3.0,
	'⅛': 0.125,
	'⅜': 0.375,
	'⅝': 0.625,
	'⅞': 0.875,
	'⅕': 0.2,
	'⅖': 0.4,
	'⅗': 0.6,
	'⅘': 0.8,
	'⅙': 1.0 / 6.0,
	'⅚': 5.0 / 6.0,
	'⅑': 1.0 / 9.0,
	'⅒': 0.1,
}

// normalizeWhitespace replaces various unicode whitespace characters with regular spaces
func normalizeWhitespace(text string) string {
	// Replace non-breaking spaces and other unicode whitespace with regular spaces
	normalized := strings.Builder{}
	for _, r := range text {
		if unicode.IsSpace(r) {
			normalized.WriteRune(' ')
		} else {
			normalized.WriteRune(r)
		}
	}
	// Collapse multiple spaces into one
	result := strings.Join(strings.Fields(normalized.String()), " ")
	return result
}

// extractNumericToken extracts a numeric token from text (supports decimals, fractions, unicode fractions)
func extractNumericToken(text string) string {
	if text == "" {
		return ""
	}

	// Build pattern to match various numeric formats:
	// - Decimal: 2.5, 2,5
	// - Mixed fraction: 2 1/2, 2½
	// - Simple fraction: 1/2, ½
	// - Unicode fractions: ½, ¼, etc.

	// Pattern: (optional whole number) (optional unicode fraction OR space + fraction)
	// This matches: "2.5", "2,5", "2 1/2", "2½", "1/2", "½"
	pattern := regexp.MustCompile(`(?:\d+[.,]\d+|\d+\s+\d+/\d+|\d+[¼½¾⅓⅔⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚⅑⅒]|\d+/\d+|[¼½¾⅓⅔⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚⅑⅒]|\d+)`)
	matches := pattern.FindStringSubmatch(text)
	if len(matches) > 0 {
		return strings.TrimSpace(matches[0])
	}
	return ""
}

// parseRoomValue parses a numeric token string into a float64 value
// Supports: decimals (2.5), mixed fractions (2 1/2), simple fractions (1/2), unicode fractions (2½, ½)
func parseRoomValue(token string) (float64, error) {
	if token == "" {
		return 0, fmt.Errorf("empty token")
	}

	// Normalize whitespace and replace comma with dot for decimal
	token = normalizeWhitespace(token)
	token = strings.ReplaceAll(token, ",", ".")
	token = strings.TrimSpace(token)

	// Check for unicode fraction with whole number: "2½"
	for whole, fraction := range unicodeFractionMap {
		pattern := regexp.MustCompile(fmt.Sprintf(`^(\d+)?\s*%c$`, whole))
		matches := pattern.FindStringSubmatch(token)
		if len(matches) > 0 {
			wholeNum := 0.0
			if matches[1] != "" {
				var err error
				wholeNum, err = strconv.ParseFloat(matches[1], 64)
				if err != nil {
					continue
				}
			}
			return wholeNum + fraction, nil
		}
	}

	// Check for mixed fraction: "2 1/2" or "2 1 / 2"
	mixedPattern := regexp.MustCompile(`^(\d+)\s+(\d+)\s*/\s*(\d+)$`)
	matches := mixedPattern.FindStringSubmatch(token)
	if len(matches) >= 4 {
		whole, _ := strconv.ParseFloat(matches[1], 64)
		numerator, _ := strconv.ParseFloat(matches[2], 64)
		denominator, _ := strconv.ParseFloat(matches[3], 64)
		if denominator != 0 {
			return whole + (numerator / denominator), nil
		}
	}

	// Check for simple fraction: "1/2"
	simpleFractionPattern := regexp.MustCompile(`^(\d+)\s*/\s*(\d+)$`)
	matches = simpleFractionPattern.FindStringSubmatch(token)
	if len(matches) >= 3 {
		numerator, _ := strconv.ParseFloat(matches[1], 64)
		denominator, _ := strconv.ParseFloat(matches[2], 64)
		if denominator != 0 {
			return numerator / denominator, nil
		}
	}

	// Check for standalone unicode fraction: "½"
	for whole, fraction := range unicodeFractionMap {
		if token == string(whole) {
			return fraction, nil
		}
	}

	// Try parsing as regular decimal/integer
	val, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to parse token: %s", token)
	}
	return val, nil
}

// extractRoomCounts extracts bedrooms, bathrooms, and beds count (supports decimal values, fractions, unicode fractions)
func (dp *DetailParser) extractRoomCounts(doc *goquery.Document) (bedrooms, bathrooms, beds float64) {
	// Build a pattern that matches various numeric formats
	// This pattern captures: decimals, mixed fractions, unicode fractions, simple fractions
	numberTokenPattern := `(?:\d+[.,]\d+|\d+\s+\d+/\d+|\d+[¼½¾⅓⅔⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚⅑⅒]|\d+/\d+|[¼½¾⅓⅔⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚⅑⅒]|\d+)`

	// Helper to validate room count values (reject unreasonable numbers)
	isValidRoomCount := func(val float64) bool {
		return val > 0 && val <= 20 // Sanity check: no listing has >20 rooms
	}

	// First, try to find in specific data-testid elements (most reliable)
	// REMOVED [data-testid*='room'] as it's too broad and matches unrelated elements
	doc.Find("[data-testid*='bedroom'], [data-testid*='bathroom'], [data-testid*='bed']").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		testid, _ := s.Attr("data-testid")
		testid = strings.ToLower(testid)

		// Extract numeric token from text (supports decimals, fractions, unicode fractions)
		rawToken := extractNumericToken(text)
		if rawToken == "" {
			return
		}

		// Parse the token to float64
		val, err := parseRoomValue(rawToken)
		if err != nil || !isValidRoomCount(val) {
			return
		}

		// Prioritize exact matches first
		if strings.Contains(testid, "bedroom") {
			if bedrooms == 0 {
				bedrooms = val
			}
		} else if strings.Contains(testid, "bathroom") {
			// Only match "bathroom" explicitly, not just "bath" to avoid false matches
			if bathrooms == 0 {
				bathrooms = val
			}
		} else if strings.Contains(testid, "bed") && !strings.Contains(testid, "bedroom") {
			if beds == 0 {
				beds = val
			}
		}
	})

	// Get all text content that might contain room info
	fullText := doc.Text()
	fullTextLower := strings.ToLower(fullText)

	// More comprehensive patterns (case-insensitive) with broader numeric token matching
	patterns := []struct {
		re            *regexp.Regexp
		field         *float64
		skipIfBedroom bool
	}{
		// Bedrooms patterns
		{regexp.MustCompile(`(?i)(` + numberTokenPattern + `)\s*(?:bedroom|bedrooms|br)\b`), &bedrooms, false},
		{regexp.MustCompile(`(?i)(` + numberTokenPattern + `)\s*br\b`), &bedrooms, false},
		// Bathrooms patterns - be more specific to avoid false matches
		// Match "1 bathroom", "1.5 bathrooms", "2½ bathrooms", "2 1/2 bathrooms" but not "Room 61 bathroom"
		{regexp.MustCompile(`(?i)\b(` + numberTokenPattern + `)\s*(?:bathroom|bathrooms|bath)\b`), &bathrooms, false},
		// Match "1 ba" with word boundary to avoid matching room numbers
		{regexp.MustCompile(`(?i)\b(` + numberTokenPattern + `)\s+ba\b`), &bathrooms, false},
		// Beds patterns - match "bed" or "beds" but not "bedroom" or "bedrooms"
		{regexp.MustCompile(`(?i)(` + numberTokenPattern + `)\s+bed(?:s)?\b`), &beds, true},
	}

	for _, p := range patterns {
		if *p.field == 0 { // Only set if not already found
			matches := p.re.FindStringSubmatch(fullText)
			if len(matches) > 1 {
				// For beds pattern, check that it's not part of "bedroom"
				if p.skipIfBedroom {
					// Get the full match to check context
					fullMatch := matches[0]
					matchIndex := strings.Index(fullTextLower, strings.ToLower(fullMatch))
					if matchIndex >= 0 {
						// Check the next few characters after the match
						afterMatch := ""
						if matchIndex+len(fullMatch) < len(fullTextLower) {
							afterMatch = fullTextLower[matchIndex+len(fullMatch):]
							// If "room" follows immediately, skip this match (it's "bedroom")
							if strings.HasPrefix(afterMatch, "room") {
								continue
							}
						}
					}
				}

				// Parse the numeric token (supports decimals, fractions, unicode fractions)
				token := matches[1]
				val, err := parseRoomValue(token)
				if err == nil && isValidRoomCount(val) {
					*p.field = val
				}
			}
		}
	}

	// Try to find in summary sections (common Airbnb pattern: "1 bed, 1 bath" or "3 beds, 2½ baths")
	// More flexible pattern: allows comma, space, or both between bed and bath counts
	summaryPattern := regexp.MustCompile(`(?i)(` + numberTokenPattern + `)\s*(?:bed|beds|br)\s*[,]?\s*(` + numberTokenPattern + `)\s*(?:bath|baths|ba)`)
	matches := summaryPattern.FindStringSubmatch(fullText)
	if len(matches) >= 3 {
		if beds == 0 {
			bedToken := matches[1]
			if val, err := parseRoomValue(bedToken); err == nil && isValidRoomCount(val) {
				beds = val
			}
		}
		if bathrooms == 0 {
			bathToken := matches[2]
			if val, err := parseRoomValue(bathToken); err == nil && isValidRoomCount(val) {
				bathrooms = val
			}
		}
	}

	// Look for structured data in meta tags or JSON-LD
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		jsonText := s.Text()
		// Try to extract from JSON-LD (basic pattern matching)
		if strings.Contains(jsonText, "numberOfBedrooms") {
			re := regexp.MustCompile(`"numberOfBedrooms"\s*:\s*(\d+(?:\.\d+)?)`)
			matches := re.FindStringSubmatch(jsonText)
			if len(matches) > 1 && bedrooms == 0 {
				if val, err := strconv.ParseFloat(matches[1], 64); err == nil && isValidRoomCount(val) {
					bedrooms = val
				}
			}
		}
		if strings.Contains(jsonText, "numberOfBathroomsTotal") {
			re := regexp.MustCompile(`"numberOfBathroomsTotal"\s*:\s*(\d+(?:\.\d+)?)`)
			matches := re.FindStringSubmatch(jsonText)
			if len(matches) > 1 && bathrooms == 0 {
				if val, err := strconv.ParseFloat(matches[1], 64); err == nil && isValidRoomCount(val) {
					bathrooms = val
				}
			}
		}
	})

	return bedrooms, bathrooms, beds
}

// extractDescription extracts the listing description
func (dp *DetailParser) extractDescription(doc *goquery.Document) string {
	// Common selectors for description
	descriptionSelectors := []string{
		"[data-testid='listing-description']",
		"[data-section-id='DESCRIPTION_DEFAULT']",
		"#description",
		"._1y6fhhr",
	}

	for _, selector := range descriptionSelectors {
		desc := doc.Find(selector).First().Text()
		if desc != "" && len(desc) > 50 {
			return strings.TrimSpace(desc)
		}
	}

	// Fallback: look for common description patterns
	doc.Find("div, section").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		if len(text) > 200 && strings.Contains(strings.ToLower(text), "description") {
			// Try to extract just the description part
			return
		}
	})

	return ""
}

// extractHouseRules extracts house rules
func (dp *DetailParser) extractHouseRules(doc *goquery.Document) string {
	var foundRules string

	// Common selectors for house rules
	houseRulesSelectors := []string{
		"[data-testid='house-rules']",
		"[data-testid*='house-rules']",
		"[data-section-id='HOUSE_RULES_DEFAULT']",
		"[data-section-id*='HOUSE_RULES']",
		"#house-rules",
		"[id*='house-rules']",
		"[class*='house-rules']",
		"[class*='HouseRules']",
	}

	for _, selector := range houseRulesSelectors {
		rules := doc.Find(selector).First().Text()
		if rules != "" && len(rules) > 10 {
			foundRules = strings.TrimSpace(rules)
			if len(foundRules) > 20 {
				return foundRules
			}
		}
	}

	// Look for section containing "House rules" text
	doc.Find("section, div, article").Each(func(i int, s *goquery.Selection) {
		if foundRules != "" && len(foundRules) > 20 {
			return // Already found good rules
		}
		text := strings.ToLower(s.Text())
		if strings.Contains(text, "house rules") || strings.Contains(text, "house rule") {
			rules := s.Text()
			// Extract just the rules part (after "House rules" heading)
			if idx := strings.Index(strings.ToLower(rules), "house rules"); idx >= 0 {
				rules = rules[idx:]
				// Try to find the end (next heading or section)
				if len(rules) > 20 {
					foundRules = strings.TrimSpace(rules)
					// Limit length to avoid getting too much
					if len(foundRules) > 500 {
						foundRules = foundRules[:500] + "..."
					}
				}
			}
		}
	})

	// Look for expandable sections that might contain house rules
	doc.Find("[aria-expanded], button, [role='button']").Each(func(i int, s *goquery.Selection) {
		if foundRules != "" && len(foundRules) > 20 {
			return
		}
		text := strings.ToLower(s.Text())
		if strings.Contains(text, "house rules") || strings.Contains(text, "show house rules") {
			// Try to find the content that would be revealed
			parent := s.Parent()
			if parent != nil {
				rules := parent.Find("div, ul, ol").Text()
				if len(rules) > 20 {
					foundRules = strings.TrimSpace(rules)
				}
			}
		}
	})

	return foundRules
}

// extractReviews extracts all reviews from the page
func (dp *DetailParser) extractReviews(doc *goquery.Document) ([]models.Review, *time.Time) {
	var reviews []models.Review
	var newestDate *time.Time

	// Find all review containers with more comprehensive selectors
	reviewSelectors := []string{
		"[data-testid='review']",
		"[data-testid='review-item']",
		"[data-testid*='review']",
		"section[aria-label*='review']",
		"[class*='Review']",
		"[class*='review']",
		"article[data-review-id]",
	}

	for _, selector := range reviewSelectors {
		doc.Find(selector).Each(func(i int, s *goquery.Selection) {
			review := dp.extractSingleReview(s)
			if review != nil && !review.Date.IsZero() {
				reviews = append(reviews, *review)
				// Track newest date
				if newestDate == nil || review.Date.After(*newestDate) {
					newestDate = &review.Date
				}
			}
		})
	}

	// If no reviews found with specific selectors, try to find review sections by content
	if len(reviews) == 0 {
		doc.Find("div, section, article").Each(func(i int, s *goquery.Selection) {
			// Check if this looks like a review container
			text := strings.ToLower(s.Text())
			hasReviewKeyword := strings.Contains(text, "review") || strings.Contains(text, "rating")
			hasStarOrScore := strings.Contains(text, "star") || strings.Contains(text, "rating") || regexp.MustCompile(`\d+\.?\d*\s*(?:out of|/)\s*5`).MatchString(text)
			hasDate := regexp.MustCompile(`\w+\s+\d{1,2},?\s+\d{4}|\d{1,2}/\d{1,2}/\d{4}|\d+\s+(?:day|week|month|year)s?\s+ago`).MatchString(text)

			if hasReviewKeyword && (hasStarOrScore || hasDate) {
				review := dp.extractSingleReview(s)
				if review != nil && !review.Date.IsZero() {
					reviews = append(reviews, *review)
					if newestDate == nil || review.Date.After(*newestDate) {
						newestDate = &review.Date
					}
				}
			}
		})
	}

	// Ensure newestDate is set if we have at least one review
	if len(reviews) > 0 && newestDate == nil {
		// Find the newest date from reviews
		for _, review := range reviews {
			if !review.Date.IsZero() {
				if newestDate == nil || review.Date.After(*newestDate) {
					newestDate = &review.Date
				}
			}
		}
	}

	return reviews, newestDate
}

// extractSingleReview extracts a single review from a selection
func (dp *DetailParser) extractSingleReview(s *goquery.Selection) *models.Review {
	review := &models.Review{}

	// Extract date (try multiple times with different scopes)
	dateStr := dp.extractReviewDate(s)
	if dateStr == "" {
		// Try parent elements
		parent := s.Parent()
		if parent != nil {
			dateStr = dp.extractReviewDate(parent)
		}
	}
	if dateStr == "" {
		// Try siblings
		s.PrevAll().Each(func(i int, prev *goquery.Selection) {
			if dateStr == "" {
				dateStr = dp.extractReviewDate(prev)
			}
		})
	}

	// Parse date, but don't fail if date parsing fails - use current date as fallback
	if dateStr != "" {
		parsedDate, err := dp.parseDate(dateStr)
		if err == nil {
			review.Date = parsedDate
		} else {
			// Use current date as fallback if parsing fails
			review.Date = time.Now()
		}
	} else {
		// No date found, use current date as fallback
		review.Date = time.Now()
	}

	// Extract score
	review.Score = dp.extractReviewScore(s)

	// Extract full text
	review.FullText = dp.extractReviewText(s)
	if review.FullText == "" {
		// Try to get text from the selection itself
		review.FullText = strings.TrimSpace(s.Text())
		// Limit length
		if len(review.FullText) > 5000 {
			review.FullText = review.FullText[:5000] + "..."
		}
	}

	// Extract time on Airbnb
	review.TimeOnAirbnb = dp.extractTimeOnAirbnb(s)

	// Only return review if it has at least text or score
	if review.FullText == "" && review.Score == 0 {
		return nil
	}

	return review
}

// extractReviewDate extracts the review date
func (dp *DetailParser) extractReviewDate(s *goquery.Selection) string {
	// Look for date elements with datetime attribute (most reliable)
	dateSelectors := []string{
		"[data-testid='review-date']",
		"[data-testid*='date']",
		"time[datetime]",
		"time",
		"[datetime]",
		"[data-date]",
	}

	for _, selector := range dateSelectors {
		elem := s.Find(selector).First()
		if elem.Length() > 0 {
			// Try datetime attribute first
			dateStr := elem.AttrOr("datetime", "")
			if dateStr != "" {
				return strings.TrimSpace(dateStr)
			}
			// Try data-date attribute
			dateStr = elem.AttrOr("data-date", "")
			if dateStr != "" {
				return strings.TrimSpace(dateStr)
			}
			// Try text content
			dateStr = elem.Text()
			if dateStr != "" {
				dateStr = strings.TrimSpace(dateStr)
				// Validate it looks like a date
				if len(dateStr) > 5 {
					return dateStr
				}
			}
		}
	}

	// Try to find date patterns in text (more comprehensive patterns)
	text := s.Text()
	datePatterns := []*regexp.Regexp{
		regexp.MustCompile(`(\w+ \d{1,2}, \d{4})`),                                          // "March 15, 2024"
		regexp.MustCompile(`(\w+\.?\s+\d{1,2}, \d{4})`),                                     // "Mar. 15, 2024" or "Mar 15, 2024"
		regexp.MustCompile(`(\d{1,2}/\d{1,2}/\d{4})`),                                       // "3/15/2024"
		regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`),                                           // "2024-03-15"
		regexp.MustCompile(`(\d{1,2}\.\d{1,2}\.\d{4})`),                                     // "15.03.2024"
		regexp.MustCompile(`(\d{1,2}\s+\w+\s+\d{4})`),                                       // "15 March 2024"
		regexp.MustCompile(`(\d+\s+(?:day|days|week|weeks|month|months|year|years)\s+ago)`), // Relative dates
	}

	for _, pattern := range datePatterns {
		matches := pattern.FindStringSubmatch(text)
		if len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

// extractReviewScore extracts the review score/rating
func (dp *DetailParser) extractReviewScore(s *goquery.Selection) float64 {
	// Look for star rating
	starSelectors := []string{
		"[data-testid='review-rating']",
		"[aria-label*='star']",
		"._1y6fhhr",
	}

	for _, selector := range starSelectors {
		ratingText := s.Find(selector).First().AttrOr("aria-label", "")
		if ratingText == "" {
			ratingText = s.Find(selector).First().Text()
		}

		// Extract number from "X out of 5" or "X stars"
		re := regexp.MustCompile(`(\d+\.?\d*)\s*(?:out of|star|/)\s*5`)
		matches := re.FindStringSubmatch(ratingText)
		if len(matches) > 1 {
			if score, err := strconv.ParseFloat(matches[1], 64); err == nil {
				return score
			}
		}
	}

	return 0
}

// extractReviewText extracts the review text
func (dp *DetailParser) extractReviewText(s *goquery.Selection) string {
	// Look for review text
	textSelectors := []string{
		"[data-testid='review-text']",
		"[data-testid='review-content']",
		"p",
		"._1y6fhhr",
	}

	for _, selector := range textSelectors {
		text := s.Find(selector).First().Text()
		if text != "" && len(text) > 20 {
			return strings.TrimSpace(text)
		}
	}

	return ""
}

// extractTimeOnAirbnb extracts how long the user has been on Airbnb
func (dp *DetailParser) extractTimeOnAirbnb(s *goquery.Selection) string {
	// Look for text like "Joined in 2020" or "Member since 2020"
	text := s.Text()
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:Joined|Member since)\s+(\d{4})`),
		regexp.MustCompile(`(\d+)\s+(?:year|years)\s+on\s+Airbnb`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(text)
		if len(matches) > 1 {
			return matches[0] // Return the full matched string
		}
	}

	return ""
}

// parseDate parses various date formats including relative dates
func (dp *DetailParser) parseDate(dateStr string) (time.Time, error) {
	// Handle relative dates like "2 months ago", "3 weeks ago"
	relativeDateRe := regexp.MustCompile(`(\d+)\s+(day|days|week|weeks|month|months|year|years)\s+ago`)
	matches := relativeDateRe.FindStringSubmatch(strings.ToLower(dateStr))
	if len(matches) >= 3 {
		amount, _ := strconv.Atoi(matches[1])
		unit := matches[2]
		now := time.Now()
		switch unit {
		case "day", "days":
			return now.AddDate(0, 0, -amount), nil
		case "week", "weeks":
			return now.AddDate(0, 0, -amount*7), nil
		case "month", "months":
			return now.AddDate(0, -amount, 0), nil
		case "year", "years":
			return now.AddDate(-amount, 0, 0), nil
		}
	}

	// Try various date formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02",
		"January 2, 2006",
		"Jan 2, 2006",
		"Jan. 2, 2006",
		"1/2/2006",
		"01/02/2006",
		"2006/01/02",
		"2 January 2006",
		"2 Jan 2006",
		"15.03.2006",
		"2006-01-02 15:04:05",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	// Try parsing with location (common in some formats)
	if t, err := time.Parse("January 2, 2006", dateStr); err == nil {
		return t, nil
	}
	if t, err := time.Parse("Jan 2, 2006", dateStr); err == nil {
		return t, nil
	}

	// If all formats fail, return error
	return time.Time{}, fmt.Errorf("unable to parse date: %s", dateStr)
}
