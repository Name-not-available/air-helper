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
	// Get all text content that might contain room info
	fullText := doc.Text()

	// Pattern for bedrooms: "X bedroom" or "X bedrooms"
	bedroomRe := regexp.MustCompile(`(\d+)\s+bedroom`)
	matches := bedroomRe.FindStringSubmatch(fullText)
	if len(matches) > 1 {
		if val, err := strconv.Atoi(matches[1]); err == nil {
			bedrooms = val
		}
	}

	// Pattern for bathrooms: "X bathroom" or "X bathrooms"
	bathroomRe := regexp.MustCompile(`(\d+)\s+bathroom`)
	matches = bathroomRe.FindStringSubmatch(fullText)
	if len(matches) > 1 {
		if val, err := strconv.Atoi(matches[1]); err == nil {
			bathrooms = val
		}
	}

	// Pattern for beds: "X bed" or "X beds"
	bedRe := regexp.MustCompile(`(\d+)\s+bed\b`)
	matches = bedRe.FindStringSubmatch(fullText)
	if len(matches) > 1 {
		if val, err := strconv.Atoi(matches[1]); err == nil {
			beds = val
		}
	}

	// Also try to find in specific elements
	doc.Find("[data-testid='bedroom-count'], [data-testid='bathroom-count'], [data-testid='bed-count']").Each(func(i int, s *goquery.Selection) {
		text := strings.ToLower(s.Text())
		testid, _ := s.Attr("data-testid")

		if strings.Contains(testid, "bedroom") {
			if val, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
				bedrooms = val
			}
		} else if strings.Contains(testid, "bathroom") {
			if val, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
				bathrooms = val
			}
		} else if strings.Contains(testid, "bed") && !strings.Contains(testid, "bedroom") {
			if val, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
				beds = val
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
	// Common selectors for house rules
	houseRulesSelectors := []string{
		"[data-testid='house-rules']",
		"[data-section-id='HOUSE_RULES_DEFAULT']",
		"#house-rules",
		"._1y6fhhr:contains('House rules')",
	}

	for _, selector := range houseRulesSelectors {
		rules := doc.Find(selector).First().Text()
		if rules != "" {
			return strings.TrimSpace(rules)
		}
	}

	// Look for section containing "House rules"
	doc.Find("section, div").Each(func(i int, s *goquery.Selection) {
		text := strings.ToLower(s.Text())
		if strings.Contains(text, "house rules") {
			rules := s.Text()
			if len(rules) > 20 {
				return
			}
		}
	})

	return ""
}

// extractReviews extracts all reviews from the page
func (dp *DetailParser) extractReviews(doc *goquery.Document) ([]models.Review, *time.Time) {
	var reviews []models.Review
	var newestDate *time.Time

	// Find all review containers
	doc.Find("[data-testid='review'], [data-testid='review-item'], section[aria-label*='review']").Each(func(i int, s *goquery.Selection) {
		review := dp.extractSingleReview(s)
		if review != nil {
			reviews = append(reviews, *review)
			// Track newest date
			if newestDate == nil || review.Date.After(*newestDate) {
				newestDate = &review.Date
			}
		}
	})

	// If no reviews found with specific selectors, try to find review sections
	if len(reviews) == 0 {
		doc.Find("div, section").Each(func(i int, s *goquery.Selection) {
			// Check if this looks like a review container
			text := strings.ToLower(s.Text())
			if strings.Contains(text, "review") && (strings.Contains(text, "star") || strings.Contains(text, "rating")) {
				review := dp.extractSingleReview(s)
				if review != nil {
					reviews = append(reviews, *review)
					if newestDate == nil || review.Date.After(*newestDate) {
						newestDate = &review.Date
					}
				}
			}
		})
	}

	return reviews, newestDate
}

// extractSingleReview extracts a single review from a selection
func (dp *DetailParser) extractSingleReview(s *goquery.Selection) *models.Review {
	review := &models.Review{}

	// Extract date
	dateStr := dp.extractReviewDate(s)
	if dateStr == "" {
		return nil // Need at least a date
	}

	parsedDate, err := dp.parseDate(dateStr)
	if err != nil {
		return nil
	}
	review.Date = parsedDate

	// Extract score
	review.Score = dp.extractReviewScore(s)

	// Extract full text
	review.FullText = dp.extractReviewText(s)

	// Extract time on Airbnb
	review.TimeOnAirbnb = dp.extractTimeOnAirbnb(s)

	return review
}

// extractReviewDate extracts the review date
func (dp *DetailParser) extractReviewDate(s *goquery.Selection) string {
	// Look for date elements
	dateSelectors := []string{
		"[data-testid='review-date']",
		"time",
		"[datetime]",
	}

	for _, selector := range dateSelectors {
		dateStr := s.Find(selector).First().AttrOr("datetime", "")
		if dateStr != "" {
			return dateStr
		}
		dateStr = s.Find(selector).First().Text()
		if dateStr != "" {
			return strings.TrimSpace(dateStr)
		}
	}

	// Try to find date patterns in text
	text := s.Text()
	datePatterns := []*regexp.Regexp{
		regexp.MustCompile(`(\w+ \d{1,2}, \d{4})`), // "March 15, 2024"
		regexp.MustCompile(`(\d{1,2}/\d{1,2}/\d{4})`), // "3/15/2024"
		regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`),    // "2024-03-15"
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

// parseDate parses various date formats
func (dp *DetailParser) parseDate(dateStr string) (time.Time, error) {
	// Try various date formats
	formats := []string{
		time.RFC3339,
		"2006-01-02",
		"January 2, 2006",
		"Jan 2, 2006",
		"1/2/2006",
		"01/02/2006",
		"2006-01-02T15:04:05Z07:00",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	// If all formats fail, return error
	return time.Time{}, fmt.Errorf("unable to parse date: %s", dateStr)
}

