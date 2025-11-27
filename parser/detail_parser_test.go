package parser

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParseRoomValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected float64
		wantErr  bool
	}{
		// Decimal values
		{"decimal 2.5", "2.5", 2.5, false},
		{"decimal 2,5", "2,5", 2.5, false},
		{"decimal 1.0", "1.0", 1.0, false},
		{"integer 2", "2", 2.0, false},
		{"decimal 0.5", "0.5", 0.5, false},

		// Mixed fractions
		{"mixed 2 1/2", "2 1/2", 2.5, false},
		{"mixed 1 1/2", "1 1/2", 1.5, false},
		{"mixed 3 1/4", "3 1/4", 3.25, false},
		{"mixed with spaces 2  1 / 2", "2  1 / 2", 2.5, false},

		// Simple fractions
		{"simple 1/2", "1/2", 0.5, false},
		{"simple 3/4", "3/4", 0.75, false},
		{"simple 1/4", "1/4", 0.25, false},

		// Unicode fractions with whole numbers
		{"unicode 2½", "2½", 2.5, false},
		{"unicode 1½", "1½", 1.5, false},
		{"unicode 2¼", "2¼", 2.25, false},
		{"unicode 3¾", "3¾", 3.75, false},
		{"unicode 1⅓", "1⅓", 1.0 + 1.0/3.0, false},

		// Standalone unicode fractions
		{"unicode standalone ½", "½", 0.5, false},
		{"unicode standalone ¼", "¼", 0.25, false},
		{"unicode standalone ¾", "¾", 0.75, false},
		{"unicode standalone ⅓", "⅓", 1.0 / 3.0, false},
		{"unicode standalone ⅔", "⅔", 2.0 / 3.0, false},

		// Edge cases
		{"empty string", "", 0, true},
		{"invalid text", "abc", 0, true},
		{"zero denominator", "1/0", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRoomValue(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRoomValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				// Use approximate comparison for floating point
				diff := got - tt.expected
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.0001 {
					t.Errorf("parseRoomValue() = %v, want %v", got, tt.expected)
				}
			}
		})
	}
}

func TestExtractNumericToken(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"decimal in text", "2.5 bathrooms", "2.5"},
		{"mixed fraction in text", "2 1/2 bathrooms", "2 1/2"},
		{"unicode fraction in text", "2½ bathrooms", "2½"},
		{"simple fraction in text", "1/2 bathroom", "1/2"},
		{"integer in text", "2 bathrooms", "2"},
		{"decimal with comma", "2,5 bathrooms", "2,5"},
		{"multiple numbers", "Room 61 has 2.5 bathrooms", "61"},
		{"no number", "bathrooms", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractNumericToken(tt.input)
			if got != tt.expected {
				t.Errorf("extractNumericToken() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractRoomCounts_Bathrooms(t *testing.T) {
	tests := []struct {
		name      string
		html      string
		expected  float64
		fieldName string
	}{
		{
			name:      "decimal bathroom in data-testid",
			html:      `<div data-testid="bathroom">2.5</div>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "unicode fraction bathroom in data-testid",
			html:      `<div data-testid="bathroom">2½</div>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "mixed fraction bathroom in data-testid",
			html:      `<div data-testid="bathroom">2 1/2</div>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "decimal bathroom in text pattern",
			html:      `<body>2.5 bathrooms</body>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "unicode fraction bathroom in text pattern",
			html:      `<body>2½ bathrooms</body>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "mixed fraction bathroom in text pattern",
			html:      `<body>2 1/2 bathrooms</body>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "bathroom in summary pattern",
			html:      `<body>3 beds, 2.5 baths</body>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "unicode fraction in summary pattern",
			html:      `<body>3 beds, 2½ baths</body>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "mixed fraction in summary pattern",
			html:      `<body>3 beds, 2 1/2 baths</body>`,
			expected:  2.5,
			fieldName: "bathrooms",
		},
		{
			name:      "integer bathroom",
			html:      `<div data-testid="bathroom">2</div>`,
			expected:  2.0,
			fieldName: "bathrooms",
		},
		{
			name:      "avoid room number false match",
			html:      `<body>Room 61 bathroom</body>`,
			expected:  0.0, // Should not match "61" as bathroom count
			fieldName: "bathrooms",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			parser := NewDetailParser()
			bedrooms, bathrooms, beds := parser.extractRoomCounts(doc)

			var got float64
			switch tt.fieldName {
			case "bathrooms":
				got = bathrooms
			case "bedrooms":
				got = bedrooms
			case "beds":
				got = beds
			}

			diff := got - tt.expected
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.0001 {
				t.Errorf("extractRoomCounts() %s = %v, want %v", tt.fieldName, got, tt.expected)
			}
		})
	}
}

func TestExtractRoomCounts_BedroomsAndBeds(t *testing.T) {
	tests := []struct {
		name             string
		html             string
		expectedBeds     float64
		expectedBedrooms float64
	}{
		{
			name:             "decimal beds and bedrooms",
			html:             `<div data-testid="bed">3.3</div><div data-testid="bedroom">10.1</div>`,
			expectedBeds:     3.3,
			expectedBedrooms: 10.1,
		},
		{
			name:             "summary pattern with decimals",
			html:             `<body>3.3 beds, 2.5 baths</body>`,
			expectedBeds:     3.3,
			expectedBedrooms: 0.0,
		},
		{
			name:             "bedroom not confused with bed",
			html:             `<body>2 bedrooms, 3 beds</body>`,
			expectedBeds:     3.0,
			expectedBedrooms: 2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tt.html))
			if err != nil {
				t.Fatalf("Failed to parse HTML: %v", err)
			}

			parser := NewDetailParser()
			bedrooms, _, beds := parser.extractRoomCounts(doc)

			diffBeds := beds - tt.expectedBeds
			if diffBeds < 0 {
				diffBeds = -diffBeds
			}
			if diffBeds > 0.0001 {
				t.Errorf("extractRoomCounts() beds = %v, want %v", beds, tt.expectedBeds)
			}

			diffBedrooms := bedrooms - tt.expectedBedrooms
			if diffBedrooms < 0 {
				diffBedrooms = -diffBedrooms
			}
			if diffBedrooms > 0.0001 {
				t.Errorf("extractRoomCounts() bedrooms = %v, want %v", bedrooms, tt.expectedBedrooms)
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"regular spaces", "2  1/2", "2 1/2"},
		{"non-breaking space", "2\u00A01/2", "2 1/2"},
		{"mixed whitespace", "2\t\n1/2", "2 1/2"},
		{"already normalized", "2 1/2", "2 1/2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeWhitespace(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeWhitespace() = %q, want %q", got, tt.expected)
			}
		})
	}
}
