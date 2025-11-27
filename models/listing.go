package models

import "time"

// Listing represents a Bnb listing
type Listing struct {
	Title       string
	Price       float64
	Currency    string // Currency symbol/code (฿, $, €, ₫, etc.)
	Stars       float64
	ReviewCount int
	URL         string
	PageNumber  int         // Page number where this listing was found
	AllPrices   []PriceInfo // For debugging: all prices found

	// Detail page fields
	IsSuperhost      bool
	IsGuestFavorite  bool
	Bedrooms         float64
	Bathrooms        float64
	Beds             float64
	Description      string
	HouseRules       string
	NewestReviewDate *time.Time
	Reviews          []Review
}

// PriceInfo represents a price found in the listing
type PriceInfo struct {
	Price    float64
	Currency string
	Text     string
	IsStrike bool
	Index    int
}

// Review represents a review for a listing
type Review struct {
	Date         time.Time
	Score        float64
	FullText     string
	TimeOnAirbnb string // How long the user has been on Airbnb
}
