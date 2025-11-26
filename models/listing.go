package models

// Listing represents a Bnb listing
type Listing struct {
	Title       string
	Price       float64
	Currency    string // Currency symbol/code (฿, $, €, ₫, etc.)
	Stars       float64
	ReviewCount int
	URL         string
	PageNumber  int    // Page number where this listing was found
	AllPrices   []PriceInfo // For debugging: all prices found
}

// PriceInfo represents a price found in the listing
type PriceInfo struct {
	Price    float64
	Currency string
	Text     string
	IsStrike bool
	Index    int
}




