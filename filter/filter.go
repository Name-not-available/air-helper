package filter

import (
	"airbnb-scraper/config"
	"airbnb-scraper/models"
)

// Filter applies filter criteria to listings
type Filter struct {
	cfg *config.FilterConfig
}

// NewFilter creates a new Filter instance
func NewFilter(cfg *config.FilterConfig) *Filter {
	return &Filter{
		cfg: cfg,
	}
}

// ApplyFilters filters listings based on the configuration
func (f *Filter) ApplyFilters(listings []models.Listing) []models.Listing {
	var filtered []models.Listing

	for _, listing := range listings {
		if f.matchesFilters(listing) {
			filtered = append(filtered, listing)
		}
	}

	return filtered
}

// matchesFilters checks if a listing matches all filter criteria
func (f *Filter) matchesFilters(listing models.Listing) bool {
	// Check minimum reviews
	if listing.ReviewCount < f.cfg.Filters.MinReviews {
		return false
	}

	// Check price range - only filter if price was successfully extracted (price > 0)
	// If price is 0, it means we couldn't extract it, so we don't filter by price
	if listing.Price > 0 {
		if listing.Price < f.cfg.Filters.MinPrice || listing.Price > f.cfg.Filters.MaxPrice {
			return false
		}
	}

	// Star rating filter removed per user request

	return true
}




