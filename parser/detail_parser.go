package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// extractRoomCounts extracts bedrooms, bathrooms, and beds count
func (dp *DetailParser) extractRoomCounts(doc *goquery.Document) (bedrooms, bathrooms, beds int) {
	// First, try to find in specific data-testid elements (most reliable)
	doc.Find("[data-testid*='bedroom'], [data-testid*='bathroom'], [data-testid*='bed'], [data-testid*='room']").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		testid, _ := s.Attr("data-testid")
		testid = strings.ToLower(testid)

		// Extract number from text
		re := regexp.MustCompile(`(\d+)`)
		matches := re.FindStringSubmatch(text)
		if len(matches) > 1 {
			if val, err := strconv.Atoi(matches[1]); err == nil {
				if strings.Contains(testid, "bedroom") || strings.Contains(testid, "br") {
					if bedrooms == 0 {
						bedrooms = val
					}
				} else if strings.Contains(testid, "bathroom") || strings.Contains(testid, "ba") {
					if bathrooms == 0 {
						bathrooms = val
					}
				} else if strings.Contains(testid, "bed") && !strings.Contains(testid, "bedroom") {
					if beds == 0 {
						beds = val
					}
				}
			}
		}
	})

	// Get all text content that might contain room info
	fullText := strings.ToLower(doc.Text())

	// More comprehensive patterns (case-insensitive)
	patterns := []struct {
		re    *regexp.Regexp
		field *int
	}{
		// Bedrooms patterns
		{regexp.MustCompile(`(?i)(\d+)\s*(?:bedroom|br|bedrooms)`), &bedrooms},
		{regexp.MustCompile(`(?i)(\d+)\s*br\b`), &bedrooms},
		// Bathrooms patterns
		{regexp.MustCompile(`(?i)(\d+)\s*(?:bathroom|ba|bathrooms|bath)`), &bathrooms},
		{regexp.MustCompile(`(?i)(\d+)\s*ba\b`), &bathrooms},
		// Beds patterns (but not bedrooms)
		{regexp.MustCompile(`(?i)(\d+)\s+bed\b(?!room)`), &beds},
		{regexp.MustCompile(`(?i)(\d+)\s+beds\b(?!room)`), &beds},
	}

	for _, p := range patterns {
		if *p.field == 0 { // Only set if not already found
			matches := p.re.FindStringSubmatch(fullText)
			if len(matches) > 1 {
				if val, err := strconv.Atoi(matches[1]); err == nil {
					*p.field = val
				}
			}
		}
	}

	// Try to find in summary sections (common Airbnb pattern: "1 bed, 1 bath")
	summaryPattern := regexp.MustCompile(`(?i)(\d+)\s*(?:bed|br)\s*[,\s]+\s*(\d+)\s*(?:bath|ba)`)
	matches := summaryPattern.FindStringSubmatch(fullText)
	if len(matches) >= 3 {
		if beds == 0 {
			if val, err := strconv.Atoi(matches[1]); err == nil {
				beds = val
			}
		}
		if bathrooms == 0 {
			if val, err := strconv.Atoi(matches[2]); err == nil {
				bathrooms = val
			}
		}
	}

	// Look for structured data in meta tags or JSON-LD
	doc.Find("script[type='application/ld+json']").Each(func(i int, s *goquery.Selection) {
		jsonText := s.Text()
		// Try to extract from JSON-LD (basic pattern matching)
		if strings.Contains(jsonText, "numberOfBedrooms") {
			re := regexp.MustCompile(`"numberOfBedrooms"\s*:\s*(\d+)`)
			matches := re.FindStringSubmatch(jsonText)
			if len(matches) > 1 && bedrooms == 0 {
				if val, err := strconv.Atoi(matches[1]); err == nil {
					bedrooms = val
				}
			}
		}
		if strings.Contains(jsonText, "numberOfBathroomsTotal") {
			re := regexp.MustCompile(`"numberOfBathroomsTotal"\s*:\s*(\d+)`)
			matches := re.FindStringSubmatch(jsonText)
			if len(matches) > 1 && bathrooms == 0 {
				if val, err := strconv.Atoi(matches[1]); err == nil {
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
		regexp.MustCompile(`(\w+ \d{1,2}, \d{4})`),                    // "March 15, 2024"
		regexp.MustCompile(`(\w+\.?\s+\d{1,2}, \d{4})`),              // "Mar. 15, 2024" or "Mar 15, 2024"
		regexp.MustCompile(`(\d{1,2}/\d{1,2}/\d{4})`),                // "3/15/2024"
		regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`),                    // "2024-03-15"
		regexp.MustCompile(`(\d{1,2}\.\d{1,2}\.\d{4})`),              // "15.03.2024"
		regexp.MustCompile(`(\d{1,2}\s+\w+\s+\d{4})`),                // "15 March 2024"
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

