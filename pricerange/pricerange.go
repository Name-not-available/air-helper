package pricerange

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// DefaultStep is the default price range step size in dollars
const DefaultStep = 50

// PriceRangeURL represents a URL with price range parameters
type PriceRangeURL struct {
	URL   string
	Label string // e.g., "$0-$50"
	Min   int
	Max   int
}

// GeneratePriceRangeURLs takes a URL and generates multiple URLs with price range steps.
// Each generated URL has price_min and price_max set to a $step range.
// If the URL has no price_max, the original URL is returned as-is.
func GeneratePriceRangeURLs(urlStr string, step int) ([]PriceRangeURL, error) {
	if step <= 0 {
		step = DefaultStep
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	query := parsedURL.Query()

	// Extract price_max from URL
	priceMaxStr := query.Get("price_max")
	if priceMaxStr == "" {
		// No price_max in URL, return original URL as-is
		return []PriceRangeURL{{URL: urlStr, Label: "all prices", Min: 0, Max: 0}}, nil
	}

	priceMax, err := strconv.Atoi(priceMaxStr)
	if err != nil {
		return nil, fmt.Errorf("invalid price_max value: %s", priceMaxStr)
	}

	// Extract price_min from URL (default to 0 if not present)
	priceMin := 0
	priceMinStr := query.Get("price_min")
	if priceMinStr != "" {
		priceMin, err = strconv.Atoi(priceMinStr)
		if err != nil {
			priceMin = 0
		}
	}

	if priceMax <= priceMin {
		return []PriceRangeURL{{URL: urlStr, Label: fmt.Sprintf("$%d-$%d", priceMin, priceMax), Min: priceMin, Max: priceMax}}, nil
	}

	// Generate price range URLs
	var ranges []PriceRangeURL

	for rangeMin := priceMin; rangeMin < priceMax; rangeMin += step {
		rangeMax := rangeMin + step
		if rangeMax > priceMax {
			rangeMax = priceMax
		}

		// Skip ranges that are too narrow (less than 1)
		if rangeMax <= rangeMin {
			continue
		}

		// Clone the query parameters
		newQuery := make(url.Values)
		for k, v := range query {
			// Handle selected_filter_order[] - update price-related entries
			if k == "selected_filter_order[]" || k == "selected_filter_order%5B%5D" {
				var filtered []string
				for _, val := range v {
					if !strings.HasPrefix(val, "price_min:") && !strings.HasPrefix(val, "price_max:") {
						filtered = append(filtered, val)
					}
				}
				// Add updated price entries
				filtered = append(filtered, fmt.Sprintf("price_min:%d", rangeMin))
				filtered = append(filtered, fmt.Sprintf("price_max:%d", rangeMax))
				newQuery[k] = filtered
				continue
			}
			newQuery[k] = v
		}

		// Set new price range
		newQuery.Set("price_min", strconv.Itoa(rangeMin))
		newQuery.Set("price_max", strconv.Itoa(rangeMax))

		// Build new URL
		newParsedURL := *parsedURL
		newParsedURL.RawQuery = newQuery.Encode()

		label := fmt.Sprintf("$%d-$%d", rangeMin, rangeMax)
		ranges = append(ranges, PriceRangeURL{
			URL:   newParsedURL.String(),
			Label: label,
			Min:   rangeMin,
			Max:   rangeMax,
		})
	}

	if len(ranges) == 0 {
		// Fallback: return original URL
		return []PriceRangeURL{{URL: urlStr, Label: fmt.Sprintf("$%d-$%d", priceMin, priceMax), Min: priceMin, Max: priceMax}}, nil
	}

	return ranges, nil
}

// ExtractPriceRangeLabel extracts price_min and price_max from a URL
// and returns a label like "$0-$50"
func ExtractPriceRangeLabel(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	query := parsedURL.Query()
	priceMinStr := query.Get("price_min")
	priceMaxStr := query.Get("price_max")

	if priceMinStr == "" && priceMaxStr == "" {
		return "all prices"
	}

	priceMin := 0
	if priceMinStr != "" {
		priceMin, _ = strconv.Atoi(priceMinStr)
	}

	priceMax := 0
	if priceMaxStr != "" {
		priceMax, _ = strconv.Atoi(priceMaxStr)
	}

	return fmt.Sprintf("$%d-$%d", priceMin, priceMax)
}

// CountRanges returns how many $step ranges fit between priceMin and priceMax
func CountRanges(priceMin, priceMax, step int) int {
	if step <= 0 || priceMax <= priceMin {
		return 1
	}
	count := (priceMax - priceMin) / step
	if (priceMax-priceMin)%step != 0 {
		count++
	}
	return count
}
